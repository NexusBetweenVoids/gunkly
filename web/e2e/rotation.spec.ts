import { expect, test, uniq } from './helpers';

test('take turns: set up in the editor, badge on Home, Mine, turn passes on completion, change turn', async ({ world }) => {
  const owner = await world.newUser();
  const other = await world.newUser();
  const h = await world.household(owner);
  const inv = await owner.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'member' });
  await other.api.post(`/invites/${inv.token}/accept`);
  const { page } = await owner.open();
  const name = uniq('Bins ');

  await page.goto('/chores/new');
  await page.getByLabel('Name').fill(name);
  await expect(page.getByLabel('Take turns')).toHaveCount(0); // needs 2+ assignees
  await page.getByLabel(owner.displayName).check();
  await expect(page.getByLabel('Take turns')).toHaveCount(0);
  await page.getByLabel(other.displayName).check();
  await page.getByLabel('Take turns').check();
  // Order: owner then other; move the other one up.
  const order = page.getByRole('list', { name: 'Order of turns' });
  await expect(order.getByRole('listitem').first()).toContainText(owner.displayName);
  await order.getByRole('button', { name: `Move ${other.displayName} up` }).click();
  await expect(order.getByRole('listitem').first()).toContainText(other.displayName);
  await expect(order.getByRole('button', { name: `Move ${other.displayName} up` })).toBeDisabled();
  await page.getByRole('button', { name: 'Save' }).click();

  // The first person in the order has the turn; the detail page says so.
  await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
  await expect(page.getByRole('term').filter({ hasText: 'Turn' })).toBeVisible();
  await expect(page.getByRole('definition').filter({ hasText: other.displayName }).first()).toBeVisible();
  const list = await owner.api.get<{ id: string; name: string; turnUserId: string; rotation: boolean }[]>(`/households/${h.id}/chores`);
  const chore = list.find((c) => c.name === name)!;
  expect(chore.rotation).toBe(true);

  // Home: badge, and Mine only for the person whose turn it is.
  await page.goto('/');
  const row = page.getByRole('listitem').filter({ has: page.getByRole('link', { name }) });
  await expect(row.getByText(`${other.displayName}'s turn`)).toBeVisible();
  const toggle = page.getByRole('group', { name: 'Whose chores' });
  await toggle.getByRole('button', { name: 'Mine' }).click();
  await expect(page.getByRole('link', { name })).toHaveCount(0);
  await toggle.getByRole('button', { name: 'Everyone' }).click();

  // "It's <name>'s turn now" from the chore page.
  await page.goto(`/chores/${chore.id}`);
  await page.getByRole('button', { name: 'Change' }).click();
  await page.getByLabel("It's this person's turn now").selectOption({ label: owner.displayName });
  await expect(page.getByText(`It's ${owner.displayName}'s turn now.`)).toBeVisible();
  const members = await owner.api.get<{ userId: string; displayName: string }[]>(`/households/${h.id}/members`);
  const idOf = (n: string) => members.find((m) => m.displayName === n)!.userId;
  expect((await owner.api.get<{ turnUserId: string }>(`/households/${h.id}/chores/${chore.id}`)).turnUserId).toBe(idOf(owner.displayName));

  // It is now the owner's: nothing is left out of Mine (so the toggle is gone). Completing passes the turn on.
  await page.goto('/');
  await expect(toggle).toHaveCount(0);
  await page.getByRole('button', { name: `Mark ${name} as done` }).click();
  await expect(page.getByRole('listitem').filter({ has: page.getByRole('link', { name }) }).getByText(`${other.displayName}'s turn`)).toBeVisible();
  expect((await owner.api.get<{ turnUserId: string }>(`/households/${h.id}/chores/${chore.id}`)).turnUserId).toBe(idOf(other.displayName));
});
