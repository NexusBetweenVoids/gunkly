import { axe, expect, headerItem, test } from './helpers';

// Passkeys with Chromium's virtual authenticator (CDP WebAuthn domain): a resident,
// user-verifying platform authenticator that approves every prompt.
test('passkeys: add one, sign in without a password, use it as second factor, remove it', async ({ world, browserName }) => {
  test.skip(browserName !== 'chromium', 'virtual authenticators need Chromium');
  const u = await world.newUser();
  const { context, page } = await u.open();
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });

  await page.goto('/account/security');
  const section = page.getByRole('region', { name: 'Passkeys' });
  await expect(section.getByRole('button', { name: 'Add a passkey' })).toBeEnabled();
  await section.getByRole('button', { name: 'Add a passkey' }).click();
  await section.getByLabel('Name').fill('Virtual key');
  await axe(page, 'add passkey form');
  await section.getByRole('button', { name: 'Create passkey' }).click();
  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  await confirm.getByLabel('Password').fill(u.password);
  await confirm.getByRole('button', { name: 'Confirm' }).click();

  // The first second factor of the account brings recovery codes.
  await expect(section.getByRole('list', { name: 'Recovery codes' })).toBeVisible();
  await section.getByRole('button', { name: "I've saved them" }).click();
  await expect(section.getByText('Virtual key')).toBeVisible();
  await expect(section.getByText(/never used/)).toBeVisible();
  await axe(page, 'passkey list');

  // Passwordless: no username, no password.
  await (await headerItem(page, 'button', 'Sign out')).click();
  await page.goto('/signin');
  await axe(page, 'sign-in with passkey button');
  await page.getByRole('button', { name: 'Sign in with a passkey' }).click();
  await expect(page).not.toHaveURL(/signin/);
  await page.goto('/account/security');
  await expect(page.getByRole('region', { name: 'Passkeys' }).getByText(/last used/)).toBeVisible();

  // As the second factor after a password.
  await (await headerItem(page, 'button', 'Sign out')).click();
  await page.goto('/signin');
  await page.getByLabel('Email or username').fill(u.username);
  await page.getByLabel('Password').fill(u.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('button', { name: 'Use a passkey' }).click();
  await expect(page).not.toHaveURL(/signin/);

  // Removing needs a step-up, which a passkey can give on its own.
  await page.goto('/account/security');
  const list = page.getByRole('region', { name: 'Passkeys' });
  await list.getByRole('button', { name: 'Delete' }).click();
  await page.getByRole('dialog', { name: 'Remove this passkey?' }).getByRole('button', { name: 'Delete' }).click();
  await page.getByRole('dialog', { name: "Confirm it's you" }).getByRole('button', { name: 'Confirm with a passkey' }).click();
  await expect(list.getByText('Virtual key')).toHaveCount(0);
});
