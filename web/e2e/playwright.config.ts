import { execFileSync } from 'node:child_process';
import { defineConfig, devices } from '@playwright/test';

// One server for the whole run. Pick its port here, once, in the main process;
// workers inherit the environment, so every config load agrees on it.
if (!process.env.E2E_PORT) {
  process.env.E2E_PORT = execFileSync(
    process.execPath,
    ['-e', "const s=require('net').createServer().listen(0,'127.0.0.1',()=>{console.log(s.address().port);s.close()})"],
    { encoding: 'utf8' },
  ).trim();
}
if (!process.env.E2E_PREFIX_PORT) {
  process.env.E2E_PREFIX_PORT = execFileSync(
    process.execPath,
    ['-e', "const s=require('net').createServer().listen(0,'127.0.0.1',()=>{console.log(s.address().port);s.close()})"],
    { encoding: 'utf8' },
  ).trim();
}
// localhost, not 127.0.0.1: passkeys (WebAuthn) bind to a domain name; an IP address cannot be an RP ID.
const baseURL = `http://localhost:${process.env.E2E_PORT}`;

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  outputDir: '.out',
  globalSetup: './global-setup.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 7_000 },
  reporter: [['list']],
  use: { baseURL, trace: 'retain-on-failure', locale: 'en-US', timezoneId: 'Europe/Berlin' },
  projects: [
    // First-run setup must happen exactly once, before anything else.
    { name: 'setup', testMatch: 'setup.spec.ts', use: { ...devices['Desktop Chrome'] } },
    // Sub-path hosting: its own server under /sub, its own database; independent of the others.
    { name: 'prefix', testMatch: 'prefix.spec.ts', use: { ...devices['Desktop Chrome'] } },
    {
      name: 'mobile',
      testIgnore: ['setup.spec.ts', 'rtl.spec.ts', 'prefix.spec.ts'],
      dependencies: ['setup'],
      use: { ...devices['Pixel 7'] },
    },
    {
      name: 'desktop',
      testIgnore: ['setup.spec.ts', 'prefix.spec.ts'],
      dependencies: ['setup'],
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
