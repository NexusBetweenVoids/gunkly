import { defineConfig, devices } from '@playwright/test';

// Signs in to a real identity provider (Keycloak or Authentik). The workflow
// .github/workflows/sso-idp.yml starts the provider and Gunkly first; this config only drives a browser.
//   IDP_PROVIDER=keycloak|authentik  GUNKLY_URL=http://localhost:4865  GUNKLY_SETUP_CODE=<from the server log>
export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  outputDir: '.out',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  reporter: [['list']],
  use: {
    baseURL: process.env.GUNKLY_URL ?? 'http://localhost:4865',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    locale: 'en-US',
  },
  projects: [{ name: 'desktop', use: { ...devices['Desktop Chrome'] } }],
});
