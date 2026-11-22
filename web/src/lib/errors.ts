import { ApiError } from './api';
import { t, type Key } from './i18n';

// API error code -> translated message (err.<code>, or an alias). Unknown codes fall back to err.unknown.
const KEYS: readonly string[] = [
  'err.network',
  'err.csrf_failed',
  'err.unauthenticated',
  'err.forbidden',
  'err.invalid_credentials',
  'err.invalid_code',
  'err.not_found',
  'err.invalid',
  'err.invalid_setup_code',
  'err.password_invalid',
  'err.password_too_long',
  'err.password_too_short',
  'err.registration_disabled',
  'err.registration_failed',
  'err.disabled_in_hosted_mode',
  'err.admin_target',
  'err.step_up_required',
  'err.totp_already_enabled',
  'err.totp_not_pending',
  'err.two_factor_required',
  'err.invalid_token',
  'err.pow_invalid',
  'err.email_not_verified',
  'err.two_factor_setup_required',
  'err.two_factor_mandatory',
  'err.email_unavailable',
  'err.already_verified',
  'err.email_otp_already_enabled',
  'err.conflict',
  'err.limit_reached',
  'err.body_too_large',
  'err.invalid_channel_url',
  'err.service_disabled',
  'err.no_next_occurrence',
  'err.egress_guard_locked',
  'err.internal',
];
const ALIAS: Record<string, string> = { invalid_json: 'invalid', not_ready: 'internal', invalid_push_endpoint: 'invalid', invalid_push_keys: 'invalid' };

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
  return { code: e.code, message: t(KEYS.includes(k) ? (k as Key) : 'err.unknown') };
}
