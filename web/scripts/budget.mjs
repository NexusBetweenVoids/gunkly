// Fails if gzipped JS or CSS in dist exceeds the budget.
// "Initial" JS is what a cold load fetches before the first screen: the entry chunk
// plus everything it imports statically (from the Vite manifest). Screens loaded with
// dynamic import() only count toward the total.
import { readFileSync, readdirSync } from 'node:fs';
import { join, extname } from 'node:path';
import { gzipSync } from 'node:zlib';

const KB = 1024;
const BUDGET = { initialJs: 50 * KB, totalJs: 200 * KB, css: 15 * KB, locale: 40 * KB }; // total counts lazy screens; initial load is the strict one
// Translations other than English are data a browser fetches for its own language only: they
// count per language (locale budget), not toward the total.
const dist = new URL('../dist', import.meta.url).pathname;
const gz = new Map(); // dist-relative path -> gzipped size
let css = 0;

function walk(d) {
  for (const ent of readdirSync(d, { withFileTypes: true })) {
    const p = join(d, ent.name);
    if (ent.isDirectory()) walk(p);
    else if (extname(p) === '.js' || extname(p) === '.css') {
      const size = gzipSync(readFileSync(p), { level: 9 }).length;
      const rel = p.slice(dist.length + 1);
      if (extname(p) === '.js') gz.set(rel, size);
      else css += size;
      console.log(`${rel.padEnd(40)} ${(size / KB).toFixed(2)} KB gz`);
    }
  }
}
walk(dist);

let manifest;
try {
  manifest = JSON.parse(readFileSync(join(dist, '.vite', 'manifest.json'), 'utf8'));
} catch {
  console.error('No dist/.vite/manifest.json; run `npm run build` first.');
  process.exit(1);
}
const entryKey = Object.keys(manifest).find((k) => manifest[k].isEntry);
const initial = new Set();
(function visit(key) {
  const c = manifest[key];
  if (!c || initial.has(c.file)) return;
  initial.add(c.file);
  (c.imports ?? []).forEach(visit); // static imports only; dynamicImports are lazy
})(entryKey);

const sum = (files) => [...files].reduce((n, f) => n + (gz.get(f) ?? 0), 0);
const locales = new Map(); // locale -> its chunk files
for (const c of Object.values(manifest)) {
  const m = /src\/lib\/i18n\/([a-z-]+)\/[^/]+\.json$/.exec(c.src ?? '');
  if (m && m[1] !== 'en') locales.set(m[1], [...(locales.get(m[1]) ?? []), c.file]);
}
const localeFiles = new Set([...locales.values()].flat());
const results = [
  ['initial JS (entry + static imports)', sum(initial), BUDGET.initialJs],
  ['total JS (without translations)', sum([...gz.keys()].filter((f) => !localeFiles.has(f))), BUDGET.totalJs],
  ...[...locales].sort().map(([loc, files]) => [`translation ${loc}`, sum(files), BUDGET.locale]),
  ['CSS', css, BUDGET.css],
];
let ok = gz.size > 0;
for (const [what, size, limit] of results) {
  const over = size > limit;
  ok &&= !over;
  console.log(`${over ? 'FAIL' : 'ok  '} ${what} ${(size / KB).toFixed(2)} KB / ${limit / KB} KB gz`);
}
process.exit(ok ? 0 : 1);
