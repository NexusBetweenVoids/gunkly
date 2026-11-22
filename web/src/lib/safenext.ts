// Same-origin path check for post-sign-in redirects. Mirrored in
// public/sw.js (a plain script that cannot import modules); keep in sync.
export function safeNext(next: string | null, origin: string = location.origin): string {
  if (!next || !next.startsWith('/') || next.startsWith('//') || next.startsWith('/\\')) return '/';
  try {
    const u = new URL(next, origin);
    if (u.origin !== origin) return '/';
    return u.pathname + u.search + u.hash;
  } catch {
    return '/';
  }
}
