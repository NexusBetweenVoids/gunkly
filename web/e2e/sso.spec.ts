import { readFileSync } from 'node:fs';
import { request } from '@playwright/test';
import { axe, expect, headerItem, test, uniq } from './helpers';
import { idpFile } from './global-setup';

// Single sign-on against the fake OpenID Connect provider started by global-setup (idp.ts).
const idpUrl = (): string => readFileSync(idpFile, 'utf8');

/** The next person to "sign in" at the provider. */
async function asProviderUser(claims: Record<string, unknown>): Promise<void> {
  const ctx = await request.newContext();
  const res = await ctx.post(`${idpUrl()}/_next`, { data: claims });
  expect(res.ok()).toBe(true);
  await ctx.dispose();
}

test('single sign-on: link an account, sign in with it, unlink', async ({ world }) => {
  const u = await world.newUser();
  const sub = uniq('sub-');
  const { page } = await u.open();

  // Linking needs a fresh password check, then the provider takes over and brings us back.
  await asProviderUser({ sub, preferred_username: u.username });
  await page.goto('/settings/security');
  const section = page.getByRole('region', { name: 'Single sign-on' });
  await expect(section.getByText('Not linked.')).toBeVisible();
  await axe(page, 'single sign-on section');
  await section.getByRole('button', { name: 'Link single sign-on' }).click();
  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  await confirm.getByLabel('Password').fill(u.password);
  await confirm.getByRole('button', { name: 'Confirm' }).click();
  await expect(page.getByText('Single sign-on linked.')).toBeVisible();
  await expect(page).toHaveURL(/\/settings\/security$/);
  await expect(section.getByText(/Linked since/)).toBeVisible();

  // Signed out, the button on the sign-in page signs in as the linked account, no password asked.
  await (await headerItem(page, 'button', 'Sign out')).click();
  await page.goto('/signin');
  await axe(page, 'sign-in with single sign-on button');
  await asProviderUser({ sub, preferred_username: 'whatever' });
  await page.getByRole('link', { name: 'Sign in with Test IdP' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toHaveCount(0);
  await page.goto('/settings/profile');
  await expect(page.getByLabel('Display name')).toHaveValue(u.displayName);

  // Unlinking (step-up again) goes back to "not linked"; the same sign-in is now a stranger.
  await page.goto('/settings/security');
  await section.getByRole('button', { name: 'Unlink' }).click();
  await page.getByRole('dialog', { name: 'Unlink single sign-on?' }).getByRole('button', { name: 'Unlink' }).click();
  const again = page.getByRole('dialog', { name: "Confirm it's you" });
  await again.getByRole('button', { name: 'Confirm with Test IdP' }).click(); // re-verifies through the provider
  await expect(page.getByText('Confirmed. You can continue now.')).toBeVisible();
  await section.getByRole('button', { name: 'Unlink' }).click();
  await page.getByRole('dialog', { name: 'Unlink single sign-on?' }).getByRole('button', { name: 'Unlink' }).click();
  await expect(section.getByText('Not linked.')).toBeVisible();
});

test('single sign-on: an unknown user gets a friendly message, or an account when the admin allows it', async ({ world }) => {
  const sub = uniq('stranger-');
  const username = uniq('sso');
  const ctx = await world.newContext();
  const page = await ctx.newPage();

  await asProviderUser({ sub, preferred_username: username, name: 'Sunny Sso' });
  await page.goto('/signin');
  await page.getByRole('link', { name: 'Sign in with Test IdP' }).click();
  await expect(page).toHaveURL(/\/signin\?sso=no_account/);
  await expect(page.getByRole('alert')).toContainText('No Gunkly account is linked');
  await axe(page, 'no account linked');

  // The administrator allows new single sign-on users to get an account.
  await world.admin.call('PATCH', '/admin/settings', { ssoCreateAccounts: true });
  try {
    await asProviderUser({ sub, preferred_username: username, name: 'Sunny Sso' });
    await page.goto('/signin');
    await page.getByRole('link', { name: 'Sign in with Test IdP' }).click();
    await expect(page.getByRole('heading', { name: 'Sign in' })).toHaveCount(0);
    await page.goto('/settings/profile');
    await expect(page.getByLabel('Display name')).toHaveValue('Sunny Sso');
    // There is no password: the security page says how to add one, and the account cannot unlink yet.
    await page.goto('/settings/security');
    const section = page.getByRole('region', { name: 'Single sign-on' });
    await expect(section.getByText(/no password/)).toBeVisible();
    await expect(section.getByRole('button', { name: 'Unlink' })).toBeDisabled();
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { ssoCreateAccounts: false });
  }
});
