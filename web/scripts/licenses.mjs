// Fails if any package in package-lock.json has a license outside the AGPL-3.0-compatible allowlist.
import { readFileSync } from 'node:fs';

const ALLOWED = new Set(['MIT', 'ISC', '0BSD', 'BSD-2-Clause', 'BSD-3-Clause', 'Apache-2.0', 'MPL-2.0', 'CC0-1.0']);
const lock = JSON.parse(readFileSync(new URL('../package-lock.json', import.meta.url), 'utf8'));
const bad = Object.entries(lock.packages)
  .filter(([path, p]) => path && !ALLOWED.has(p.license))
  .map(([path, p]) => `${path.replace(/.*node_modules\//, '')}: ${p.license ?? 'unknown'}`);

if (bad.length) {
  console.error(`Licenses needing review:\n${bad.join('\n')}`);
  process.exit(1);
}
console.log(`licenses: ${Object.keys(lock.packages).length - 1} packages OK`);
