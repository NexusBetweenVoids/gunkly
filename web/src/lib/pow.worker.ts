// Solves a sign-up proof-of-work challenge (ALTCHA-compatible, SHA-256) off the
// main thread: finds the number n in [0, maxnumber] with SHA-256(salt + n) = challenge.
// Messages in: { salt, challenge, maxnumber }. Out: { progress } now and then,
// then { number } (-1 when none was found).
type Job = { salt: string; challenge: string; maxnumber: number };
type Reply = { progress: number } | { number: number };

const post = (m: Reply): void => (self as unknown as { postMessage(m: Reply): void }).postMessage(m);

function fromHex(h: string): Uint8Array {
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(h.slice(2 * i, 2 * i + 2), 16);
  return out;
}

// A dedicated worker only receives messages from the page that created it;
// still, accept nothing but a well-formed job.
const isJob = (d: unknown): d is Job => {
  const j = d as Job;
  return (
    typeof j === 'object' && j !== null &&
    typeof j.salt === 'string' && j.salt.length <= 256 &&
    typeof j.challenge === 'string' && /^[0-9a-f]{64}$/.test(j.challenge) &&
    Number.isInteger(j.maxnumber) && j.maxnumber >= 0 && j.maxnumber <= 10_000_000
  );
};

addEventListener('message', (e: MessageEvent<unknown>) => {
  if (e.origin !== '' && e.origin !== self.location.origin) return;
  if (isJob(e.data)) void solve(e.data);
});

async function solve({ salt, challenge, maxnumber }: Job): Promise<void> {
  const want = fromHex(challenge);
  const enc = new TextEncoder();
  for (let n = 0; n <= maxnumber; n++) {
    const got = new Uint8Array(await crypto.subtle.digest('SHA-256', enc.encode(salt + n)));
    if (got.length === want.length && got.every((b, i) => b === want[i])) {
      post({ number: n });
      return;
    }
    if (n % 1000 === 0) post({ progress: n / maxnumber });
  }
  post({ number: -1 });
}
