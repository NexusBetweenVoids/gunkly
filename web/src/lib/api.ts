// The only network client. Talks exclusively to same-origin /api/v1.
const BASE = '/api/v1';
const CSRF_COOKIE = 'gunkly_csrf';

export class ApiError extends Error {
  readonly status: number; // 0 = network failure
  readonly code: string;
  readonly retryAfter: number; // seconds, from Retry-After on 429; 0 if absent
  constructor(status: number, code: string, retryAfter = 0) {
    super(code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
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
  if (!res.ok) {
    // Expected error body: {"error": {"code": "..."}}; anything else falls back to http_<status>.
    const code = (data as { error?: { code?: unknown } } | null)?.error?.code;
    throw new ApiError(
      res.status,
      typeof code === 'string' ? code : `http_${res.status}`,
      Number(res.headers.get('Retry-After')) || 0,
    );
  }
  return data as T;
}
