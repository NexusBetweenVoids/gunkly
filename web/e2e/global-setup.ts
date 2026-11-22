import { type ChildProcess, execFileSync, spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startFakeSmtp } from './smtp';

const here = dirname(fileURLToPath(import.meta.url));
const web = resolve(here, '..');
const root = resolve(web, '..');
export const stateFile = join(here, '.out', 'state.json');
/** Every email the app sent, one JSON object per line (written by the fake SMTP server below). */
export const mailFile = join(here, '.out', 'mail.jsonl');

// Builds the web bundle and the Go binary, starts it on a fresh SQLite database
// and records the one-time setup code from its log. Returns the teardown.
export default async function globalSetup(): Promise<() => Promise<void>> {
  const port = process.env.E2E_PORT!;
  const dir = mkdtempSync(join(tmpdir(), 'gunkly-e2e-'));
  const bin = join(dir, 'gunkly');
  const run = (cmd: string, args: string[], cwd: string, env: NodeJS.ProcessEnv = {}) =>
    execFileSync(cmd, args, { cwd, stdio: 'inherit', env: { ...process.env, ...env } });

  run('npm', ['run', '-s', 'build'], web);
  run('go', ['build', '-o', bin, './cmd/gunkly'], root, { CGO_ENABLED: '0' });

  mkdirSync(dirname(mailFile), { recursive: true });
  const smtp = await startFakeSmtp(mailFile);
  const key = randomBytes(32).toString('base64');
  const server: ChildProcess = spawn(bin, ['serve'], {
    cwd: dir,
    env: {
      PATH: process.env.PATH,
      GUNKLY_DATABASE_URL: `sqlite://${join(dir, 'gunkly.db')}`,
      GUNKLY_LISTEN_ADDR: `127.0.0.1:${port}`,
      GUNKLY_BASE_URL: `http://127.0.0.1:${port}`,
      GUNKLY_SECRET_KEYS: `1:${key}`,
      // Lets tests give each simulated user their own client IP via X-Forwarded-For,
      // so the per-IP login and invite rate limits do not couple unrelated tests.
      GUNKLY_TRUSTED_PROXIES: '127.0.0.1/32',
      GUNKLY_LOG_LEVEL: 'info',
      // Email goes to the fake SMTP server above (plain, on loopback; the specs turn the egress guard off).
      GUNKLY_EMAIL_ENABLED: 'true',
      GUNKLY_SMTP_HOST: '127.0.0.1',
      GUNKLY_SMTP_PORT: String(smtp.port),
      GUNKLY_SMTP_TLS: 'none',
      GUNKLY_SMTP_FROM: 'Gunkly <gunkly@example.test>',
      // Small puzzle so sign-up specs stay fast.
      GUNKLY_SIGNUP_POW_MAX: '2000',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  let log = '';
  let exited = false;
  const collect = (b: Buffer) => (log += b.toString());
  server.stdout!.on('data', collect);
  server.stderr!.on('data', collect);
  server.on('exit', () => (exited = true));

  const deadline = Date.now() + 30_000;
  let code = '';
  while (Date.now() < deadline && !code) {
    if (exited) throw new Error(`gunkly exited early:\n${log}`);
    code = /setup_code"?[=:]\s*"?([A-Za-z0-9-]+)/.exec(log)?.[1] ?? '';
    if (!code) await new Promise((r) => setTimeout(r, 100));
  }
  if (!code) throw new Error(`setup code not found in server log:\n${log}`);

  mkdirSync(dirname(stateFile), { recursive: true });
  writeFileSync(stateFile, JSON.stringify({ setupCode: code }));

  return async () => {
    server.kill('SIGTERM');
    await smtp.close();
    await new Promise<void>((r) => (exited ? r() : server.on('exit', () => r())));
    rmSync(dir, { recursive: true, force: true });
  };
}
