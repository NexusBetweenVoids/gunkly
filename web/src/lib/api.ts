// The only network client. Talks exclusively to same-origin /api/v1.
import { href } from './base';

const BASE = href('/api/v1');
const CSRF_COOKIE = 'gunkly_csrf';

export class ApiError extends Error {
  readonly status: number; // 0 = network failure
  readonly code: string;
  readonly retryAfter: number; // seconds, from Retry-After on 429; 0 if absent
  readonly reason: string; // validation detail (import files); '' if absent
  readonly path: string; // offending field for reason
  readonly body: unknown; // the whole response body (owner_required lists the candidates)
  constructor(status: number, code: string, retryAfter = 0, reason = '', path = '', body: unknown = null) {
    super(code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
    this.reason = reason;
    this.path = path;
    this.body = body;
  }
}

function csrfToken(): string {
  for (const part of document.cookie.split(';')) {
    const [k, ...v] = part.trim().split('=');
    if (k === CSRF_COOKIE) return decodeURIComponent(v.join('='));
  }
  return '';
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

export async function api<T>(method: Method, path: string, body?: unknown): Promise<T> {
  let res = await send(method, path, body);
  // The CSRF cookie can vanish under a page, for example when another tab signs out (its response
  // deletes the cookie). GET /session hands out a fresh one; retry the request once with it.
  if (res.status === 403 && method !== 'GET') {
    const data: unknown = await res.clone().json().catch(() => null);
    if ((data as { error?: { code?: string } } | null)?.error?.code === 'csrf_failed') {
      await send('GET', '/session').catch(() => null);
      res = await send(method, path, body);
    }
  }
  if (res.status === 204) return undefined as T;
  const data: unknown = await res.json().catch(() => null);
  if (!res.ok) throw failed(res, data);
  return data as T;
}

async function send(method: Method, path: string, body?: unknown): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken();
  try {
    return await fetch(BASE + path, {
      method,
      headers,
      credentials: 'same-origin',
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'network');
  }
}

/** Builds the error for a non-2xx response. Body: {"error": {"code", "reason"?, "path"?}}. */
function failed(res: Response, data: unknown): ApiError {
  const e = (data as { error?: { code?: unknown; reason?: unknown; path?: unknown } } | null)?.error;
  const str = (v: unknown) => (typeof v === 'string' ? v : '');
  return new ApiError(res.status, str(e?.code) || `http_${res.status}`, Number(res.headers.get('Retry-After')) || 0, str(e?.reason), str(e?.path), data);
}

/** GETs a file download (an attachment) and hands it to the browser's save dialog. */
export async function download(path: string, fallbackName: string): Promise<void> {
  let res: Response;
  try {
    res = await fetch(BASE + path, { credentials: 'same-origin' });
  } catch {
    throw new ApiError(0, 'network');
  }
  if (!res.ok) throw failed(res, await res.json().catch(() => null));
  const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? fallbackName;
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}
