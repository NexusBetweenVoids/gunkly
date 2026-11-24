import type { Page, Request } from '@playwright/test';
import { expect, PASSWORD, test, uniq, type World } from './helpers';

/** Answers the "Confirm it's you" dialog if (and only if) the app asks for it. */
async function maybeStepUp(page: Page, password: string, done: ReturnType<Page['locator']>): Promise<void> {
  const dialog = page.getByRole('dialog', { name: "Confirm it's you" });
  await dialog.or(done).first().waitFor();
  if (await dialog.isVisible()) {
    await dialog.getByLabel('Password').fill(password);
    await dialog.getByRole('button', { name: 'Confirm' }).click();
  }
  await done.waitFor();
}

test('household settings: areas, calendar feed, notification channel from the service picker', async ({ world }) => {
  const u = await world.newUser();
  const h = await world.household(u, undefined, { areas: true });
  const { page } = await u.open();
  await page.goto(`/households/${h.id}/general`);

  // Areas: add, rename, delete.
  const areas = page.getByRole('region', { name: 'Areas' });
  const name = uniq('Cellar');
  await areas.getByLabel('New area').fill(name);
  await areas.getByRole('button', { name: 'Add area' }).click();
  await expect(areas.getByText(name, { exact: true })).toBeVisible();
  await areas.getByRole('button', { name: `Rename ${name}` }).click();
  await page.getByRole('dialog').getByLabel('Area name').fill(`${name} 2`);
  await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click();
  await expect(areas.getByText(`${name} 2`, { exact: true })).toBeVisible();
  await areas.getByRole('button', { name: `Delete ${name} 2` }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click();
  await expect(areas.getByText(`${name} 2`, { exact: true })).toHaveCount(0);

  // Feed: create (address shown once), regenerate, revoke.
  await page.goto(`/households/${h.id}/connections`);
  const feeds = page.getByRole('region', { name: 'Calendar feeds' });
  await feeds.getByRole('button', { name: 'Create feed' }).click();
  await expect(feeds.getByText(/\/ical\/.+\.ics$/)).toBeVisible();
  await feeds.getByRole('button', { name: 'New address' }).click();
  await expect(feeds.getByText(/\/ical\/.+\.ics$/)).toBeVisible();
  await feeds.getByRole('button', { name: 'Revoke' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Revoke' }).click();
  await expect(page.getByText('Feed revoked.')).toBeVisible();
  await expect(feeds.getByText('No feeds yet.')).toBeVisible();

  // Channels: the service picker lists the enabled kinds (what the API says), and sets the URL scheme.
  const enabled = (await u.api.get<{ enabledNotificationKinds: string[] }>('/notification-kinds')).enabledNotificationKinds;
  expect(enabled).toContain('discord');
  expect(enabled).not.toContain('gotify');
  await page.goto(`/households/${h.id}/notifications`);
  const channels = page.getByRole('region', { name: 'Household notification channels' });
  await channels.getByRole('button', { name: 'Add channel' }).click();
  const dlg = page.getByRole('dialog');
  const picker = dlg.getByLabel('Service', { exact: true });
  await expect(picker.locator('option')).toHaveCount(enabled.length + 1);
  await picker.selectOption('discord');
  await expect(dlg.getByLabel('Service URL')).toHaveValue('discord://');
  await dlg.getByLabel('Service URL').fill('discord://tokensecret1234567890@1234567890');
  await dlg.getByLabel('Label').fill('Family chat');
  await dlg.getByRole('button', { name: 'Save' }).click();
  await expect(channels.getByText('Family chat')).toBeVisible();
  await expect(page.getByText('tokensecret')).toHaveCount(0); // write-only URL
  // Free URL entry still works, and a disabled service is refused with a clear message.
  await channels.getByRole('button', { name: 'Add channel' }).click();
  await dlg.getByLabel('Service URL').fill('gotify://gotify.example.com/AbCdEfGhIjKl');
  await dlg.getByRole('button', { name: 'Save' }).click();
  await expect(dlg.getByText("That notification service isn't enabled on this server.")).toBeVisible();
});

test('account: change password (with step-up) and sign in with the new one', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.goto('/settings/security');
  const next = `${PASSWORD} again`;
  const section = page.getByRole('region', { name: 'Password' });
  await section.getByLabel('New password', { exact: true }).fill(next);
  await section.getByLabel('Confirm new password').fill(next);
  await section.getByRole('button', { name: 'Change password' }).click();
  await maybeStepUp(page, u.password, page.getByText('Password changed.'));
  await expect(page.getByText('Password changed.')).toBeVisible();
  await world.signIn(u.username, next); // would throw on 401
});

test('admin: create a user and manage instance notification services', async ({ world }) => {
  const defaults = () => world.admin.call('PATCH', '/admin/settings', { enabledNotificationKinds: null });
  await defaults(); // shared server: start from, and leave behind, the defaults
  try {
    await adminFlow(world);
  } finally {
    await defaults();
  }
});

async function adminFlow(world: World): Promise<void> {
  const admin = await world.newUser({ admin: true });
  const { page } = await admin.open();
  await page.goto('/admin/users');

  const users = page.getByRole('region', { name: 'Users' });
  const username = uniq('made');
  await users.getByLabel('Display name').fill('Made In Ui');
  await users.getByLabel('Username', { exact: true }).fill(username);
  await users.getByLabel('Initial password').fill(PASSWORD);
  await users.getByRole('button', { name: 'Create user' }).click();
  await maybeStepUp(page, admin.password, users.getByText(username));
  await world.signIn(username, PASSWORD);

  // Settings only send the notification kinds when the admin changed them.
  await page.goto('/admin/connections');
  const settings = page.getByRole('region', { name: 'Connections' });
  const patches: Record<string, unknown>[] = [];
  page.on('request', (r: Request) => {
    if (r.method() === 'PATCH' && r.url().endsWith('/admin/settings')) patches.push(r.postDataJSON());
  });
  const save = async (btn: string) => {
    const n = patches.length;
    await settings.getByRole('button', { name: btn, exact: true }).click();
    await maybeStepUp(page, admin.password, page.getByText('Saved.').first());
    await expect.poll(() => patches.length).toBeGreaterThan(n);
    await expect(page.getByText('Saved.').first()).toBeVisible();
    return patches[patches.length - 1];
  };
  const kinds = (await admin.api.get<{ enabledNotificationKinds: string[] }>('/notification-kinds')).enabledNotificationKinds;

  expect(await save('Save')).not.toHaveProperty('enabledNotificationKinds');

  const gotify = settings.getByRole('checkbox', { name: 'gotify', exact: true });
  await gotify.check();
  const changed = await save('Save');
  expect(changed.enabledNotificationKinds).toContain('gotify');
  expect((await admin.api.get<{ enabledNotificationKinds: string[] }>('/notification-kinds')).enabledNotificationKinds).toContain('gotify');

  const reset = await save('Reset to defaults');
  expect(reset).toEqual({ enabledNotificationKinds: null });
  await expect(gotify).not.toBeChecked();
  expect((await admin.api.get<{ enabledNotificationKinds: string[] }>('/notification-kinds')).enabledNotificationKinds).toEqual(kinds);
}
