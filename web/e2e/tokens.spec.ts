import { request } from '@playwright/test';
import { axe, expect, nextIP, test } from './helpers';

test('integrations: create an access token in the UI, use it as a bearer, revoke it', async ({ world, baseURL }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const chore = await world.chore(u, h);
  const { page } = await u.open();
  await page.goto('/account/integrations');

  const section = page.getByRole('region', { name: 'Integrations' });
  await expect(section.getByText(/Home Assistant/)).toBeVisible();
  await section.getByRole('button', { name: 'Create access token' }).click();

  const dialog = page.getByRole('dialog', { name: 'Create access token' });
  await dialog.getByLabel('Name').fill('Home Assistant');
  await expect(dialog.getByLabel(h.name)).toBeChecked(); // the only household is preselected
  await expect(dialog.getByLabel('See households, chores and history')).toBeChecked();
  await expect(dialog.getByLabel('Mark chores done, snooze, skip and pause them')).toBeChecked();
  await expect(dialog.getByLabel('Add, change and delete chores and their reminders')).not.toBeChecked();
  await axe(page, 'create token dialog');
  await dialog.getByRole('button', { name: 'Create access token' }).click();

  // Creating needs a fresh password check.
  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  await confirm.getByLabel('Password').fill(u.password);
  await confirm.getByRole('button', { name: 'Confirm' }).click();

  const shown = page.getByRole('dialog', { name: 'Your new access token' });
  await expect(shown.getByText(/won't be shown again/)).toBeVisible();
  const secret = (await shown.getByText(/^gk_/).textContent())!.trim();
  expect(secret).toMatch(/^gk_[\w-]{43}$/);
  await axe(page, 'token shown dialog');
  await shown.getByRole('button', { name: 'Done' }).click();

  const row = section.getByRole('listitem').filter({ hasText: 'Home Assistant' });
  await expect(row).toContainText(h.name);
  await expect(row).toContainText('never used');
  await expect(row).not.toContainText(secret);

  // The token works as a bearer credential, without cookies or CSRF.
  const api = await request.newContext({ baseURL, extraHTTPHeaders: { 'X-Forwarded-For': nextIP() } });
  const auth = { Authorization: `Bearer ${secret}` };
  const me = await api.get('/api/v1/token', { headers: auth });
  expect(me.status()).toBe(200);
  expect(await me.json()).toMatchObject({
    name: 'Home Assistant',
    scopes: ['chores:read', 'chores:complete'],
    households: [{ id: h.id, name: h.name, role: 'owner' }],
    user: { displayName: u.displayName },
    expiresAt: expect.any(String),
  });
  const done = await api.post(`/api/v1/households/${h.id}/chores/${chore.id}/complete`, { headers: auth, data: {} });
  expect(done.status()).toBe(201);
  expect((await done.json()).completion.source).toBe('api_token');
  expect((await api.post(`/api/v1/households/${h.id}/chores`, { headers: auth, data: {} })).status()).toBe(403); // no chores:write
  expect((await api.get('/api/v1/account/tokens', { headers: auth })).status()).toBe(403); // tokens never manage tokens

  // Revoke with confirmation; the token stops working at once.
  await page.reload();
  await row.getByRole('button', { name: 'Revoke' }).click();
  await page.getByRole('dialog', { name: 'Revoke Home Assistant?' }).getByRole('button', { name: 'Revoke' }).click();
  await expect(page.getByText('Token revoked.')).toBeVisible();
  await expect(section.getByText('No access tokens yet.')).toBeVisible();
  const after = await api.get('/api/v1/token', { headers: auth });
  expect(after.status()).toBe(401);
  expect(after.headers()['www-authenticate']).toBe('Bearer');
  await api.dispose();
});

test('integrations: guided Claude Code setup creates a working token, then revoke', async ({ world, baseURL }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const { page } = await u.open();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/account/integrations');

  const card = page.getByRole('region', { name: 'Connect an AI assistant' });
  await expect(card.getByText(/can only use the households and actions you allow/)).toBeVisible();
  await card.getByRole('button', { name: 'Connect an assistant' }).click();

  const dialog = page.getByRole('dialog', { name: 'Connect an AI assistant' });
  await expect(dialog.getByLabel('Claude Code')).toBeChecked();
  await expect(dialog.getByLabel(h.name)).toBeChecked();
  await expect(dialog.getByLabel('Mark chores as done, snooze or skip')).toBeChecked();
  await expect(dialog.getByLabel('Create and edit chores, manage areas')).not.toBeChecked();
  await axe(page, 'connect assistant dialog');
  await dialog.getByRole('button', { name: 'Create access' }).click();

  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  await confirm.getByLabel('Password').fill(u.password);
  await confirm.getByRole('button', { name: 'Confirm' }).click();

  const ready = page.getByRole('dialog', { name: 'Your assistant is ready to connect' });
  const cmd = (await ready.locator('pre.code').textContent())!;
  expect(cmd).toContain(`--transport http gunkly ${new URL(baseURL!).origin}/mcp`);
  const secret = cmd.match(/Bearer (gk_[\w-]{43})/)![1]!;
  await axe(page, 'assistant ready');
  await page.screenshot({ path: '/tmp/claude-1000/brand/mcp-guide.png' });
  await ready.getByRole('button', { name: 'Copy command' }).click();
  await ready.getByRole('button', { name: 'Done' }).click();

  const api = await request.newContext({ baseURL, extraHTTPHeaders: { 'X-Forwarded-For': nextIP() } });
  const auth = { Authorization: `Bearer ${secret}` };
  expect((await api.get('/api/v1/token', { headers: auth })).status()).toBe(200);

  await page.reload();
  const row = page.getByRole('region', { name: 'Integrations' }).getByRole('listitem').filter({ hasText: 'Claude Code (MCP)' });
  await row.getByRole('button', { name: 'Revoke' }).click();
  await page.getByRole('dialog', { name: 'Revoke Claude Code (MCP)?' }).getByRole('button', { name: 'Revoke' }).click();
  await expect(page.getByText('Token revoked.')).toBeVisible();
  expect((await api.get('/api/v1/token', { headers: auth })).status()).toBe(401);
  await api.dispose();
});
