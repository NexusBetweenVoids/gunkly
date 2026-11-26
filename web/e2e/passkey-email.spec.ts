import { axe, expect, headerItem, mailMark, nextMail, PASSWORD, test, uniq } from './helpers';


// An account with email codes and a passkey: signing in offers the passkey first and sends no email
// until asked ("Email me a code instead"); the recovery codes section shows what is left and replaces
// them. Chromium's virtual authenticator approves every passkey prompt.
test('passkey first: no email until asked; recovery codes can be renewed', async ({ world, browserName }) => {
  test.skip(browserName !== 'chromium', 'virtual authenticators need Chromium');
  const username = uniq('pkmail');
  const email = `${username}@example.test`;
  const mark0 = mailMark();
  await world.admin.post('/admin/users', { username, email, password: PASSWORD, displayName: 'Passkey Mail' });
  const verify = await nextMail(email, /Confirm your email address/, mark0);

  const context = await world.newContext();
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  const signIn = async (): Promise<void> => {
    await page.goto('/signin');
    await page.getByLabel('Email or username').fill(username);
    await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  };
  await signIn();
  await expect(page).toHaveURL(/\/$/);
  await page.goto(verify.link);
  await expect(page.getByText('Your email address is confirmed.')).toBeVisible();

  // Email codes on (the first second factor: recovery codes come with it).
  await page.goto('/settings/security');
  const emailCodes = page.getByRole('region', { name: 'Email codes' });
  await emailCodes.getByRole('button', { name: 'Use email codes' }).click();
  const dialog = page.getByRole('dialog', { name: "Confirm it's you" });
  await dialog.getByLabel('Password').fill(PASSWORD);
  await dialog.getByRole('button', { name: 'Confirm' }).click();
  await emailCodes.getByRole('button', { name: "I've saved them" }).click();

  // The recovery codes section counts them.
  const recovery = page.getByRole('region', { name: 'Recovery codes' });
  await expect(recovery.getByText(/^10 unused codes left$/)).toBeVisible();

  // Add a passkey (the confirmation from turning on email codes is still fresh).
  const passkeys = page.getByRole('region', { name: 'Passkeys' });
  await passkeys.getByRole('button', { name: 'Add a passkey' }).click();
  await passkeys.getByLabel('Name').fill('Virtual key');
  await passkeys.getByRole('button', { name: 'Create passkey' }).click();
  // Codes existed already: no new list, a hint that they still work.
  await expect(page.getByText(/Your recovery codes from before still work/)).toBeVisible();
  await expect(passkeys.getByText('Virtual key')).toBeVisible();

  // Sign in again: the passkey is offered first and no email goes out.
  await (await headerItem(page, 'button', 'Sign out')).click();
  const mark2 = mailMark();
  await signIn();
  await expect(page.getByRole('button', { name: 'Use a passkey' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Email me a code instead' })).toBeVisible();
  await expect(page.getByLabel('Code from your email')).toHaveCount(0);
  await axe(page, 'second step: passkey first');
  await page.waitForTimeout(1500);
  expect(mailMark()).toBe(mark2);
  await page.getByRole('button', { name: 'Use a passkey' }).click();
  await expect(page).toHaveURL(/\/$/);

  // Renew the recovery codes (confirmed with the passkey).
  await page.goto('/settings/security');
  await recovery.getByRole('button', { name: 'Create new codes' }).click();
  await page.getByRole('dialog', { name: 'Create new recovery codes?' }).getByRole('button', { name: 'Create new codes' }).click();
  // Signing in with the passkey just now counts as a fresh confirmation, or the dialog asks: use the passkey.
  const stepUp = page.getByRole('dialog', { name: "Confirm it's you" });
  const list = recovery.getByRole('list', { name: 'Recovery codes' });
  await expect(stepUp.or(list)).toBeVisible();
  if (await stepUp.isVisible()) await stepUp.getByRole('button', { name: 'Confirm with a passkey' }).click();
  await expect(list.getByRole('listitem')).toHaveCount(10);
  await axe(page, 'new recovery codes');
  await recovery.getByRole('button', { name: "I've saved them" }).click();
  await expect(recovery.getByText(/^10 unused codes left$/)).toBeVisible();

  // Or ask for the email code instead (another device, so the sign-in rate limit of this one is untouched).
  const page2 = await (await world.newContext()).newPage();
  await page2.goto('/signin');
  await page2.getByLabel('Email or username').fill(username);
  await page2.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page2.getByRole('button', { name: 'Sign in', exact: true }).click();
  const mark3 = mailMark();
  await page2.getByRole('button', { name: 'Email me a code instead' }).click();
  const login = await nextMail(email, /sign-in code/, mark3);
  await expect(page2.getByText(/We emailed a 6-digit code/)).toBeVisible();
  await page2.getByLabel('Code from your email').fill(login.code);
  await page2.getByRole('button', { name: 'Verify and sign in' }).click();
  await expect(page2).toHaveURL(/\/$/);
});
