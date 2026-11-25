import { locale, t } from './i18n';
import { app, currentHousehold } from './session.svelte';

// Calendar dates are "YYYY-MM-DD" strings; arithmetic happens on UTC midnights.
export const parseYmd = (s: string): Date => new Date(`${s}T00:00:00Z`);
export const ymd = (d: Date): string => d.toISOString().slice(0, 10);
export const addDays = (s: string, n: number): string => ymd(new Date(parseYmd(s).getTime() + n * 864e5));
export const diffDays = (a: string, b: string): number => Math.round((parseYmd(a).getTime() - parseYmd(b).getTime()) / 864e5);

function dateIn(zone: string | null | undefined): string {
  try {
    return new Intl.DateTimeFormat('en-CA', { timeZone: zone || undefined }).format(new Date());
  } catch {
    return new Intl.DateTimeFormat('en-CA').format(new Date());
  }
}

/** Today's date as the user sees it: their own time zone when set, else the household's.
 *  For what is shown (due today, overdue, the calendar's today). */
export const today = (): string => dateIn(app.user?.timeZone || currentHousehold()?.timezone);

/** Today's date in the household's time zone. For date inputs: due dates are household dates. */
export const householdToday = (): string => dateIn(currentHousehold()?.timezone);

/** First day of the week, 0 = Sunday. Mirrors the server default. */
export function weekStart(): number {
  if (app.user?.weekStart != null) return app.user.weekStart;
  const tag = navigator.languages[0] ?? locale;
  return /^(en-US|en-CA|ja)/.test(tag) ? 0 : 1;
}

export const fmt = (s: string, o: Intl.DateTimeFormatOptions = { dateStyle: 'medium' }): string =>
  new Intl.DateTimeFormat(locale, { ...o, timeZone: 'UTC' }).format(parseYmd(s));

export const fmtTime = (iso: string): string =>
  new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso));

export const clock = (minute: number): string =>
  new Intl.DateTimeFormat(locale, { timeStyle: 'short', timeZone: 'UTC' }).format(new Date(minute * 60000));

export function dueText(due: string): string {
  const n = diffDays(due, today());
  if (n < 0) return t('due.overdue', { count: -n });
  if (n === 0) return t('home.dueToday');
  if (n === 1) return t('due.tomorrow');
  return n < 7 ? fmt(due, { weekday: 'long' }) : fmt(due);
}

export const initials = (name: string): string =>
  name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => [...w][0]).join('').toUpperCase();
