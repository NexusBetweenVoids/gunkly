import { axe, expect, headerItem, setTheme, PASSWORD, test, totp, uniq } from './helpers';

test('invite link: owner creates it, a second user registers in a new context, the guest has no Done button', async ({ world }) => {
  const owner = await world.newUser();
  const h = await world.household(owner);
  const c = await world.chore(owner, h, { name: uniq('Shared ') });
  const { page } = await owner.open();
  await page.goto(`/households/${h.id}/people`);

  const invites = page.getByRole('region', { name: 'Invites' });
  await invites.getByLabel('Role').selectOption('guest');
  await invites.getByRole('button', { name: 'Create invite link' }).click();
  const link = (await page.getByText(/\/invite\//).textContent())!.trim();
  expect(link).toMatch(/\/invite\/[\w-]+$/);
  await expect(invites.getByText('No active invites.')).toHaveCount(0);

  const ctx = await world.newContext();
  const guest = await ctx.newPage();
  await guest.goto(link);
  await expect(guest.getByRole('heading', { name: "You're invited" })).toBeVisible();
  await guest.getByLabel('Display name').fill('Guest Person');
  await guest.getByLabel('Username', { exact: true }).fill(uniq('guest'));
  await guest.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await guest.getByRole('button', { name: 'Create account and join' }).click();

  await expect(guest).toHaveURL(/\/$/);
  await expect(guest.getByRole('link', { name: new RegExp(c.name) })).toBeVisible();
  await expect(guest.getByRole('button', { name: /as done$/ })).toHaveCount(0);
  await expect(guest.getByRole('link', { name: 'Add', exact: true })).toHaveCount(0);

  // Revoking the link stops it working for the next person.
  await invites.getByRole('button', { name: 'Revoke' }).click();
  await expect(page.getByText('Invite revoked.')).toBeVisible();
  const late = await (await world.newContext()).newPage();
  await late.goto(link);
  await late.getByLabel('Display name').fill('Late');
  await late.getByLabel('Username', { exact: true }).fill(uniq('late'));
  await late.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await late.getByRole('button', { name: 'Create account and join' }).click();
  await expect(late.getByText('This invite link is invalid, expired or already used.')).toBeVisible();
});

test('TOTP: enrol in the account screen, then sign in with a code', async ({ world }) => {
  const u = await world.newUser();
  await world.household(u);
  const ctx = await world.newContext();
  const page = await ctx.newPage();

  // Plain password sign-in through the UI.
  await page.goto('/signin');
  await page.getByLabel('Email or username').fill(u.username);
  await page.getByLabel('Password', { exact: true }).fill('wrong password');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByText('Incorrect username or password.')).toBeVisible();
  await page.getByLabel('Password', { exact: true }).fill(u.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page).toHaveURL(/\/$/);

  // Enrol; the step-up dialog asks for the password first.
  await page.goto('/account/security');
  const section = page.getByRole('region', { name: 'Two-factor authentication' });
  await section.getByRole('button', { name: 'Set up' }).click();
  const dialog = page.getByRole('dialog');
  const secretLine = section.getByLabel('Secret', { exact: true });
  await dialog.or(secretLine).waitFor();
  if (await dialog.isVisible()) {
    await dialog.getByLabel('Password').fill(u.password);
    await dialog.getByRole('button', { name: 'Confirm' }).click();
  }
  await expect(section.getByRole('img', { name: 'QR code for your authenticator app' })).toBeVisible();
  await expect(section.getByText('Scan with your authenticator app')).toBeVisible();
  for (const theme of ['light', 'dark'] as const) {
    await setTheme(page, theme);
    await axe(page, `two-factor setup (${theme})`);
  }
  await setTheme(page, 'light');
  const secret = (await secretLine.textContent())!.replace(/\s/g, ''); // shown in groups of four
  await section.getByLabel('Code from your app').fill(totp(secret));
  await section.getByRole('button', { name: 'Turn on' }).click();
  await expect(section.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem')).not.toHaveCount(0);
  await section.getByRole('button', { name: "I've saved them" }).click();
  await expect(section.getByText('On', { exact: true })).toBeVisible();

  // A fresh device now needs the second factor.
  const page2 = await (await world.newContext()).newPage();
  await page2.goto('/signin');
  await page2.getByLabel('Email or username').fill(u.username);
  await page2.getByLabel('Password', { exact: true }).fill(u.password);
  await page2.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page2.getByText('Enter the code from your authenticator app')).toBeVisible();
  await page2.getByLabel('Authenticator code').fill('000000');
  await page2.getByRole('button', { name: 'Verify and sign in' }).click();
  await expect(page2.getByText("That code isn't valid. Check it and try again.")).toBeVisible();
  await page2.getByLabel('Authenticator code').fill(totp(secret, 1)); // the current step was used when enrolling
  await page2.getByRole('button', { name: 'Verify and sign in' }).click();
  await expect(page2).toHaveURL(/\/$/);
  await expect(await headerItem(page2, 'link', 'Account')).toBeVisible();
});

test('theme override: light, dark and following the system', async ({ world }) => {
  const u = await world.newUser();
  await world.household(u);
  const { page } = await u.open();
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('/account/profile');
  const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  const set = async (v: string) => {
    await page.getByLabel('Appearance').selectOption(v);
    await page.getByRole('region', { name: 'Profile' }).getByRole('button', { name: 'Save' }).click();
    await expect(page.getByText('Saved.').last()).toBeVisible(); // earlier toasts may still be fading out
  };

  expect(await bg()).toBe('rgb(245, 238, 227)');
  await set('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  expect(await bg()).toBe('rgb(38, 46, 52)');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark'); // persisted on this device
  expect(await bg()).toBe('rgb(38, 46, 52)');

  await set('light');
  await page.emulateMedia({ colorScheme: 'dark' }); // explicit light beats a dark system
  expect(await bg()).toBe('rgb(245, 238, 227)');
  await set('system');
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /.+/);
  expect(await bg()).toBe('rgb(38, 46, 52)'); // now follows the (dark) system
});

test('signed-out visitors are sent to sign in and back; sign out ends the session', async ({ world }) => {
  const u = await world.newUser();
  await world.household(u);
  const page = await (await world.newContext()).newPage();
  await page.goto('/account');
  await expect(page).toHaveURL(/\/signin\?next=%2Faccount/);
  await page.getByLabel('Email or username').fill(u.username);
  await page.getByLabel('Password', { exact: true }).fill(u.password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page).toHaveURL(/\/account$/);
  await (await headerItem(page, 'button', 'Sign out')).click();
  await expect(page).toHaveURL(/\/signin/);
  await page.goto('/account');
  await expect(page).toHaveURL(/\/signin/);
});
