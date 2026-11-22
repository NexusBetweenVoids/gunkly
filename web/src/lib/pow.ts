// Client side of the sign-up proof of work: fetch a challenge and solve it in a
// Web Worker, reporting progress. No dependency; see pow.worker.ts.
import { api } from './api';

export type Challenge = { algorithm: string; challenge: string; salt: string; maxnumber: number; signature: string };
export type Solution = Omit<Challenge, 'maxnumber'> & { number: number };

/** Gets a fresh challenge and solves it. onProgress receives 0..1. */
export async function solveChallenge(onProgress: (fraction: number) => void): Promise<Solution> {
  const c = await api<Challenge>('GET', '/signup/challenge');
  const worker = new Worker(new URL('./pow.worker.ts', import.meta.url), { type: 'module' });
  try {
    const number = await new Promise<number>((resolve, reject) => {
      worker.onmessage = (e: MessageEvent<{ progress?: number; number?: number }>) => {
        if (e.data.number !== undefined) resolve(e.data.number);
        else if (e.data.progress !== undefined) onProgress(e.data.progress);
      };
      worker.onerror = () => reject(new Error('pow worker failed'));
      worker.postMessage({ salt: c.salt, challenge: c.challenge, maxnumber: c.maxnumber });
    });
    if (number < 0) throw new Error('pow unsolved');
    return { algorithm: c.algorithm, challenge: c.challenge, salt: c.salt, signature: c.signature, number };
  } finally {
    worker.terminate();
  }
}
