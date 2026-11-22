import { ApiError } from './api';
import { has, t, type Key } from './i18n';

// API error code -> translated message (err.<code>, or an alias). Unknown codes fall back to err.unknown.
const ALIAS: Record<string, string> = { invalid_json: 'invalid', not_ready: 'internal' };

function wait(seconds: number): string {
  const nf = (unit: string, n: number) =>
    new Intl.NumberFormat(undefined, { style: 'unit', unit, unitDisplay: 'long' }).format(n);
  if (seconds <= 0) return t('err.soon');
  return seconds >= 90 ? nf('minute', Math.ceil(seconds / 60)) : nf('second', seconds);
}

export type Failure = { code: string; message: string };

/** Translated message for any thrown value; empty for a cancelled step-up. */
export function failure(e: unknown): Failure {
  if (!(e instanceof ApiError)) return { code: 'unknown', message: t('err.unknown') };
  if (e.code === 'cancelled') return { code: e.code, message: '' };
  if (e.status === 429) return { code: 'rate_limited', message: t('err.rate_limited', { wait: wait(e.retryAfter) }) };
  const k = `err.${ALIAS[e.code] ?? e.code}`;
  return { code: e.code, message: t(has(k) ? (k as Key) : 'err.unknown') };
}
