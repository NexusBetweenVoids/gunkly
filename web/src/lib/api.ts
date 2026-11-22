// The only network client. Talks exclusively to same-origin /api/v1.
const BASE = '/api/v1';
const CSRF_COOKIE = 'gunkly_csrf';

export class ApiError extends Error {
  readonly status: number; // 0 = network failure
  readonly code: string;
  readonly retryAfter: number; // seconds, from Retry-After on 429; 0 if absent
  readonly reason: string; // validation detail (import files); '' if absent
  readonly path: string; // offending field for reason
  constructor(status: number, code: string, retryAfter = 0, reason = '', path = '') {
    super(code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
    this.reason = reason;
    this.path = path;
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
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken();

  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      credentials: 'same-origin',
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'network');
  }
  if (res.status === 204) return undefined as T;
  const data: unknown = await res.json().catch(() => null);
  if (!res.ok) throw failed(res, data);
  return data as T;
}

/** Builds the error for a non-2xx response. Body: {"error": {"code", "reason"?, "path"?}}. */
function failed(res: Response, data: unknown): ApiError {
  const e = (data as { error?: { code?: unknown; reason?: unknown; path?: unknown } } | null)?.error;
  const str = (v: unknown) => (typeof v === 'string' ? v : '');
  return new ApiError(res.status, str(e?.code) || `http_${res.status}`, Number(res.headers.get('Retry-After')) || 0, str(e?.reason), str(e?.path));
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
