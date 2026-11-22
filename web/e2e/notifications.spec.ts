import { expect, test } from './helpers';

test('account: enabling browser notifications is offered where the browser supports push', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.goto('/account');
  const card = page.getByRole('region', { name: 'Notifications on this device' });
  await expect(card.getByRole('button', { name: 'Enable notifications on this device' })).toBeVisible();
  await expect(card.getByText(/browser vendor's push service/)).toBeVisible();
});

test('account: browsers without push get an explanation instead of a button', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.addInitScript(() => {
    // @ts-expect-error simulate a browser without the Push API
    delete window.PushManager;
  });
  await page.goto('/account');
  const card = page.getByRole('region', { name: 'Notifications on this device' });
  await expect(card.getByTestId('push-unsupported')).toHaveText('This browser does not support push notifications.');
  await expect(card.getByRole('button')).toHaveCount(0);
});

test('admin: the Updates section shows the running version and the check can be switched off', async ({ world }) => {
  const admin = await world.newUser({ admin: true });
  await admin.api.call('POST', '/session/step-up', { password: admin.password }, 204);
  const { page } = await admin.open();
  await page.goto('/admin');
  const card = page.getByRole('region', { name: 'Updates' });
  await expect(card.getByText(/^Running version: /)).toBeVisible();
  const box = card.getByRole('checkbox', { name: 'Check for updates' });
  try {
    await box.check();
    await expect.poll(async () => (await admin.api.get<{ enabled: boolean }>('/admin/updates')).enabled).toBe(true);
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { updateCheck: false }); // the e2e server must never call out
  }
  await page.reload();
  await expect(card.getByRole('checkbox', { name: 'Check for updates' })).not.toBeChecked();
});
