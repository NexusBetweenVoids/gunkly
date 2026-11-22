import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { safeNext } from '../src/lib/safenext';

const origin = 'https://gunkly.example';
const cases: [string | null, string][] = [
  ['/chores?x=1#a', '/chores?x=1#a'],
  ['/', '/'],
  [null, '/'],
  ['', '/'],
  ['//evil.example', '/'],
  ['/\\evil.example', '/'],
  ['https://evil.example/x', '/'],
  ['javascript:alert(1)', '/'],
  ['relative/path', '/'],
  ['/\t/evil.example', '/'],
];

test('safeNext accepts only same-origin paths', () => {
  for (const [input, want] of cases) expect(safeNext(input, origin), String(input)).toBe(want);
});

test('service worker notification URL guard matches', () => {
  const src = readFileSync(new URL('../public/sw.js', import.meta.url), 'utf8');
  const fn = /function sameOriginUrl[\s\S]*?\n}\n/.exec(src)?.[0];
  expect(fn).toBeTruthy();
  const run = new Function('self', `${fn}; return sameOriginUrl;`)({ location: { origin } }) as (r: unknown) => URL;
  for (const [input, want] of cases) {
    if (input === null) continue;
    const got = run(input);
    expect(got.origin, String(input)).toBe(origin);
    expect(got.pathname + got.search + got.hash, String(input)).toBe(want);
  }
});
