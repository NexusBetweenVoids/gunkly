// WebAuthn in the browser (ADR 0018): navigator.credentials directly, with base64url
// helpers, no library. Loaded on demand (import()) by the screens that use it.
import { api, ApiError } from './api';

type Json = Record<string, unknown>;
type Descriptor = { id: string; type: PublicKeyCredentialType; transports?: AuthenticatorTransport[] };
export type PasskeyAnswer = { challenge: string; credential: unknown };

export const passkeysSupported = (): boolean =>
  typeof window !== 'undefined' && !!window.PublicKeyCredential && !!navigator.credentials;

export function fromB64u(s: string): ArrayBuffer {
  const b = atob(s.replace(/-/g, '+').replace(/_/g, '/'));
  const out = new Uint8Array(b.length);
  for (let i = 0; i < b.length; i++) out[i] = b.charCodeAt(i);
  return out.buffer;
}

export function toB64u(buf: ArrayBuffer): string {
  let s = '';
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

const descriptors = (list: Descriptor[] | undefined): PublicKeyCredentialDescriptor[] =>
  (list ?? []).map((d) => ({ type: d.type, id: fromB64u(d.id), transports: d.transports }));

/** A cancelled or timed-out prompt is not an error worth showing; anything else is. */
function browserError(e: unknown): ApiError {
  if (e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError')) return new ApiError(0, 'cancelled');
  if (e instanceof DOMException && e.name === 'InvalidStateError') return new ApiError(0, 'passkey_exists');
  return new ApiError(0, 'passkey_failed');
}

async function run(fn: () => Promise<Credential | null>): Promise<PublicKeyCredential> {
  let c: Credential | null;
  try {
    c = await fn();
  } catch (e) {
    throw browserError(e);
  }
  if (!c) throw new ApiError(0, 'cancelled');
  return c as PublicKeyCredential;
}

/** The credential in the JSON form the server parses (binary values base64url). */
const base = (c: PublicKeyCredential, response: Json): Json => ({
  id: c.id,
  rawId: toB64u(c.rawId),
  type: c.type,
  response,
  clientExtensionResults: {},
});

/** Asks the authenticator to create a passkey for the options returned by the server. */
export async function createPasskey(options: { publicKey: Json }): Promise<unknown> {
  const pk = options.publicKey as {
    challenge: string;
    user: { id: string; name: string; displayName: string };
    excludeCredentials?: Descriptor[];
  } & Json;
  const c = await run(() =>
    navigator.credentials.create({
      publicKey: {
        ...pk,
        challenge: fromB64u(pk.challenge),
        user: { ...pk.user, id: fromB64u(pk.user.id) },
        excludeCredentials: descriptors(pk.excludeCredentials),
      } as PublicKeyCredentialCreationOptions,
    }),
  );
  const r = c.response as AuthenticatorAttestationResponse;
  return base(c, {
    clientDataJSON: toB64u(r.clientDataJSON),
    attestationObject: toB64u(r.attestationObject),
    transports: r.getTransports?.() ?? [],
  });
}

/** Asks for an assertion for the options returned by the server. */
export async function getPasskey(options: { publicKey: Json }): Promise<unknown> {
  const pk = options.publicKey as { challenge: string; allowCredentials?: Descriptor[] } & Json;
  const c = await run(() =>
    navigator.credentials.get({
      publicKey: {
        ...pk,
        challenge: fromB64u(pk.challenge),
        allowCredentials: descriptors(pk.allowCredentials),
      } as PublicKeyCredentialRequestOptions,
    }),
  );
  const r = c.response as AuthenticatorAssertionResponse;
  return base(c, {
    clientDataJSON: toB64u(r.clientDataJSON),
    authenticatorData: toB64u(r.authenticatorData),
    signature: toB64u(r.signature),
    userHandle: r.userHandle ? toB64u(r.userHandle) : undefined,
  });
}

/** Options call, prompt, answer: what the sign-in, second-factor and step-up flows send back. */
export async function assertion(optionsPath: string, body?: unknown): Promise<PasskeyAnswer> {
  const o = await api<{ challenge: string; options: { publicKey: Json } }>('POST', optionsPath, body);
  return { challenge: o.challenge, credential: await getPasskey(o.options) };
}
