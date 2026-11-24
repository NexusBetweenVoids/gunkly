import { type APIRequestContext, type Page, expect, request, test } from '@playwright/test';
import { NAME, PASSWORD, USERS, throughProvider } from './providers';

// Signs in to Gunkly through a REAL identity provider. Runs in order: each test builds on the one before.
test.describe.configure({ mode: 'serial' });

const ADMIN = { username: 'idp-admin', password: 'ci-only-admin-password-1', displayName: 'IdP Admin' };
const GUNKLY = new URL(process.env.GUNKLY_URL ?? 'http://localhost:4865').origin;

class Api {
  constructor(readonly ctx: APIRequestContext) {}
  async call<T = unknown>(method: string, path: string, data?: unknown): Promise<T> {
    let c = (await this.ctx.storageState()).cookies.find((x) => x.name === 'gunkly_csrf');
    if (!c) {
      await this.ctx.get('/api/v1/session');
      c = (await this.ctx.storageState()).cookies.find((x) => x.name === 'gunkly_csrf');
    }
    const res = await this.ctx.fetch(`/api/v1${path}`, {
      method,
      data,
      headers: method === 'GET' ? {} : { 'X-CSRF-Token': decodeURIComponent(c?.value ?? '') },
    });
    if (!res.ok()) throw new Error(`${method} ${path} -> ${res.status()} ${await res.text()}`);
    return (res.status() === 204 ? undefined : await res.json()) as T;
  }
}

let admin: Api;
let ssoUserId = '';

test.beforeAll(async () => {
  const ctx = await request.newContext({ baseURL: GUNKLY });
  admin = new Api(ctx);
  const code = process.env.GUNKLY_SETUP_CODE;
  if (!code) throw new Error('GUNKLY_SETUP_CODE is not set');
  await admin.call('POST', '/setup', { code, username: ADMIN.username, password: ADMIN.password, displayName: ADMIN.displayName });
  await admin.call('POST', '/session/step-up', { password: ADMIN.password });
  // "Create accounts for new single sign-on users" on; never ask GitHub for releases.
  await admin.call('PATCH', '/admin/settings', { ssoCreateAccounts: true, updateCheck: false });
});

const me = async (page: Page): Promise<{ id: string; username: string; displayName: string }> => {
  const res = await page.request.get('/api/v1/me');
  expect(res.ok()).toBe(true);
  return res.json();
};

const signOut = async (page: Page): Promise<void> => {
  await page.goto('/');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByRole('link', { name: `Sign in with ${NAME}` })).toBeVisible();
};

test('a new single sign-on user gets an account and lands signed in', async ({ browser }) => {
  const page = await (await browser.newContext()).newPage();
  await page.goto('/signin');
  await throughProvider(page, GUNKLY, () => page.getByRole('link', { name: `Sign in with ${NAME}` }).click(), USERS.sso);

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toHaveCount(0);
  await expect(page.getByRole('navigation', { name: 'Main' }).getByRole('button', { name: 'Sign out' })).toBeVisible();
  const who = await me(page);
  expect(who.username).toBe(USERS.sso.username);
  expect(who.displayName).toBe(USERS.sso.displayName);
  ssoUserId = who.id;
});

test('signing out and in again is the same account, not a second one', async ({ browser }) => {
  const page = await (await browser.newContext()).newPage();
  await page.goto('/signin');
  await throughProvider(page, GUNKLY, () => page.getByRole('link', { name: `Sign in with ${NAME}` }).click(), USERS.sso);
  await page.goto('/');
  await signOut(page);

  await throughProvider(page, GUNKLY, () => page.getByRole('link', { name: `Sign in with ${NAME}` }).click(), USERS.sso);
  await page.goto('/');
  expect((await me(page)).id).toBe(ssoUserId);
  const users = JSON.stringify(await admin.call('GET', '/admin/users'));
  expect(users.match(/"username":"ci-sso-user/g)).toHaveLength(1);
});

test('a password account links single sign-on and then signs in with it', async ({ browser }) => {
  const username = `pw${Date.now().toString(36)}`;
  await admin.call('POST', '/admin/users', { username, password: PASSWORD, displayName: 'Password Person', isAdmin: false });
  const signed = new Api(await request.newContext({ baseURL: GUNKLY }));
  await signed.call('POST', '/session', { login: username, password: PASSWORD });
  const page = await (await browser.newContext({ storageState: await signed.ctx.storageState() })).newPage();

  await page.goto('/account/security');
  const section = page.getByRole('region', { name: 'Single sign-on' });
  await expect(section.getByText('Not linked.')).toBeVisible();
  await section.getByRole('button', { name: 'Link single sign-on' }).click();
  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  await confirm.getByLabel('Password').fill(PASSWORD);
  await throughProvider(page, GUNKLY, () => confirm.getByRole('button', { name: 'Confirm' }).click(), USERS.link);
  await expect(page.getByText('Single sign-on linked.')).toBeVisible();
  await expect(section.getByText(/Linked since/)).toBeVisible();

  await signOut(page);
  await throughProvider(page, GUNKLY, () => page.getByRole('link', { name: `Sign in with ${NAME}` }).click(), USERS.link);
  await page.goto('/');
  const who = await me(page);
  expect(who.username).toBe(username);
  expect(who.displayName).toBe('Password Person');
});

test('an account with only single sign-on confirms a sensitive action through the provider', async ({ browser }) => {
  const page = await (await browser.newContext()).newPage();
  await page.goto('/signin');
  await throughProvider(page, GUNKLY, () => page.getByRole('link', { name: `Sign in with ${NAME}` }).click(), USERS.sso);

  await page.goto('/account/integrations');
  await page.getByRole('region', { name: 'Connect an AI assistant' }).getByRole('button', { name: 'Connect an assistant' }).click();
  await page.getByRole('dialog', { name: 'Connect an AI assistant' }).getByRole('button', { name: 'Create access' }).click();
  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  // The provider still has our session; Gunkly asks it to authenticate anew (prompt=login), so the form must appear.
  const { prompted } = await throughProvider(page, GUNKLY, () => confirm.getByRole('button', { name: `Confirm with ${NAME}` }).click(), USERS.sso);
  expect(prompted, 'the provider must ask for the password again (prompt=login)').toBe(true);
  await expect(page.getByText('Confirmed. You can continue now.')).toBeVisible();
});
