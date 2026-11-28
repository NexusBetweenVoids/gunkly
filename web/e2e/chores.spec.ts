import { expect, test, uniq, ymd } from './helpers';

test('areas are off for a new household and can be switched on and off without losing data', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.goto('/households/new');
  await expect(page.getByLabel(/starter areas/i)).toHaveCount(0);
  const name = uniq('Flat ');
  await page.getByLabel('Household name').fill(name);
  await page.getByRole('button', { name: 'Create household' }).click();
  await expect(page).toHaveURL(/\/settings\/household$/);
  await expect(page.getByRole('region', { name: 'General' }).getByLabel('Name')).toHaveValue(name);
  const areas = page.getByRole('region', { name: 'Areas' });
  await expect(areas).toHaveCount(0);

  // Off: no area field in the editor, no chips on Home.
  await page.goto('/chores/new');
  await expect(page.getByLabel('Name')).toBeVisible();
  await expect(page.getByRole('button', { name: /^Area:/ })).toHaveCount(0);
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
  await expect(page.getByRole('button', { name: /^Area:/ })).toBeVisible();

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
  await expect(page.getByRole('button', { name: /^Area:/ })).toHaveCount(0);
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
  await page.getByRole('button', { name: 'More…' }).click();
  await page.getByRole('dialog', { name: 'Start from a template' }).getByRole('button', { name: 'Wipe counters' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByLabel('Name')).toHaveValue('Wipe counters');
  await expect(page.getByRole('button', { name: 'Area: Kitchen' })).toBeVisible();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Wipe counters' })).toBeVisible();
  await expect(page.getByText('Kitchen', { exact: true })).toBeVisible();

  // A custom chore with its own name, repeat, notes and a new area.
  await page.goto('/chores/new');
  const custom = uniq('Custom ');
  await page.getByLabel('Name').fill(custom);
  await page.getByRole('button', { name: /^Area:/ }).click();
  const sheet = page.getByRole('dialog', { name: 'Area' });
  await sheet.getByLabel('New area…').check();
  await sheet.getByLabel('New area name').fill('Attic');
  await sheet.getByRole('button', { name: 'Done' }).click();
  await expect(page.getByRole('button', { name: 'Area: Attic' })).toBeVisible();
  await page.getByRole('button', { name: /^Repeat:/ }).click();
  const rep = page.getByRole('dialog', { name: 'Repeat' });
  await rep.getByRole('button', { name: 'One more' }).click();
  await expect(rep.getByRole('status')).toContainText('then about');
  await rep.getByLabel('The scheduled date').check();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: /^Repeat: Every 2 weeks/ })).toContainText('fixed dates');
  await page.getByRole('button', { name: /Notes and link/ }).click();
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

test('calendar: overdue days are marked; the day list completes a chore with Undo like Home', async ({ world }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const late = await world.chore(u, h, { name: uniq('Late '), kind: 'oneoff', schedule: { firstDue: ymd(-1) } });
  const c = await world.chore(u, h, { name: uniq('CalDone ') });
  const { page } = await u.open();
  await page.goto('/calendar');
  const cell = page.locator(`#cal-${ymd(-1)}`);
  // Let the month render (today's cell is always on it) before deciding whether yesterday is on it.
  await expect(page.locator(`#cal-${ymd(0)}`)).toBeVisible();
  if (!(await cell.count())) await page.getByRole('button', { name: 'Previous' }).click();
  await expect(cell).toContainText('1 chore, Overdue');
  await expect(cell.locator('.pill.late')).toHaveText('1');
  await expect(page.locator(`#cal-${ymd(0)} .pill.late`)).toHaveCount(0);

  // Past day: a full row with its overdue status and a Done button.
  await cell.click();
  const day = page.getByRole('region', { name: /./ }).filter({ has: page.locator('#cal-day') });
  await expect(day.getByRole('link', { name: late.name })).toContainText('1 day overdue');
  await expect(day.getByRole('button', { name: `Mark ${late.name} as done` })).toBeVisible();

  // Today: complete, then Undo from the toast.
  await page.getByRole('button', { name: 'Today' }).click();
  await day.getByRole('button', { name: `Mark ${c.name} as done` }).click();
  await expect(page.getByText(`Done: ${c.name}`)).toBeVisible();
  await expect(day.getByRole('link', { name: c.name })).toHaveCount(0);
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(page.getByText(`Undone: ${c.name}`)).toBeVisible();
  await expect(day.getByRole('link', { name: c.name })).toBeVisible();
  const list = await u.api.get<{ id: string; status: string }[]>(`/households/${h.id}/chores`);
  expect(list.find((x) => x.id === c.id)?.status).toBe('today');
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
  await page.getByRole('button', { name: 'More…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Wipe counters' }).click();
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
  const remRow = page.getByRole('button', { name: /^Reminders:/ });
  const sheet = page.getByRole('dialog', { name: 'Reminders' });
  await remRow.click();
  await expect(sheet.getByLabel('Remind me at')).toBeVisible();
  await sheet.getByLabel('No reminders').check();
  await expect(sheet.getByLabel('Remind me at')).toHaveCount(0);
  await sheet.getByLabel(/^Nag/).check();
  await sheet.getByLabel('Remind me at').fill('07:30');
  await sheet.getByRole('button', { name: 'Done' }).click();
  await expect(remRow).toContainText('At 07:30');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();

  // Reopen: the preset name and the time are shown; saving another field keeps them.
  await page.getByRole('link', { name: 'Edit' }).first().click();
  await expect(remRow).toContainText('Nag');
  await expect(remRow).toContainText('At 07:30');
  await page.getByLabel('Name').fill(`${name}2`);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: `${name}2` })).toBeVisible();
  await page.getByRole('link', { name: 'Edit' }).first().click();
  await remRow.click();
  await expect(sheet.getByLabel('Remind me at')).toHaveValue('07:30');

  // Choosing Custom lists the reminders, all at 07:30.
  await sheet.getByLabel('Custom', { exact: true }).check();
  await expect(sheet.getByLabel('At', { exact: true })).toHaveCount(3);
  for (const at of await sheet.getByLabel('At', { exact: true }).all()) await expect(at).toHaveValue('07:30');
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
  const preset = (label: RegExp) => page.getByRole('dialog', { name: 'Reminders' }).getByLabel(label).check();
  const save = async (): Promise<void> => {
    await page.getByRole('dialog', { name: 'Reminders' }).getByRole('button', { name: 'Done' }).click();
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('link', { name: 'Edit' }).first()).toBeVisible();
  };
  const edit = async (id: string): Promise<void> => {
    await page.goto(`/chores/${id}/edit`);
    await expect(page.getByLabel('Name')).toBeVisible();
    await page.getByRole('button', { name: /^Reminders:/ }).click();
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
  await preset(/^Gentle/);
  await field.fill('11:30');
  await save();
  expect(await times(none.id)).toEqual([690]);

  // (f) household default none: same, via the default and via a chosen preset
  await u.api.call('PATCH', base, { defaultReminderPreset: 'none' });
  const dn = await world.chore(u, h, { name: uniq('defnone ') });
  await edit(dn.id);
  await expect(field).toHaveCount(0);
  await preset(/^Nag/);
  await field.fill('12:00');
  await save();
  expect(new Set(await times(dn.id))).toEqual(new Set([720]));
  await edit(dn.id);
  await expect(field).toHaveValue('12:00');
});

test('"On" pins a weekly or monthly chore to weekdays or the first Saturday, and reopens that way', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  const h = await world.household(u);
  const get = async (name: string) =>
    (await u.api.get<{ name: string; schedule: { type: string; rrule?: string; firstDue: string } }[]>(`/households/${h.id}/chores`)).find((c) => c.name === name)!;
  const repeatRow = page.getByRole('button', { name: /^Repeat:/ });
  const sheet = page.getByRole('dialog', { name: 'Repeat' });

  // Weekly on Saturday: the first due date moves to a Saturday.
  const sat = uniq('Sat ');
  await page.goto('/chores/new');
  await page.getByLabel('Name').fill(sat);
  await repeatRow.click();
  await sheet.getByRole('button', { name: 'Saturday' }).click();
  await expect(sheet.getByRole('button', { name: 'Saturday' })).toHaveAttribute('aria-pressed', 'true');
  await sheet.getByRole('button', { name: 'Done' }).click();
  await expect(repeatRow).toContainText('Saturday');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: sat })).toBeVisible();
  const a = await get(sat);
  expect(a.schedule.type).toBe('rrule');
  expect(a.schedule.rrule).toBe('FREQ=WEEKLY;BYDAY=SA');
  expect(new Date(`${a.schedule.firstDue}T00:00:00Z`).getUTCDay()).toBe(6);

  // Monthly, every 3 months on the first Saturday.
  const first = uniq('First ');
  await page.goto('/chores/new');
  await page.getByLabel('Name').fill(first);
  await repeatRow.click();
  await sheet.getByText('Monthly', { exact: true }).click();
  await sheet.getByRole('button', { name: 'One more' }).click();
  await sheet.getByRole('button', { name: 'One more' }).click();
  await sheet.getByLabel(/^A weekday/).check();
  await sheet.getByLabel('Weekday', { exact: true }).selectOption('SA');
  await sheet.getByRole('button', { name: 'Done' }).click();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: first })).toBeVisible();
  const b = await get(first);
  expect(b.schedule.rrule).toBe('FREQ=MONTHLY;INTERVAL=3;BYDAY=1SA');
  const d = new Date(`${b.schedule.firstDue}T00:00:00Z`);
  expect([d.getUTCDay(), d.getUTCDate() <= 7]).toEqual([6, true]);

  // Reopening shows the simple form with "On" set, not a custom rule.
  await page.getByRole('link', { name: 'Edit' }).first().click();
  await repeatRow.click();
  await expect(sheet.getByLabel(/^A weekday/)).toBeChecked();
  await expect(sheet.getByLabel('Every', { exact: true })).toHaveValue('3');
  await expect(sheet.getByLabel('Weekday', { exact: true })).toHaveValue('SA');
});

test('a calendar rule the editor cannot show (BYSETPOS) is shown as it is and kept when saving', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  const h = await world.household(u);
  const rrule = 'FREQ=YEARLY;BYMONTH=4,10;BYMONTHDAY=1,2,3,4,5,6,7,8,9,10,11,12,13,14;BYDAY=SA;BYSETPOS=-1,1';
  const c = await world.chore(u, h, { name: uniq('Gutters '), kind: 'recurring', schedule: { type: 'rrule', rrule, firstDue: '2026-11-26' } });
  await page.goto(`/chores/${c.id}`);
  await expect(page.getByText(`Custom rule: ${rrule}`)).toBeVisible();

  await page.goto(`/chores/${c.id}/edit`);
  const row = page.getByRole('button', { name: /^Repeat:/ });
  await expect(row).toContainText('Custom rule');
  await row.click();
  const sheet = page.getByRole('dialog', { name: 'Repeat' });
  await expect(sheet.getByText(/can't show\. It stays exactly as it is/)).toBeVisible();
  await expect(sheet.getByText(rrule)).toBeVisible();
  await sheet.getByRole('button', { name: 'Done' }).click();
  await page.getByLabel('Name').fill(`${c.name} (both)`);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { level: 1, name: `${c.name} (both)` })).toBeVisible();
  const after = await u.api.get<{ schedule: { rrule: string } }>(`/households/${h.id}/chores/${c.id}`);
  expect(after.schedule.rrule).toBe(rrule);
});
