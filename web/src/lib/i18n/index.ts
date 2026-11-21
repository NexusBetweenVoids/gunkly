import en from './en.json';

// ICU-lite messages: "{name}" interpolation; plurals as an object keyed by
// Intl.PluralRules category, selected by params.count.
type Message = string | Partial<Record<Intl.LDMLPluralRule, string>>;
export type Key = keyof typeof en;
type Params = Record<string, string | number>;

const catalogs: Record<string, Partial<Record<Key, Message>>> = { en };
const RTL = new Set(['ar', 'fa', 'he', 'ur', 'ps', 'yi']);

const pick = () =>
  navigator.languages.map((l) => l.split('-')[0]).find((l) => l in catalogs) ?? 'en';
export const locale = pick();

const plurals = new Intl.PluralRules(locale);
const numbers = new Intl.NumberFormat(locale);

export function t(key: Key, params: Params = {}): string {
  const msg: Message = catalogs[locale]?.[key] ?? en[key];
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

export const available = Object.keys(catalogs);

export function languageName(tag: string): string {
  return new Intl.DisplayNames(locale, { type: 'language' }).of(tag) ?? tag;
}
