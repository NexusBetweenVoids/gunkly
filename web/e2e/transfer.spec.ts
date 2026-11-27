import { readFileSync } from 'node:fs';
import { axe, expect, PASSWORD, test, uniq, ymd, type Household } from './helpers';

test('export a household, import it as a new household: the same chores appear', async ({ world }, testInfo) => {
  const u = await world.newUser();
  const h = await world.household(u, uniq('Flat '));
  const a = uniq('Dust ');
  const b = uniq('Water plants ');
  await world.chore(u, h, { name: a, notes: 'Top shelves first', schedule: { type: 'interval', interval: { every: 2, unit: 'week' }, anchor: 'from_completion', firstDue: ymd(-3) } });
  await world.chore(u, h, { name: b, kind: 'oneoff', schedule: { firstDue: ymd(5) } });

  const { page } = await u.open();
  await page.goto(`/households/${h.id}/data`);
  const section = page.getByRole('region', { name: 'Export and import' });
  await axe(page, 'household settings with export and import');

  // Export: a JSON attachment without secrets.
  const [download] = await Promise.all([page.waitForEvent('download'), section.getByRole('button', { name: 'Export household' }).click()]);
  expect(download.suggestedFilename()).toMatch(/^gunkly-household-export-\d{4}-\d{2}-\d{2}\.json$/);
  const file = testInfo.outputPath('household.json');
  await download.saveAs(file);
  const doc = JSON.parse(readFileSync(file, 'utf8')) as { format: string; version: number; chores: { name: string }[] };
  expect(doc.format).toBe('gunkly-household-export');
  expect(doc.version).toBe(1);
  expect(doc.chores.map((c) => c.name).sort()).toEqual([a, b].sort());

  // A broken file is refused with a readable message that says where.
  const broken = JSON.parse(readFileSync(file, 'utf8')) as { chores: { link: string }[] };
  broken.chores[0].link = 'javascript:alert(1)';
  await section.getByLabel('Household file').setInputFiles({ name: 'broken.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(broken)) });
  await section.getByRole('button', { name: 'Import' }).click();
  await expect(section.getByRole('alert')).toContainText("A value isn't valid.");
  await expect(section.getByRole('alert')).toContainText('chores[0].link');
  await expect(page.getByRole('heading', { level: 1, name: /^(Settings|Data)$/ })).toBeVisible(); // phones title the open group

  // Import as a new household.
  await section.getByLabel('Household file').setInputFiles(file);
  await section.getByLabel('A new household (you become its owner)').check();
  await section.getByRole('button', { name: 'Import' }).click();
  await expect(section.getByRole('status')).toContainText(`Created “${h.name}” from the file.`);
  await expect(section.getByText('Chores added: 2')).toBeVisible();
  await section.getByRole('button', { name: 'Open household' }).click();
  await expect(page).toHaveURL(/\/settings\/household$/);

  const all = await u.api.get<Household[]>('/households');
  const copies = all.filter((x) => x.name === h.name && x.id !== h.id);
  expect(copies).toHaveLength(1);
  const names = async (id: string) =>
    (await u.api.get<{ name: string }[]>(`/households/${id}/chores`)).map((c) => c.name).sort();
  expect(await names(copies[0].id)).toEqual(await names(h.id));
  expect(await names(copies[0].id)).toEqual([a, b].sort());

  // Merge back into the original: chores are added as new ones.
  await page.goto(`/households/${h.id}/data`);
  await section.getByLabel('Household file').setInputFiles(file);
  await section.getByLabel('Merge into this household').check();
  await section.getByRole('button', { name: 'Import' }).click();
  await expect(section.getByRole('status')).toContainText(`Merged the file into “${h.name}”.`);
  expect((await names(h.id)).length).toBe(4);
});

test('guests cannot export or import a household', async ({ world }) => {
  const owner = await world.newUser();
  const guest = await world.newUser();
  const h = await world.household(owner);
  const inv = await owner.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'guest' });
  await guest.api.post(`/invites/${inv.token}/accept`);
  const { page } = await guest.open();
  await page.goto(`/households/${h.id}/data`);
  await expect(page.getByRole('heading', { level: 1, name: /^(Settings|Data)$/ })).toBeVisible(); // phones title the open group
  await expect(page.getByRole('region', { name: 'Export and import' })).toHaveCount(0);
  await guest.api.call('GET', `/households/${h.id}/export`, undefined, 403);
});

test('export my data asks for the password and downloads the file', async ({ world }, testInfo) => {
  const u = await world.newUser();
  const h = await world.household(u);
  await world.chore(u, h);
  const { page } = await u.open();
  await page.goto('/settings/data');
  const section = page.getByRole('region', { name: 'Export my data' });
  await section.getByRole('button', { name: 'Export my data' }).click();
  const dialog = page.getByRole('dialog', { name: "Confirm it's you" });
  await dialog.getByLabel('Password').fill(PASSWORD);
  const [download] = await Promise.all([page.waitForEvent('download'), dialog.getByRole('button', { name: 'Confirm' }).click()]);
  expect(download.suggestedFilename()).toMatch(/^gunkly-user-export-/);
  const file = testInfo.outputPath('me.json');
  await download.saveAs(file);
  const doc = JSON.parse(readFileSync(file, 'utf8')) as { format: string; profile: { username: string }; memberships: unknown[] };
  expect(doc.format).toBe('gunkly-user-export');
  expect(doc.profile.username).toBe(u.username);
  expect(doc.memberships).toHaveLength(1);
});
