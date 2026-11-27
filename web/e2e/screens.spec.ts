import type { Page } from '@playwright/test';
import { axe, expect, setTheme, test, uniq } from './helpers';

// Every main screen, in both themes: WCAG 2.2 AA (axe) and no horizontal overflow.
const noOverflow = async (page: Page, what: string) => {
  const w = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  expect.soft(w.sw, `horizontal overflow on ${what}`).toBeLessThanOrEqual(w.cw);
};

test('axe WCAG 2.2 AA and no overflow on every main screen, light and dark', async ({ world }) => {
  test.setTimeout(120_000);
  const u = await world.newUser();
  const h = await world.household(u, undefined, { areas: true });
  const c = await world.chore(u, h, { name: uniq('A11y '), notes: 'Some notes', link: 'https://example.com/how-to' });
  await u.api.post(`/households/${h.id}/chores/${c.id}/complete`);
  await u.api.post(`/households/${h.id}/invites`, { role: 'member', expiresInHours: 24 });
  await u.api.post(`/households/${h.id}/feeds`, {});

  const check = async (page: Page, what: string, ready: string | RegExp) => {
    await expect(page.getByRole('heading', { level: 1 }).or(page.getByRole('heading', { level: 2 }).first()).first()).toBeVisible();
    if (typeof ready === 'string') await expect(page.getByText(ready).first()).toBeVisible();
    else await expect(page.getByText(ready).first()).toBeVisible();
    for (const theme of ['light', 'dark'] as const) {
      await setTheme(page, theme);
      await axe(page, `${what} (${theme})`);
    }
    await noOverflow(page, what);
  };

  const { page } = await u.open();
  const screens: [string, string, string | RegExp][] = [
    ['/', 'home', c.name],
    ['/calendar', 'calendar', 'Subscribe'],
    ['/chores/new', 'add chore', 'Who can do it'],
    [`/chores/${c.id}`, 'chore detail', 'History'],
    [`/chores/${c.id}/edit`, 'edit chore', 'Notes and link'],
    ['/households/new', 'new household', 'Create household'],
    ['/settings/household-general', 'household settings', /^Time zone$/],
    ['/settings/household-people', 'household people', /^Invites$/],
    ['/settings/household-notifications', 'household notifications', 'Household delivery log'],
    ['/settings/household-connections', 'household connections', /^Calendar feeds$/],
    ['/settings/household-data', 'household data', /^Export and import$/],
    ['/settings/profile', 'account', /^Display name$/],
    ['/settings/security', 'account security', 'Recent account activity'],
    ['/settings/notifications', 'account notifications', 'Email me reminders'],
    ['/settings/ai', 'settings ai', /^Connect an AI assistant$/],
    ['/settings/integrations', 'settings integrations', /^Home Assistant$/],
    ['/settings/data', 'account data', /^Delete account$/],
    ['/nope', 'not found', 'Page not found'],
  ];
  for (const [path, what, ready] of screens) {
    await page.goto(path);
    await check(page, what, ready);
  }

  // Dialogs are screens too.
  await page.goto(`/households/${h.id}/notifications`);
  await page.getByRole('region', { name: 'Household notification channels' }).getByRole('button', { name: 'Add channel' }).click();
  await expect(page.getByRole('dialog').getByLabel('Service', { exact: true })).toBeVisible();
  for (const theme of ['light', 'dark'] as const) {
    await setTheme(page, theme);
    await axe(page, `add channel dialog (${theme})`);
  }
  await page.keyboard.press('Escape');
  await page.goto(`/chores/${c.id}`);
  await page.getByRole('button', { name: 'Snooze', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  for (const theme of ['light', 'dark'] as const) {
    await setTheme(page, theme);
    await axe(page, `snooze dialog (${theme})`);
  }

  // Admin screens.
  const admin = await world.newUser({ admin: true });
  const ap = (await admin.open()).page;
  for (const [path, ready] of [
    ['/admin/users', 'Create user'],
    ['/admin/signin', /^Sessions$/],
    ['/admin/connections', 'Allowed notification services'],
    ['/admin/data', /^Inactive accounts$/],
    ['/admin/limits', /built-in default/],
    ['/admin/updates', 'Running version'],
    ['/admin/email', 'SMTP server'],
    ['/admin/ai', 'AI chore suggestions'],
    ['/setup/guide', "Your server's address"],
  ] as const) {
    await ap.goto(path);
    await check(ap, `admin ${path}`, ready);
  }
  // Phones: the bare URL is the list of groups, a group URL shows that group with a way back.
  await ap.setViewportSize({ width: 390, height: 800 });
  await ap.goto('/admin');
  await expect(ap.getByRole('navigation', { name: 'Administration' }).getByRole('link', { name: /Limits/ })).toBeVisible();
  await expect(ap.getByRole('region', { name: 'Users' })).toBeHidden();
  await ap.getByRole('link', { name: /Limits/ }).click();
  await expect(ap).toHaveURL(/\/admin\/limits$/);
  await expect(ap.getByRole('navigation', { name: 'Administration' })).toBeHidden();
  await ap.getByRole('link', { name: 'Administration' }).last().click();
  await expect(ap).toHaveURL(/\/admin$/);
  await noOverflow(ap, 'admin list on a phone');
  await ap.goto('/');
  await check(ap, 'home without a household', 'You are not in a household yet.');

  // Signed-out screens.
  const anon = await (await world.newContext()).newPage();
  await anon.goto('/signin');
  await check(anon, 'sign in', 'Email or username');
  const inv = await u.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'guest', expiresInHours: 24 });
  await anon.goto(`/invite/${inv.token}`);
  await check(anon, 'invite', 'invited you. Create an account to join.');
});
