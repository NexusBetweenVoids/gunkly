import { expect, test } from './helpers';

test('the Mine toggle appears only once someone else has a chore of their own', async ({ world }) => {
  const owner = await world.newUser();
  const other = await world.newUser();
  const h = await world.household(owner);
  const mine = await world.chore(owner, h);
  const { page } = await owner.open();
  const toggle = page.getByRole('group', { name: 'Whose chores' });

  await page.goto('/');
  await expect(page.getByRole('link', { name: mine.name })).toBeVisible();
  await expect(toggle).toHaveCount(0); // single member

  const inv = await owner.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'member' });
  await other.api.post(`/invites/${inv.token}/accept`);
  await page.reload();
  await expect(page.getByRole('link', { name: mine.name })).toBeVisible();
  await expect(toggle).toHaveCount(0); // two members, nothing assigned

  const members = await owner.api.get<{ userId: string; displayName: string }[]>(`/households/${h.id}/members`);
  const otherId = members.find((m) => m.displayName === other.displayName)!.userId;
  const theirs = await world.chore(owner, h, { assignees: [otherId] });
  await page.reload();
  await expect(toggle).toBeVisible();
  await toggle.getByRole('button', { name: 'Mine' }).click();
  await expect(page.getByRole('link', { name: mine.name })).toBeVisible();
  await expect(page.getByRole('link', { name: theirs.name })).toHaveCount(0);
  await toggle.getByRole('button', { name: 'Everyone' }).click();
  await expect(page.getByRole('link', { name: theirs.name })).toBeVisible();
});
