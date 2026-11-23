import { expect, test, uniq, ymd } from './helpers';

test('areas are off for a new household and can be switched on and off without losing data', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.goto('/households/new');
  await expect(page.getByLabel(/starter areas/i)).toHaveCount(0);
  const name = uniq('Flat ');
  await page.getByLabel('Household name').fill(name);
  await page.getByRole('button', { name: 'Create household' }).click();
  await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
  const areas = page.getByRole('region', { name: 'Areas' });
  await expect(areas).toHaveCount(0);

  // Off: no area field in the editor, no chips on Home.
  await page.goto('/chores/new');
  await expect(page.getByLabel('Name')).toBeVisible();
  await expect(page.getByLabel('Area', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('group', { name: 'Filter by area' })).toHaveCount(0);

  // On: the section appears with an "Add starter areas" button; chips and the editor field follow.
  await page.goto('/');
  const hh = (await u.api.get<{ id: string; name: string }[]>('/households')).find((x) => x.name === name)!;
  await page.goto(`/households/${hh.id}/general`);
  await page.getByLabel('Group chores by area').check();
  await page.getByRole('button', { name: 'Save' }).first().click();
  await expect(areas).toBeVisible();
  await expect(areas.getByText('No areas yet.')).toBeVisible();
  await areas.getByRole('button', { name: 'Add starter areas' }).click();
  await expect(areas.getByText('Kitchen', { exact: true })).toBeVisible();
  const kitchen = (await u.api.get<{ id: string; name: string }[]>(`/households/${hh.id}/areas`)).find((a) => a.name === 'Kitchen')!;
  const chore = await world.chore(u, hh, { name: uniq('Pans '), areaId: kitchen.id });
  await page.goto('/');
  await expect(page.getByRole('group', { name: 'Filter by area' })).toBeVisible();
  await expect(page.getByRole('link', { name: new RegExp(`${chore.name}.*Kitchen`) })).toBeVisible();
  await page.goto(`/chores/${chore.id}/edit`);
  await expect(page.getByLabel('Area', { exact: true })).toBeVisible();

  // Off again: hidden everywhere, and editing a chore keeps its area.
  await page.goto(`/households/${hh.id}/general`);
  await page.getByLabel('Group chores by area').uncheck();
  await page.getByRole('button', { name: 'Save' }).first().click();
  await expect(areas).toHaveCount(0);
  await page.goto('/');
  await expect(page.getByRole('link', { name: new RegExp(chore.name) })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Filter by area' })).toHaveCount(0);
  await expect(page.getByText('Kitchen')).toHaveCount(0);
  await page.goto(`/chores/${chore.id}/edit`);
  await expect(page.getByLabel('Area', { exact: true })).toHaveCount(0);
  await page.getByLabel('Name').fill(`${chore.name}2`);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: `${chore.name}2` })).toBeVisible();
  const kept = await u.api.get<{ areaId: string | null }>(`/households/${hh.id}/chores/${chore.id}`);
  expect(kept.areaId).toBe(kitchen.id);
});

test('with areas on: chores from a template and a custom one use areas', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await world.household(u, undefined, { areas: true });
  await page.goto('/');

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

test('a new chore pre-ticks the assignees of the last chore created; editing keeps its own', async ({ world }) => {
  const owner = await world.newUser();
  const bee = await world.newUser();
  const { page } = await owner.open();
  const h = await world.household(owner);
  const inv = await owner.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'member' });
  await bee.api.post(`/invites/${inv.token}/accept`);
  const box = (n: string) => page.getByRole('checkbox', { name: n });

  await page.goto('/chores/new');
  await expect(box(bee.displayName)).not.toBeChecked();
  const a = uniq('A ');
  await page.getByLabel('Name').fill(a);
  await box(bee.displayName).check();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: a })).toBeVisible();
  const aUrl = page.url();

  // Next new chore starts with Bee ticked, also from a template.
  await page.goto('/chores/new');
  await expect(box(bee.displayName)).toBeChecked();
  await page.getByLabel('Start from a template').selectOption({ label: 'Wipe counters' });
  await expect(box(bee.displayName)).toBeChecked();

  // Untick everything and save: the next one starts as anyone.
  await box(bee.displayName).uncheck();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Wipe counters' })).toBeVisible();
  await page.goto('/chores/new');
  await expect(box(bee.displayName)).not.toBeChecked();
  await expect(box(owner.displayName)).not.toBeChecked();

  // Editing chore A still shows its own assignees.
  await page.goto(`${aUrl}/edit`);
  await expect(box(bee.displayName)).toBeChecked();

  // A remembered user who is no longer a member is dropped.
  await page.evaluate((k) => localStorage.setItem(k, JSON.stringify(['gone-user'])), `gunkly.lastAssignees.${h.id}`);
  await page.goto('/chores/new');
  await expect(box(bee.displayName)).not.toBeChecked();
});

test('the reminder time of a chore is set in the editor and shown again when reopening', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await world.household(u);
  const name = uniq('Timed ');
  await page.goto('/chores/new');
  await page.getByLabel('Name').fill(name);
  await expect(page.getByRole('button', { name: 'More options' })).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByLabel('Remind me at')).toBeVisible();
  await page.getByLabel('Reminders', { exact: true }).selectOption('none');
  await expect(page.getByLabel('Remind me at')).toHaveCount(0);
  await page.getByLabel('Reminders', { exact: true }).selectOption('nag');
  await page.getByLabel('Remind me at').fill('07:30');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();

  // Reopen: the preset name and the time are shown; saving another field keeps them.
  await page.getByRole('link', { name: 'Edit' }).first().click();
  await expect(page.getByLabel('Remind me at')).toHaveValue('07:30');
  await expect(page.getByLabel('Reminders', { exact: true })).toHaveValue('nag');
  await page.getByLabel('Name').fill(`${name}2`);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: `${name}2` })).toBeVisible();
  await page.getByRole('link', { name: 'Edit' }).first().click();
  await expect(page.getByLabel('Remind me at')).toHaveValue('07:30');

  // More options: choosing Custom lists the reminders, all at 07:30.
  await page.getByRole('button', { name: 'More options' }).click();
  await page.getByLabel('Reminders', { exact: true }).selectOption('custom');
  await expect(page.getByLabel('At', { exact: true })).toHaveCount(3);
  for (const at of await page.getByLabel('At', { exact: true }).all()) await expect(at).toHaveValue('07:30');
});

type Rem = { kind: string; days: number; minute: number };

test('the reminder time can be changed when editing chores in every reminder state', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  const h = await world.household(u);
  const base = `/households/${h.id}`;
  const times = async (id: string): Promise<number[]> =>
    (await u.api.get<{ reminders: Rem[] }>(`${base}/chores/${id}/reminders`)).reminders.map((r) => r.minute);
  const field = page.getByLabel('Remind me at');
  const presetBox = page.getByLabel('Reminders', { exact: true });
  const save = async (): Promise<void> => {
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('link', { name: 'Edit' }).first()).toBeVisible();
  };
  const edit = async (id: string): Promise<void> => {
    await page.goto(`/chores/${id}/edit`);
    await expect(page.getByLabel('Name')).toBeVisible();
  };
  const row = (kind: string, days: number, minute: number): Rem => ({ kind, days, minute });

  // (a) created before the time field existed: household default, no explicit rows
  // (b) gentle and nag presets
  // (d) custom reminders at one time
  // (e) custom reminders at different times
  const cases: [string, Record<string, unknown>][] = [
    ['default', {}],
    ['gentle', { reminderPreset: 'gentle' }],
    ['nag', { reminderPreset: 'nag' }],
  ];
  for (const [label, over] of cases) {
    const c = await world.chore(u, h, { name: uniq(`${label} `), ...over });
    await edit(c.id);
    await expect(page.getByRole('button', { name: 'More options' })).toHaveAttribute('aria-expanded', 'false');
    await expect(field).toBeVisible();
    await field.fill('07:15');
    await save();
    expect((await times(c.id)).length).toBeGreaterThan(0);
    expect(new Set(await times(c.id))).toEqual(new Set([435]));
    await edit(c.id);
    await expect(field).toHaveValue('07:15');
    await field.fill('20:45');
    await save();
    expect(new Set(await times(c.id))).toEqual(new Set([1245]));
  }

  const custom = await world.chore(u, h, { name: uniq('same ') });
  await u.api.call('PUT', `${base}/chores/${custom.id}/reminders`, { reminders: [row('on', 0, 600), row('overdue', 2, 600)] });
  await edit(custom.id);
  await expect(field).toHaveValue('10:00');
  await field.fill('06:00');
  await save();
  expect(new Set(await times(custom.id))).toEqual(new Set([360]));

  const mixed = await world.chore(u, h, { name: uniq('mixed ') });
  await u.api.call('PUT', `${base}/chores/${mixed.id}/reminders`, { reminders: [row('on', 0, 600), row('overdue', 2, 700)] });
  await edit(mixed.id);
  await expect(field).toBeVisible();
  await field.fill('08:00');
  await save();
  expect(new Set(await times(mixed.id))).toEqual(new Set([480]));
  await edit(mixed.id);
  await expect(field).toHaveValue('08:00');

  // (c) preset none: no time field until a preset is chosen
  const none = await world.chore(u, h, { name: uniq('none '), reminderPreset: 'none' });
  await edit(none.id);
  await expect(field).toHaveCount(0);
  await presetBox.selectOption('gentle');
  await field.fill('11:30');
  await save();
  expect(await times(none.id)).toEqual([690]);

  // (f) household default none: same, via the default and via a chosen preset
  await u.api.call('PATCH', base, { defaultReminderPreset: 'none' });
  const dn = await world.chore(u, h, { name: uniq('defnone ') });
  await edit(dn.id);
  await expect(field).toHaveCount(0);
  await presetBox.selectOption('nag');
  await field.fill('12:00');
  await save();
  expect(new Set(await times(dn.id))).toEqual(new Set([720]));
  await edit(dn.id);
  await expect(field).toHaveValue('12:00');
});
