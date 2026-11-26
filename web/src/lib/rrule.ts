import { fmt } from './dates';
import { t, type Key } from './i18n';

// Builder state for the supported RRULE subset (see internal/schedule).
export type Rule = {
  freq: 'WEEKLY' | 'MONTHLY' | 'YEARLY';
  interval: number;
  weekdays: string[]; // WEEKLY: MO..SU
  months: number[]; // YEARLY: 1..12
  mode: 'none' | 'day' | 'nth'; // MONTHLY/YEARLY refinement
  day: number; // 1..31 or -1 (last day)
  nth: number; // 1..5 or -1 (last)
  weekday: string;
};
export const DAYS = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'];
export const newRule = (): Rule => ({
  freq: 'MONTHLY', interval: 1, weekdays: ['MO'], months: [], mode: 'day', day: 1, nth: 1, weekday: 'SA',
});

export function toRRule(r: Rule): string {
  const p = [`FREQ=${r.freq}`];
  if (r.interval > 1) p.push(`INTERVAL=${r.interval}`);
  if (r.freq === 'YEARLY' && r.months.length) p.push(`BYMONTH=${[...r.months].sort((a, b) => a - b).join(',')}`);
  if (r.freq === 'MONTHLY' && r.mode === 'day') p.push(`BYMONTHDAY=${r.day}`);
  if (r.freq === 'YEARLY' && r.mode === 'day' && r.months.length) p.push(`BYMONTHDAY=${r.day}`);
  if (r.freq === 'WEEKLY' && r.weekdays.length) p.push(`BYDAY=${DAYS.filter((d) => r.weekdays.includes(d)).join(',')}`);
  if (r.freq !== 'WEEKLY' && r.mode === 'nth' && (r.freq === 'MONTHLY' || r.months.length)) p.push(`BYDAY=${r.nth}${r.weekday}`);
  return p.join(';');
}

export function fromRRule(s: string): Rule {
  const r = { ...newRule(), mode: 'none' as Rule['mode'] };
  for (const part of s.split(';')) {
    const [k, v = ''] = part.split('=');
    if (k === 'FREQ' && ['WEEKLY', 'MONTHLY', 'YEARLY'].includes(v)) r.freq = v as Rule['freq'];
    else if (k === 'INTERVAL') r.interval = Number(v) || 1;
    else if (k === 'BYMONTH') r.months = v.split(',').map(Number);
    else if (k === 'BYMONTHDAY') [r.mode, r.day] = ['day', Number(v.split(',')[0])];
    else if (k === 'BYDAY') {
      const first = v.split(',')[0].match(/^(-?\d)?([A-Z]{2})$/);
      if (r.freq === 'WEEKLY') r.weekdays = v.split(',');
      else if (first?.[1]) [r.mode, r.nth, r.weekday] = ['nth', Number(first[1]), first[2]];
    }
  }
  return r;
}

const dayName = (code: string, style: 'long' | 'short' = 'long') =>
  fmt(`2024-01-0${DAYS.indexOf(code) + 1}`, { weekday: style }); // 2024-01-01 is a Monday
export const monthName = (m: number) => fmt(`2024-${String(m).padStart(2, '0')}-01`, { month: 'long' });
const list = (xs: string[]) => new Intl.ListFormat(undefined, { type: 'conjunction' }).format(xs);
const NTH: Record<number, Key> = {
  1: 'rule.nth.1', 2: 'rule.nth.2', 3: 'rule.nth.3', 4: 'rule.nth.4', 5: 'rule.nth.5', [-1]: 'rule.nth.last',
};
export const nthKey = (n: number): Key => NTH[n] ?? 'rule.nth.last';

/** Human-readable summary of a rule, e.g. "Every month, on the first Saturday". */
export function describe(r: Rule): string {
  const base = t(r.freq === 'WEEKLY' ? 'rule.week' : r.freq === 'MONTHLY' ? 'rule.month' : 'rule.year', { count: r.interval });
  const bits: string[] = [];
  if (r.freq === 'WEEKLY' && r.weekdays.length) bits.push(t('rule.onDays', { days: list(DAYS.filter((d) => r.weekdays.includes(d)).map((d) => dayName(d))) }));
  if (r.freq === 'YEARLY' && r.months.length) bits.push(t('rule.inMonths', { months: list([...r.months].sort((a, b) => a - b).map(monthName)) }));
  if (r.freq !== 'WEEKLY' && (r.freq === 'MONTHLY' || r.months.length)) {
    if (r.mode === 'nth') bits.push(t('rule.onNth', { nth: t(nthKey(r.nth)), day: dayName(r.weekday) }));
    else if (r.mode === 'day') bits.push(r.day === -1 ? t('rule.lastDay') : t('rule.onDay', { day: r.day }));
  }
  return bits.length ? t('rule.preview', { base, detail: bits.join(', ') }) : base;
}

/** Whether the rule builder shows this rule exactly (rules set through the API may use more of RRULE,
 *  for example BYSETPOS or several days of the month). */
export const representable = (s: string): boolean => norm(toRRule(fromRRule(s))) === norm(s);

// The same rule written differently compares equal: INTERVAL=1 is the default, and neither the order
// of the parts nor of a part's values matters.
const norm = (s: string): string =>
  s
    .split(';')
    .filter((p) => p && p !== 'INTERVAL=1')
    .map((p) => {
      const [k, v = ''] = p.split('=');
      return `${k}=${v.split(',').sort().join(',')}`;
    })
    .sort()
    .join(';');

/** Summary of a stored rule: described when the builder understands it, else shown as it is. */
export const describeRRule = (s: string): string => (representable(s) ? describe(fromRRule(s)) : t('rule.customRaw', { rule: s }));
