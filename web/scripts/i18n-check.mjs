// Message files live in src/lib/i18n/<locale>/<area>.json. Fails on: t('key') calls whose key
// is in no en file; en keys never referenced as a string literal in src (except dynamic prefixes); keys defined twice
// (within a file or across areas); locales whose file layout or keys differ from en.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, extname } from 'node:path';

const root = new URL('..', import.meta.url).pathname;
const dir = join(root, 'src/lib/i18n');
const errors = [];

const areas = (loc) => readdirSync(join(dir, loc)).filter((f) => f.endsWith('.json')).sort();
const locales = readdirSync(dir).filter((f) => statSync(join(dir, f)).isDirectory());

// Top-level keys of a file; JSON.parse would silently drop duplicates, so scan the text too.
function keysOf(loc, file) {
  const text = readFileSync(join(dir, loc, file), 'utf8');
  const obj = JSON.parse(text);
  const seen = new Set();
  for (const m of text.matchAll(/^ {2}"([^"]+)":/gm)) {
    if (seen.has(m[1])) errors.push(`${loc}/${file}: duplicate key "${m[1]}"`);
    seen.add(m[1]);
  }
  return Object.keys(obj);
}

const enFiles = areas('en');
const owner = new Map(); // en key -> file
for (const f of enFiles) {
  for (const k of keysOf('en', f)) {
    if (owner.has(k)) errors.push(`en/${f}: key "${k}" is also defined in en/${owner.get(k)}`);
    else owner.set(k, f);
  }
}

for (const loc of locales.filter((l) => l !== 'en')) {
  const files = areas(loc);
  for (const f of enFiles) if (!files.includes(f)) errors.push(`${loc}: missing file ${f}`);
  for (const f of files) if (!enFiles.includes(f)) errors.push(`${loc}: unknown file ${f}`);
  const all = new Set();
  for (const f of files.filter((f) => enFiles.includes(f))) {
    const keys = keysOf(loc, f);
    const want = new Set(keysOf('en', f));
    for (const k of want) if (!keys.includes(k)) errors.push(`${loc}/${f}: missing key "${k}"`);
    for (const k of keys) {
      if (!want.has(k)) errors.push(`${loc}/${f}: unknown key "${k}"`);
      if (all.has(k)) errors.push(`${loc}: duplicate key "${k}"`);
      all.add(k);
    }
  }
}

function* walk(d) {
  for (const name of readdirSync(d)) {
    const p = join(d, name);
    if (statSync(p).isDirectory()) yield* walk(p);
    else if (['.svelte', '.ts'].includes(extname(p))) yield p;
  }
}

// Keys looked up with a run-time suffix (API error codes, server template names, limit kinds, audit events).
const DYNAMIC = ['err.', 'area.', 'template.', 'limit.', 'audit.', 'secEvents.', 'emailFailing.reason.'];
const literals = new Set();
for (const file of walk(join(root, 'src'))) {
  const src = readFileSync(file, 'utf8');
  for (const m of src.matchAll(/['"`]([a-z][\w]*(?:\.[\w]+)+)['"`]/g)) literals.add(m[1]);
  for (const m of src.matchAll(/\bt\(\s*['"`]([^'"`]+)['"`]/g)) {
    if (!m[1].includes('${') && !owner.has(m[1])) errors.push(`${file.slice(root.length)}: missing key "${m[1]}" in en/*.json`);
  }
}
for (const k of owner.keys()) if (!literals.has(k) && !DYNAMIC.some((p) => k.startsWith(p))) errors.push(`en/${owner.get(k)}: unused key "${k}"`);

if (errors.length) {
  console.error(errors.join('\n'));
  process.exit(1);
}
console.log(`i18n: ${owner.size} keys in ${enFiles.length} files OK (${locales.length} locale${locales.length === 1 ? '' : 's'})`);
