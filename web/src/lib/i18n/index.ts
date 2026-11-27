import core from './en/core.json';
import type account from './en/account.json';
import type admin from './en/admin.json';
import type auth from './en/auth.json';
import type calendar from './en/calendar.json';
import type channels from './en/channels.json';
import type chores from './en/chores.json';
import type household from './en/household.json';

// ICU-lite messages: "{name}" interpolation; plurals as an object keyed by
// Intl.PluralRules category, selected by params.count.
//
// Layout: ./<locale>/<area>.json. The "core" area ships in the entry chunk (English only; other
// locales' core is fetched before the first render); every other area is fetched on demand
// with loadMessages(), which Lazy calls together with the screen's code.
type Message = string | Partial<Record<Intl.LDMLPluralRule, string>>;
export type Key =
  | keyof typeof core
  | keyof typeof account
  | keyof typeof admin
  | keyof typeof auth
  | keyof typeof calendar
  | keyof typeof channels
  | keyof typeof chores
  | keyof typeof household;
export type Area = 'account' | 'admin' | 'auth' | 'calendar' | 'channels' | 'chores' | 'household';
type Params = Record<string, string | number>;
type Catalog = Record<string, Message>;

const DEFAULT = 'en';
const files = import.meta.glob<Catalog>(['./*/*.json', '!./en/core.json'], { import: 'default' });
const catalogs: Record<string, Catalog> = { [DEFAULT]: core };
const loaded = new Map<string, Promise<void>>();
const RTL = new Set(['ar', 'fa', 'he', 'ur', 'ps', 'yi']);

export const available = [...new Set([DEFAULT, ...Object.keys(files).map((f) => f.split('/')[1]!)])].sort();
// The language shown: the account's choice (remembered in this browser, see useLanguage), else the
// browser's languages, else English.
const PREF = 'gunkly.language';
const pick = (tag: string): string => {
  const base = tag.split('-')[0]!.toLowerCase();
  return available.includes(base) ? base : '';
};
const browser = navigator.languages.map(pick).find(Boolean) ?? DEFAULT;
function stored(): string {
  try {
    return localStorage.getItem(PREF) ?? '';
  } catch {
    return '';
  }
}
export const locale = pick(stored()) || browser;

/** Shows the account's language in this browser ('' = follow the browser). Messages are loaded once
 *  per page, so a change reloads the page; returns true when it does. Without storage nothing is
 *  remembered and nothing reloads (no loop). */
export function useLanguage(tag: string): boolean {
  const want = pick(tag);
  try {
    if (want) localStorage.setItem(PREF, want);
    else localStorage.removeItem(PREF);
  } catch {
    return false;
  }
  if ((want || browser) === locale) return false;
  location.reload();
  return true;
}

const plurals = new Intl.PluralRules(locale);
const numbers = new Intl.NumberFormat(locale);

function fetchArea(loc: string, area: string): Promise<void> {
  const id = `${loc}/${area}`;
  let p = loaded.get(id);
  if (!p) {
    const load = files[`./${id}.json`];
    p = load ? load().then((c) => void (catalogs[loc] = { ...catalogs[loc], ...c })) : Promise.resolve();
    loaded.set(id, p);
  }
  return p;
}

/** Fetches the message files for the given areas (and the core area for non-English locales). */
export async function loadMessages(...areas: (Area | 'core')[]): Promise<void> {
  const want = locale === DEFAULT ? areas : ['core', ...areas];
  await Promise.all([
    ...areas.filter((a) => a !== 'core').map((a) => fetchArea(DEFAULT, a)),
    ...want.map((a) => fetchArea(locale, a)),
  ]);
}

/** Whether a message exists (in the loaded areas), for keys built at run time. */
export const has = (key: string): boolean => key in (catalogs[locale] ?? {}) || key in catalogs[DEFAULT]!;

export function t(key: Key, params: Params = {}): string {
  const msg: Message = catalogs[locale]?.[key] ?? catalogs[DEFAULT]?.[key] ?? key;
  let s: string;
  if (typeof msg === 'string') {
    s = msg;
  } else {
    const n = Number(params.count ?? 0);
    s = msg[plurals.select(n)] ?? msg.other ?? '';
  }
  return s.replace(/\{(\w+)\}/g, (m, name: string) => {
    const v = params[name];
    if (v === undefined) return m;
    return typeof v === 'number' ? numbers.format(v) : v;
  });
}

export function formatDate(d: Date, opts: Intl.DateTimeFormatOptions = { dateStyle: 'full' }): string {
  return new Intl.DateTimeFormat(locale, opts).format(d);
}

export function applyLocale(): void {
  document.documentElement.lang = locale;
  document.documentElement.dir = RTL.has(locale) ? 'rtl' : 'ltr';
}

export function languageName(tag: string): string {
  return new Intl.DisplayNames(locale, { type: 'language' }).of(tag) ?? tag;
}
