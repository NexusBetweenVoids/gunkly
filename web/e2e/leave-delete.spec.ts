import { expect, PASSWORD, test } from './helpers';

test('leaving as the last owner asks who takes over, then hands the household on', async ({ world }) => {
  const owner = await world.newUser();
  const heir = await world.newUser();
  const h = await world.household(owner);
  const inv = await owner.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'member' });
  await heir.api.post(`/invites/${inv.token}/accept`);

  const { page } = await owner.open();
  await page.goto(`/households/${h.id}/data`);
  await page.getByRole('region', { name: 'Leave household' }).getByRole('button', { name: 'Leave household' }).click();
  await page.getByRole('dialog', { name: `Leave ${h.name}?` }).getByRole('button', { name: 'Leave household' }).click();

  const pick = page.getByRole('dialog', { name: `Choose a new owner for ${h.name}` });
  await expect(pick.getByLabel('New owner')).toContainText(heir.displayName);
  await pick.getByLabel('New owner').selectOption({ label: heir.displayName });
  await pick.getByRole('button', { name: 'Leave household' }).click();
  await expect(page).toHaveURL(/\/$/);

  const members = await heir.api.get<{ userId: string; role: string }[]>(`/households/${h.id}/members`);
  expect(members).toHaveLength(1);
  expect(members[0].role).toBe('owner');
});

test('leaving as the only member asks to confirm deleting the household', async ({ world }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const { page } = await u.open();
  await page.goto(`/households/${h.id}/data`);
  await page.getByRole('region', { name: 'Leave household' }).getByRole('button', { name: 'Leave household' }).click();
  await page.getByRole('dialog', { name: `Leave ${h.name}?` }).getByRole('button', { name: 'Leave household' }).click();

  const del = page.getByRole('dialog', { name: `Delete ${h.name}?` });
  await expect(del).toContainText('permanently delete');
  await del.getByRole('button', { name: 'Leave and delete household' }).click();
  await expect(page).toHaveURL(/\/$/);
  await u.api.call('GET', `/households/${h.id}`, undefined, 404);
});

test('delete my account: preview, new owner, username confirmation, then signed out', async ({ world }) => {
  const u = await world.newUser();
  const heir = await world.newUser();
  const shared = await world.household(u);
  const solo = await world.household(u, 'Solo flat');
  const inv = await u.api.post<{ token: string }>(`/households/${shared.id}/invites`, { role: 'member' });
  await heir.api.post(`/invites/${inv.token}/accept`);

  const { page } = await u.open();
  await page.goto('/settings/data');
  const section = page.getByRole('region', { name: 'Delete account' });
  await section.getByRole('button', { name: 'Delete my account...' }).click();
  await expect(section.getByText(solo.name)).toBeVisible();
  await section.getByLabel(`New owner of ${shared.name}`).selectOption({ label: heir.displayName });

  // A wrong username is refused, and nothing is deleted.
  await section.getByLabel(/Type your username/).fill('somebody-else');
  await section.getByRole('button', { name: 'Delete my account permanently' }).click();
  const dialog = page.getByRole('dialog', { name: "Confirm it's you" });
  await dialog.getByLabel('Password').fill(PASSWORD);
  await dialog.getByRole('button', { name: 'Confirm' }).click();
  await expect(section.getByRole('alert')).toBeVisible();

  await section.getByLabel(/Type your username/).fill(u.username);
  await section.getByRole('button', { name: 'Delete my account permanently' }).click();
  await expect(page).toHaveURL(/\/signin/);

  const members = await heir.api.get<{ role: string }[]>(`/households/${shared.id}/members`);
  expect(members.map((m) => m.role)).toEqual(['owner']);
  await heir.api.call('GET', `/households/${solo.id}`, undefined, 404);
});
