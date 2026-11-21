import { expect, test, uniq, ymd } from './helpers';

test('create a household with starter areas, then chores from a template and a custom one', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.goto('/households/new');

  const name = uniq('Flat ');
  await page.getByLabel('Household name').fill(name);
  await page.getByRole('button', { name: 'Create household' }).click();
  await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
  const areas = page.getByRole('region', { name: 'Areas' });
  for (const a of ['Kitchen', 'Bathroom', 'Garden', 'Exterior', 'Appliances', 'General']) {
    await expect(areas.getByText(a, { exact: true })).toBeVisible();
  }

  // From a template: name, area and interval are prefilled.
  await page.goto('/chores/new');
  await page.getByLabel('Start from a template').selectOption({ label: 'Wipe counters' });
  await expect(page.getByLabel('Name')).toHaveValue('Wipe counters');
  await expect(page.getByLabel('Area')).toHaveText(/Kitchen/);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Wipe counters' })).toBeVisible();
  await expect(page.getByText('Kitchen', { exact: true })).toBeVisible();

  // A custom chore with its own name, repeat, notes and a new area.
  await page.goto('/chores/new');
  const custom = uniq('Custom ');
  await page.getByLabel('Name').fill(custom);
  await page.getByLabel('Area').selectOption('__new');
  await page.getByLabel('New area name').fill('Attic');
  await page.getByLabel('Repeat').selectOption({ label: 'Every 2 weeks' });
  await page.getByRole('button', { name: 'More options' }).click();
  await page.getByLabel('Notes').fill('Bring a torch');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: custom })).toBeVisible();
  await expect(page.getByText('Attic', { exact: true })).toBeVisible();
  await expect(page.getByText('Every 2 weeks')).toBeVisible();
  await expect(page.getByText('Bring a torch')).toBeVisible();

  // Both show up on Home.
  await page.goto('/');
  await expect(page.getByRole('link', { name: /Wipe counters/ })).toBeVisible();
  await expect(page.getByRole('link', { name: new RegExp(custom) })).toBeVisible();
});

test('complete from home in at most 3 taps; the toast Undo restores it', async ({ world, hasTouch }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const c = await world.chore(u, h, { name: uniq('Dishes ') });
  const { page } = await u.open();
  await page.goto('/');

  let taps = 0;
  const tap = async (l: ReturnType<typeof page.locator>) => {
    taps++;
    await (hasTouch ? l.tap() : l.click());
  };

  const today = page.getByRole('region', { name: 'Today' });
  const done = page.getByRole('button', { name: `Mark ${c.name} as done` });
  await expect(today.getByText(c.name)).toBeVisible();

  await tap(done); // tap 1: Done
  await expect(page.getByText(`Done: ${c.name}`)).toBeVisible();
  await expect(today.getByText(c.name)).toHaveCount(0);
  await expect(page.getByRole('region', { name: /This week|Later/ }).getByText(c.name)).toBeVisible();

  await tap(page.getByRole('button', { name: 'Undo' })); // tap 2: Undo
  await expect(page.getByText(`Undone: ${c.name}`)).toBeVisible();
  await expect(today.getByText(c.name)).toBeVisible();
  expect(taps).toBeLessThanOrEqual(3);
  expect(taps).toBe(2);
  // The server agrees.
  const list = await u.api.get<{ id: string; status: string }[]>(`/households/${h.id}/chores`);
  expect(list.find((x) => x.id === c.id)?.status).toBe('today');
});

test('chore detail: snooze, skip, pause/resume, archive, history revert', async ({ world }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const c = await world.chore(u, h, { name: uniq('Detail ') });
  const { page } = await u.open();
  await page.goto(`/chores/${c.id}`);
  await expect(page.getByRole('heading', { level: 1, name: c.name })).toBeVisible();
  const status = page.locator('.badge').first();
  await expect(status).toHaveText('Due today');

  // Snooze (dialog defaults to tomorrow).
  await page.getByRole('button', { name: 'Snooze', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Snooze' }).click();
  await expect(page.getByText('Snoozed.')).toBeVisible();
  await expect(page.getByText('Tomorrow', { exact: true }).first()).toBeVisible();

  // Skip once moves a week on.
  await page.getByRole('button', { name: 'Skip once' }).click();
  await expect(page.getByText('Skipped to the next date.')).toBeVisible();
  await expect(status).not.toHaveText('Due today');

  // Pause indefinitely, then resume.
  await page.getByRole('button', { name: 'Pause', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Pause indefinitely' }).click();
  await expect(page.getByText('Paused until you resume it.')).toBeVisible();
  await expect(status).toHaveText('Paused');
  await page.getByRole('button', { name: 'Resume' }).click();
  await expect(page.getByText('Resumed.')).toBeVisible();
  await expect(page.getByText('Paused until you resume it.')).toHaveCount(0);

  // Complete from the detail page and revert it from the history.
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(page.getByText(`Done: ${c.name}`)).toBeVisible();
  const history = page.getByRole('region', { name: 'History' });
  await expect(history.getByRole('listitem')).toHaveCount(1);
  await history.getByRole('button', { name: /^Undo completion/ }).click();
  await expect(page.getByText('Completion undone.')).toBeVisible();
  await expect(history.getByText('Reverted')).toBeVisible();

  // Archive and restore.
  await page.getByRole('button', { name: 'Archive' }).click();
  await expect(page.getByText('Archived.')).toBeVisible();
  await expect(status).toHaveText('Archived');
  await page.getByRole('button', { name: 'Unarchive' }).click();
  await expect(page.getByText('Restored.')).toBeVisible();
  await expect(status).not.toHaveText('Archived');
});

test('calendar month and week show the chore; arrow keys move the selection', async ({ world }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const c = await world.chore(u, h, { name: uniq('Cal ') });
  const { page } = await u.open();
  await page.goto('/calendar');
  const today = ymd(0);
  const cell = (d: string) => page.locator(`#cal-${d}`);

  await expect(cell(today)).toContainText(/1 chore/);
  await expect(page.getByRole('region', { name: /./ }).getByRole('link', { name: c.name })).toBeVisible();

  await page.getByRole('button', { name: 'Week' }).click();
  await expect(cell(today)).toContainText(c.name);

  await page.getByRole('button', { name: 'Month' }).click();
  await cell(today).focus();
  const next = ymd(1);
  await page.keyboard.press('ArrowRight');
  await expect(cell(next)).toBeFocused();
  await expect(cell(next)).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowLeft');
  await expect(cell(today)).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(cell(ymd(7))).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await expect(cell(today)).toBeFocused();
  // Weekly recurrence: a week ahead has no entry until completed, so just check paging works.
  const title = await page.locator('#cal-title').textContent();
  await page.keyboard.press('PageDown');
  await expect(page.locator('#cal-title')).not.toHaveText(title ?? '');
});
