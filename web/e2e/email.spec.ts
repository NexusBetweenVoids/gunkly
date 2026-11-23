import { axe, expect, headerItem, mailMark, nextMail, PASSWORD, setTheme, test, uniq } from './helpers';

// These run against a server whose SMTP goes to the fake server started in global setup
// (e2e/smtp.ts); nothing leaves the machine.

test('open sign-up solves the proof of work in a worker, signs in at once, and the emailed link confirms the address', async ({ world }) => {
  await world.admin.call('PATCH', '/admin/settings', { openSignup: true, requirePow: true });
  try {
    const page = await (await world.newContext()).newPage();
    await page.goto('/signin');
    await page.getByRole('link', { name: 'Create an account' }).click();
    await expect(page).toHaveURL(/\/signup$/);
    await expect(page.getByRole('heading', { name: 'Create an account' })).toBeVisible();

    const name = uniq('new');
    const email = `${name}@example.test`;
    const mark = mailMark();
    await page.getByLabel('Display name').fill('New Person');
    await page.getByLabel('Username', { exact: true }).fill(name);
    await expect(page.getByText('Only needed to reset your password by email.')).toBeVisible();
    await page.getByLabel('Email (optional)').fill(email);
    await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
    await page.getByRole('button', { name: 'Create account' }).click();

    // Self-hosted: signed in right away, no banner, and households work without confirming.
    await expect(page.getByRole('heading', { name: 'Chores' })).toBeVisible();
    await expect(page.getByText(/Confirm your email address: we sent a link/)).toHaveCount(0);
    await page.goto('/account/profile');
    await expect(page.getByText('Not confirmed')).toBeVisible();
    await expect(page.getByText(/Confirm it with the link we emailed/)).toBeVisible();

    const m = await nextMail(email, /Confirm your email address/, mark);
    expect(m.link).toMatch(/^\/verify-email\?token=/);
    await page.goto(m.link);
    await expect(page.getByText('Your email address is confirmed.')).toBeVisible();
    await expect(page).toHaveURL(/\/verify-email$/); // the token is gone from the address bar

    // The link works once.
    await page.goto(m.link);
    await expect(page.getByText(/This link is not valid any more/)).toBeVisible();

    await page.goto('/account/profile');
    await expect(page.getByText('Confirmed', { exact: true })).toBeVisible();
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { openSignup: false, requirePow: false });
  }
});

test('forgot password: the emailed link sets a new password, once', async ({ world }) => {
  const username = uniq('reset');
  const email = `${username}@example.test`;
  await world.admin.post('/admin/users', { username, email, password: PASSWORD, displayName: 'Reset Person' });
  const page = await (await world.newContext()).newPage();
  await page.goto('/signin');
  await page.getByRole('link', { name: 'Forgot password?' }).click();
  await expect(page.getByRole('heading', { name: 'Reset your password' })).toBeVisible();

  // An unknown account gets the very same answer and no email.
  await page.getByLabel('Email or username').fill(uniq('ghost'));
  await page.getByRole('button', { name: 'Send reset link' }).click();
  await expect(page.getByText(/If an account matches, a reset link is on its way/)).toBeVisible();

  await page.goto('/forgot-password');
  const mark = mailMark();
  await page.getByLabel('Email or username').fill(email);
  await page.getByRole('button', { name: 'Send reset link' }).click();
  await expect(page.getByText(/If an account matches, a reset link is on its way/)).toBeVisible();
  const m = await nextMail(email, /Reset your Gunkly password/, mark);

  const NEW = 'a brand new long password';
  await page.goto(m.link);
  await expect(page).toHaveURL(/\/reset-password$/);
  await page.getByLabel('New password').fill('short');
  await page.getByRole('button', { name: 'Set new password' }).click();
  await expect(page.getByText('That password is too short.')).toBeVisible();
  await page.getByLabel('New password').fill(NEW);
  await page.getByRole('button', { name: 'Set new password' }).click();
  await expect(page).toHaveURL(/\/signin$/);
  await expect(page.getByText('Password changed. Sign in with your new password.')).toBeVisible();

  await page.getByLabel('Email or username').fill(username);
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByText('Incorrect username or password.')).toBeVisible();
  await page.getByLabel('Password', { exact: true }).fill(NEW);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/\/$/);

  // Single use.
  const again = await (await world.newContext()).newPage();
  await again.goto(m.link);
  await again.getByLabel('New password').fill('another long password');
  await again.getByRole('button', { name: 'Set new password' }).click();
  await expect(again.getByText(/This reset link is not valid any more/)).toBeVisible();
});

test('email codes: turn them on in the account screen, then sign in with an emailed code', async ({ world }) => {
  const username = uniq('otp');
  const email = `${username}@example.test`;
  const mark0 = mailMark();
  await world.admin.post('/admin/users', { username, email, password: PASSWORD, displayName: 'Otp Person' });
  const verify = await nextMail(email, /Confirm your email address/, mark0);

  const page = await (await world.newContext()).newPage();
  await page.goto('/signin');
  await page.getByLabel('Email or username').fill(username);
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText(/Confirm your email address: we sent a link/)).toHaveCount(0); // self-hosted: no banner

  const section = () => page.getByRole('region', { name: 'Email codes' });
  await page.goto('/account/security');
  await expect(section().getByRole('button', { name: 'Use email codes' })).toBeDisabled(); // not confirmed yet

  await page.goto(verify.link);
  await expect(page.getByText('Your email address is confirmed.')).toBeVisible();
  await page.goto('/account/security');
  await section().getByRole('button', { name: 'Use email codes' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Password').fill(PASSWORD);
  await dialog.getByRole('button', { name: 'Confirm' }).click();
  await expect(section().getByRole('list', { name: 'Recovery codes' }).getByRole('listitem')).not.toHaveCount(0);
  await section().getByRole('button', { name: "I've saved them" }).click();
  await expect(section().getByText('On', { exact: true })).toBeVisible();

  // A new device now needs the emailed code.
  const page2 = await (await world.newContext()).newPage();
  await page2.goto('/signin');
  await page2.getByLabel('Email or username').fill(username);
  await page2.getByLabel('Password', { exact: true }).fill(PASSWORD);
  const mark = mailMark();
  await page2.getByRole('button', { name: 'Sign in' }).click();
  await expect(page2.getByText(/We emailed a 6-digit code/)).toBeVisible();
  const first = await nextMail(email, /sign-in code/, mark);
  expect(first.code).toMatch(/^\d{6}$/);

  await page2.getByLabel('Code from your email').fill(first.code === '000000' ? '000001' : '000000');
  await page2.getByRole('button', { name: 'Verify and sign in' }).click();
  await expect(page2.getByText("That code isn't valid. Check it and try again.")).toBeVisible();

  // "Email me a new code" replaces the first one.
  const mark2 = mailMark();
  await page2.getByRole('button', { name: 'Email me a new code' }).click();
  const second = await nextMail(email, /sign-in code/, mark2);
  await page2.getByLabel('Code from your email').fill(second.code);
  await page2.getByRole('button', { name: 'Verify and sign in' }).click();
  await expect(page2).toHaveURL(/\/$/);
  await expect(await headerItem(page2, 'link', 'Account')).toBeVisible();
});

test('the sign-up, reset and verification screens pass axe in light and dark', async ({ world }) => {
  await world.admin.call('PATCH', '/admin/settings', { openSignup: true, requirePow: false });
  try {
    const page = await (await world.newContext()).newPage();
    for (const path of ['/signin', '/signup', '/forgot-password', '/reset-password?token=x', '/verify-email?token=x']) {
      for (const theme of ['light', 'dark'] as const) {
        await page.goto(path);
        await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
        await setTheme(page, theme);
        await axe(page, `${path} (${theme})`);
      }
    }
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { openSignup: false, requirePow: false });
  }
});
