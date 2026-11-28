import { axe, expect, test, totp } from './helpers';

test('an admin with two-factor authentication deletes an account; without it they are sent to set it up', async ({ world }) => {
  const target = await world.newUser();
  const h = await world.household(target);

  // Without a second factor: the dialog explains and offers no delete button.
  const plain = await world.newUser({ admin: true });
  const { page: p1 } = await plain.open();
  await p1.goto('/admin/users');
  await p1.getByRole('listitem').filter({ hasText: target.displayName }).getByRole('button', { name: 'Delete' }).click();
  const d1 = p1.getByRole('dialog', { name: `Delete ${target.displayName}'s account?` });
  await expect(d1.getByText('Deleting accounts needs a passkey or two-factor authentication on your own account.')).toBeVisible();
  await expect(d1.getByRole('button', { name: 'Delete account permanently' })).toHaveCount(0);
  await expect(d1.getByRole('link', { name: 'Set it up' })).toHaveAttribute('href', /\/settings\/security$/);

  // With an authenticator app. The session confirmed itself to set it up, so no step-up prompt follows here
  // (the API tests cover that the step-up is required).
  const admin = await world.newUser({ admin: true });
  await admin.api.call('POST', '/session/step-up', { password: admin.password }, 204);
  const { secret } = await admin.api.post<{ secret: string }>('/account/totp/setup');
  await admin.api.post('/account/totp/confirm', { code: totp(secret) });
  const { page } = await admin.open();
  await page.goto('/admin/users');
  const row = page.getByRole('listitem').filter({ hasText: target.displayName });
  // Not offered for the admin's own row.
  await expect(page.getByRole('listitem').filter({ hasText: admin.displayName }).getByRole('button', { name: 'Delete' })).toHaveCount(0);
  await row.getByRole('button', { name: 'Delete' }).click();
  const dialog = page.getByRole('dialog', { name: `Delete ${target.displayName}'s account?` });
  await expect(dialog.getByRole('listitem').filter({ hasText: h.name })).toBeVisible(); // deleted with the account
  await axe(page, 'admin delete account dialog');
  await dialog.getByLabel(`Type their username (${target.username}) to confirm`).fill('someone-else');
  await dialog.getByRole('button', { name: 'Delete account permanently' }).click();
  await expect(dialog.getByText("That doesn't match their username.")).toBeVisible();

  await dialog.getByLabel(`Type their username (${target.username}) to confirm`).fill(target.username);
  await dialog.getByRole('button', { name: 'Delete account permanently' }).click();
  await expect(page.getByText('Account deleted.')).toBeVisible();
  await expect(row).toHaveCount(0);
  await target.api.call('GET', '/me', undefined, 401);
});
