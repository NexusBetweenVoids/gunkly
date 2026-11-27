import { axe, expect, setTheme, test } from './helpers';

test('welcome flow: household, starter chores spread over the week, invite link, then Home', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.goto('/');
  await page.getByRole('link', { name: 'Set up your home' }).click();
  await expect(page).toHaveURL(/\/welcome$/);
  const steps = page.getByRole('list', { name: 'Setup steps' });
  await expect(steps.getByRole('listitem')).toHaveCount(3); // self-hosted: no email step
  await expect(steps.locator('[aria-current="step"]')).toContainText('Household');
  for (const theme of ['light', 'dark'] as const) {
    await setTheme(page, theme);
    await axe(page, `welcome: household (${theme})`);
  }

  // Household: a name is suggested; areas on by default.
  const name = page.getByLabel('Household name');
  await expect(name).not.toHaveValue('');
  await name.fill('Test home');
  await expect(page.getByLabel(/Group chores by area/)).toBeChecked();
  await page.getByRole('button', { name: 'Create household' }).click();

  // Chores: common ones ticked; untick one, tick another.
  await expect(page.getByRole('heading', { name: 'Pick a few chores to start with' })).toBeVisible();
  await expect(page.getByLabel('Vacuum')).toBeChecked();
  await page.getByLabel('Vacuum').uncheck();
  await page.getByLabel('Clean gutters').check();
  await axe(page, 'welcome: chores');
  await page.getByRole('button', { name: 'Add 6 chores' }).click();

  // Invite: a link to copy (or share).
  await expect(page.getByRole('heading', { name: 'Invite someone' })).toBeVisible();
  await page.getByRole('button', { name: 'Create an invite link' }).click();
  await expect(page.getByText(/\/invite\//)).toBeVisible();
  await axe(page, 'welcome: invite');
  await page.getByRole('button', { name: 'Go to my chores' }).click();
  await expect(page.getByRole('heading', { name: 'Chores' })).toBeVisible();

  // The chores exist, with their areas, and not all are due today.
  const hh = (await u.api.get<{ id: string; name: string }[]>('/households')).find((h) => h.name === 'Test home')!;
  const chores = await u.api.get<{ name: string; areaId: string | null; dueDate: string }[]>(`/households/${hh.id}/chores`);
  expect(chores.map((c) => c.name).sort()).toEqual(
    ['Change bedding', 'Clean bathroom', 'Clean gutters', 'Take out trash', 'Water plants', 'Wipe counters'].sort(),
  );
  expect(chores.every((c) => c.areaId)).toBe(true);
  expect(new Set(chores.map((c) => c.dueDate)).size).toBeGreaterThan(1);
});

test('sign-in page: a clear way in for new people when sign-up is open', async ({ world }) => {
  await world.admin.call('PATCH', '/admin/settings', { openSignup: true });
  try {
    const page = await (await world.newContext()).newPage();
    await page.goto('/signin');
    await expect(page.getByText('New to Gunkly? Create a free account in a minute.')).toBeVisible();
    await axe(page, 'sign-in with sign-up open');
    await page.getByRole('link', { name: 'Create an account' }).click();
    await expect(page).toHaveURL(/\/signup$/);
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { openSignup: false });
  }
});
