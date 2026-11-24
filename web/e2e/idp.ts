import { createHash, createSign, generateKeyPairSync, randomBytes } from 'node:crypto';
import { createServer } from 'node:http';

// A tiny OpenID Connect provider for the single sign-on specs: discovery, JWKS (ES256, like
// Kanidm), an authorization endpoint that approves at once as whoever the spec set with
// POST /_next, and a token endpoint that checks PKCE and the client secret. Never part of the app.
export const IDP_CLIENT = 'gunkly-e2e';
export const IDP_SECRET = 'e2e-secret';

const b64u = (b: Buffer): string => b.toString('base64url');

export async function startFakeIdp(): Promise<{ url: string; close: () => Promise<void> }> {
  const { privateKey, publicKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'k1', use: 'sig', alg: 'ES256' };
  const signer = privateKey;
  let next: Record<string, unknown> = { sub: 'nobody' };
  const grants = new Map<string, { challenge: string; nonce: string; redirect: string; claims: Record<string, unknown> }>();
  let issuer = '';

  const sign = (claims: Record<string, unknown>): string => {
    const head = b64u(Buffer.from(JSON.stringify({ alg: 'ES256', kid: 'k1', typ: 'JWT' })));
    const body = b64u(Buffer.from(JSON.stringify(claims)));
    const sig = createSign('SHA256').update(`${head}.${body}`).sign({ key: signer, dsaEncoding: 'ieee-p1363' });
    return `${head}.${body}.${b64u(sig)}`;
  };
  const json = (res: import('node:http').ServerResponse, status: number, v: unknown): void => {
    res.writeHead(status, { 'Content-Type': 'application/json' }).end(JSON.stringify(v));
  };
  const readBody = (req: import('node:http').IncomingMessage): Promise<string> =>
    new Promise((resolve) => {
      let s = '';
      req.on('data', (c: Buffer) => (s += c.toString()));
      req.on('end', () => resolve(s));
    });

  const server = createServer((req, res) => {
    void (async () => {
      const u = new URL(req.url ?? '/', issuer);
      if (u.pathname === '/.well-known/openid-configuration') {
        return json(res, 200, {
          issuer,
          authorization_endpoint: `${issuer}/auth`,
          token_endpoint: `${issuer}/token`,
          jwks_uri: `${issuer}/keys`,
          response_types_supported: ['code'],
          subject_types_supported: ['public'],
          id_token_signing_alg_values_supported: ['ES256'],
          token_endpoint_auth_methods_supported: ['client_secret_basic', 'client_secret_post'],
          code_challenge_methods_supported: ['S256'],
        });
      }
      if (u.pathname === '/keys') return json(res, 200, { keys: [jwk] });
      if (u.pathname === '/_next' && req.method === 'POST') {
        next = JSON.parse(await readBody(req)) as Record<string, unknown>;
        return json(res, 200, {});
      }
      if (u.pathname === '/auth') {
        const q = u.searchParams;
        if (q.get('response_type') !== 'code' || q.get('code_challenge_method') !== 'S256' || q.get('client_id') !== IDP_CLIENT) {
          return json(res, 400, { error: 'invalid_request' });
        }
        const code = randomBytes(16).toString('hex');
        const claims = { ...next };
        if (q.get('prompt') === 'login') claims.auth_time = Math.floor(Date.now() / 1000);
        grants.set(code, { challenge: q.get('code_challenge') ?? '', nonce: q.get('nonce') ?? '', redirect: q.get('redirect_uri') ?? '', claims });
        const back = new URL(q.get('redirect_uri') ?? '');
        back.search = new URLSearchParams({ code, state: q.get('state') ?? '', iss: issuer }).toString();
        res.writeHead(302, { Location: back.toString() }).end();
        return;
      }
      if (u.pathname === '/token' && req.method === 'POST') {
        const form = new URLSearchParams(await readBody(req));
        const basic = /^Basic (.+)$/.exec(req.headers.authorization ?? '')?.[1];
        const [id, secret] = basic ? Buffer.from(basic, 'base64').toString().split(':') : [form.get('client_id'), form.get('client_secret')];
        const g = grants.get(form.get('code') ?? '');
        grants.delete(form.get('code') ?? '');
        const challenge = createHash('sha256')
          .update(form.get('code_verifier') ?? '')
          .digest('base64url');
        if (id !== IDP_CLIENT || secret !== IDP_SECRET || !g || g.challenge !== challenge || g.redirect !== form.get('redirect_uri')) {
          return json(res, 400, { error: 'invalid_grant' });
        }
        const now = Math.floor(Date.now() / 1000);
        return json(res, 200, {
          access_token: 'at',
          token_type: 'Bearer',
          id_token: sign({ iss: issuer, aud: IDP_CLIENT, iat: now, exp: now + 3600, nonce: g.nonce, ...g.claims }),
        });
      }
      json(res, 404, { error: 'not_found' });
    })();
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  const addr = server.address();
  if (!addr || typeof addr === 'string') throw new Error('no idp port');
  issuer = `http://127.0.0.1:${addr.port}`;
  return { url: issuer, close: () => new Promise<void>((r) => server.close(() => r())) };
}
