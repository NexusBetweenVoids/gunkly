import { ApiError } from './api';
import { t, type Key } from './i18n';

// API error code -> translated message. Unknown codes fall back to err.unknown.
const MESSAGES: Record<string, Key> = {
  network: 'err.network',
  csrf_failed: 'err.csrf_failed',
  unauthenticated: 'err.unauthenticated',
  forbidden: 'err.forbidden',
  invalid_credentials: 'err.invalid_credentials',
  invalid_code: 'err.invalid_code',
  not_found: 'err.not_found',
  invalid: 'err.invalid',
  invalid_json: 'err.invalid',
  invalid_setup_code: 'err.invalid_setup_code',
  password_invalid: 'err.password_invalid',
  password_too_long: 'err.password_too_long',
  password_too_short: 'err.password_too_short',
  registration_disabled: 'err.registration_disabled',
  step_up_required: 'err.step_up_required',
  totp_already_enabled: 'err.totp_already_enabled',
  totp_not_pending: 'err.totp_not_pending',
  two_factor_required: 'err.two_factor_required',
  conflict: 'err.conflict',
  limit_reached: 'err.limit_reached',
  body_too_large: 'err.body_too_large',
  invalid_channel_url: 'err.invalid_channel_url',
  service_disabled: 'err.service_disabled',
  no_next_occurrence: 'err.no_next_occurrence',
  egress_guard_locked: 'err.egress_guard_locked',
  internal: 'err.internal',
  not_ready: 'err.internal',
};

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
  return { code: e.code, message: t(MESSAGES[e.code] ?? 'err.unknown') };
}

export const errorText = (e: unknown): string => failure(e).message;
