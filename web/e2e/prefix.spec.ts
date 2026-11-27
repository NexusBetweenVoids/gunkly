import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { prefixFile } from './global-setup';

// Sub-path hosting: the app served at http://localhost:PORT/sub/ (GUNKLY_BASE_URL with a path).
const { url: base, setupCode } = JSON.parse(readFileSync(prefixFile, 'utf8')) as { url: string; setupCode: string };
const origin = new URL(base).origin;
const PASSWORD = 'correct horse battery staple';

test('core flows work under a path prefix', async ({ page, request }) => {
  // First-run setup, then the app lives under /sub/ and nowhere else.
  await page.goto(`${base}/`);
  await expect(page).toHaveURL(`${base}/setup`);
  await page.getByLabel('Setup code').fill(setupCode);
  await page.getByLabel('Display name').fill('Prefix Admin');
  await page.getByLabel('Username', { exact: true }).fill('prefixadmin');
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByRole('button', { name: 'Create administrator account' }).click();
  await expect(page).toHaveURL(`${base}/setup/guide`);
  await expect(page.getByTestId('address-check')).toBeVisible();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Skip the rest' }).click();
  await page.getByRole('button', { name: 'Finish' }).click();
  await expect(page).toHaveURL(`${base}/households/new`);

  // Household, then a chore, done from Home.
  await page.getByLabel('Household name').fill('Prefix Flat');
  await page.getByRole('button', { name: 'Create household' }).click();
  await expect(page).toHaveURL(`${base}/settings/household`);
  await page.goto(`${base}/chores/new`);
  await page.getByLabel('Name').fill('Sweep the porch');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Sweep the porch' })).toBeVisible();
  await page.getByRole('link', { name: 'Home' }).first().click(); // an in-app link keeps the prefix
  await expect(page).toHaveURL(`${base}/`);
  await page.getByRole('button', { name: 'Mark Sweep the porch as done' }).click();
  await expect(page.getByText('Done: Sweep the porch')).toBeVisible();

  // Settings through a link, and a reload on a deep link.
  await page.getByRole('link', { name: 'Settings' }).first().click();
  await expect(page).toHaveURL(new RegExp(`^${base}/settings`));
  await page.goto(`${base}/settings/profile`);
  await page.reload();
  await expect(page).toHaveURL(`${base}/settings/profile`);

  // The session cookie is scoped to the prefix; signed out, a deep link goes to sign-in and back.
  const cookies = await page.context().cookies();
  expect(cookies.find((c) => c.name === 'gunkly_session')?.path).toBe('/sub');
  await page.context().clearCookies();
  await page.goto(`${base}/settings`);
  await expect(page).toHaveURL(new RegExp(`^${base}/signin\\?next=%2Fsettings`));
  await page.getByLabel('Email or username').fill('prefixadmin');
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`^${base}/settings`));

  // Manifest, service worker and icons are reachable and prefixed; the worker's scope is the prefix.
  const manifest = await (await request.get(`${base}/manifest.webmanifest`)).json();
  expect(manifest).toMatchObject({ start_url: '/sub/', scope: '/sub/', id: '/sub/' });
  expect(manifest.icons[0].src).toBe('/sub/icons/icon-192.png');
  expect((await request.get(`${origin}${manifest.icons[0].src}`)).ok()).toBe(true);
  expect((await request.get(`${base}/sw.js`)).ok()).toBe(true);
  const scope = await page.evaluate(async () => (await navigator.serviceWorker.ready).scope);
  expect(scope).toBe(`${base}/`);

  // Nothing is served outside the prefix; / redirects into it.
  expect((await request.get(`${origin}/api/v1/session`)).status()).toBe(404);
  expect((await request.get(`${base}/healthz`)).ok()).toBe(true);
  expect((await request.get(`${origin}/`, { maxRedirects: 0 })).headers()['location']).toBe('/sub/');
});
