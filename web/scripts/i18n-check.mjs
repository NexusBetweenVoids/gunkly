// Fails on: t('key') calls whose key is missing from en.json; en.json keys never
// referenced as a string literal in src; locale files with missing/extra keys.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, extname } from 'node:path';

const root = new URL('..', import.meta.url).pathname;
const dir = join(root, 'src/lib/i18n');
const load = (f) => JSON.parse(readFileSync(join(dir, f), 'utf8'));
const en = load('en.json');
const enKeys = new Set(Object.keys(en));
const errors = [];

function* walk(d) {
  for (const name of readdirSync(d)) {
    const p = join(d, name);
    if (statSync(p).isDirectory()) yield* walk(p);
    else if (['.svelte', '.ts'].includes(extname(p))) yield p;
  }
}

const literals = new Set();
for (const file of walk(join(root, 'src'))) {
  const src = readFileSync(file, 'utf8');
  for (const m of src.matchAll(/['"`]([a-z][\w]*(?:\.[\w]+)+)['"`]/g)) literals.add(m[1]);
  for (const m of src.matchAll(/\bt\(\s*['"`]([^'"`]+)['"`]/g)) {
    if (!enKeys.has(m[1])) errors.push(`${file.slice(root.length)}: missing key "${m[1]}" in en.json`);
  }
}
for (const k of enKeys) if (!literals.has(k)) errors.push(`en.json: unused key "${k}"`);

for (const f of readdirSync(dir).filter((f) => f.endsWith('.json') && f !== 'en.json')) {
  const keys = new Set(Object.keys(load(f)));
  for (const k of enKeys) if (!keys.has(k)) errors.push(`${f}: missing key "${k}"`);
  for (const k of keys) if (!enKeys.has(k)) errors.push(`${f}: unknown key "${k}"`);
}

if (errors.length) {
  console.error(errors.join('\n'));
  process.exit(1);
}
console.log(`i18n: ${enKeys.size} keys OK`);
