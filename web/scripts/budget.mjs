// Fails if gzipped JS or CSS in dist exceeds the budget.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, extname } from 'node:path';
import { gzipSync } from 'node:zlib';

const BUDGET = { '.js': 60 * 1024, '.css': 15 * 1024 };
const dist = new URL('../dist', import.meta.url).pathname;
const totals = { '.js': 0, '.css': 0 };

function walk(d) {
  for (const name of readdirSync(d)) {
    const p = join(d, name);
    if (statSync(p).isDirectory()) walk(p);
    else if (extname(p) in totals) {
      const gz = gzipSync(readFileSync(p), { level: 9 }).length;
      totals[extname(p)] += gz;
      console.log(`${p.slice(dist.length + 1).padEnd(40)} ${(gz / 1024).toFixed(2)} KB gz`);
    }
  }
}
walk(dist);

let ok = true;
for (const [ext, limit] of Object.entries(BUDGET)) {
  const over = totals[ext] > limit;
  ok &&= !over;
  console.log(`${over ? 'FAIL' : 'ok  '} ${ext.slice(1).toUpperCase()} total ${(totals[ext] / 1024).toFixed(2)} KB / ${limit / 1024} KB gz`);
}
if (totals['.js'] === 0) {
  console.error('No JS found in dist; run `npm run build` first.');
  ok = false;
}
process.exit(ok ? 0 : 1);
