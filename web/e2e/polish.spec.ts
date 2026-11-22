import { expect, headerItem, test, uniq } from './helpers';

test('header menu on phones: keyboard open, arrow keys, Escape returns focus', async ({ world }, info) => {
  test.skip(info.project.name !== 'mobile', 'the menu only exists below the sidebar breakpoint');
  const u = await world.newUser();
  await world.household(u);
  const { page } = await u.open();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  const nav = page.getByRole('navigation', { name: 'Main' });
  const menu = nav.getByRole('button', { name: 'Menu' });
  await expect(menu).toHaveAttribute('aria-expanded', 'false');
  await expect(nav.getByRole('link', { name: 'Account' })).toBeHidden();
  const bar = await nav.boundingBox();
  expect(bar!.height).toBeLessThan(70); // one calm row

  await menu.focus();
  await page.keyboard.press('Enter');
  await expect(menu).toHaveAttribute('aria-expanded', 'true');
  await expect(nav.getByRole('link', { name: 'New household' })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(nav.getByRole('link', { name: 'Account' })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(nav.getByRole('button', { name: 'Sign out' })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(nav.getByRole('link', { name: 'New household' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(menu).toHaveAttribute('aria-expanded', 'false');
  await expect(menu).toBeFocused();

  // Tabbing out closes it; a link navigates and closes it.
  await page.keyboard.press('Enter');
  await page.keyboard.press('Tab');
  await page.keyboard.press('Tab');
  await page.keyboard.press('Tab');
  await expect(menu).toHaveAttribute('aria-expanded', 'false');
  await (await headerItem(page, 'link', 'Account')).click();
  await expect(page).toHaveURL(/\/account$/);
  await expect(menu).toHaveAttribute('aria-expanded', 'false');
});

test('header menu mirrors in RTL', async ({ world }, info) => {
  test.skip(info.project.name !== 'mobile');
  const u = await world.newUser();
  await world.household(u);
  const { page } = await u.open();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await page.evaluate(() => (document.documentElement.dir = 'rtl'));
  const nav = page.getByRole('navigation', { name: 'Main' });
  const menu = nav.getByRole('button', { name: 'Menu' });
  await menu.click();
  const b = await menu.boundingBox();
  const p = await nav.getByRole('link', { name: 'Account' }).boundingBox();
  expect(b!.x).toBeLessThan(100); // the button sits at the inline start edge in RTL
  expect(p!.x).toBeLessThan(150);
});

test('home search filters by name, area and assignee; lives in the URL only', async ({ world }) => {
  const u = await world.newUser();
  const h = await world.household(u, undefined, { areas: true });
  const areas = await u.api.get<{ id: string; name: string }[]>(`/households/${h.id}/areas`);
  const members = await u.api.get<{ userId: string }[]>(`/households/${h.id}/members`);
  const garden = areas.find((a) => a.name === 'Garden')!;
  const a = await world.chore(u, h, { name: uniq('Zebra ') });
  const b = await world.chore(u, h, { name: uniq('Yak '), areaId: garden.id });
  const c = await world.chore(u, h, { name: uniq('Xerus '), assignees: [members[0].userId] });
  const { page } = await u.open();
  await page.goto('/');
  const search = page.getByLabel('Search chores');
  await expect(page.getByText(a.name)).toBeVisible();

  await search.fill('zebra');
  await expect(page).toHaveURL(/\?q=zebra$/);
  await expect(page.getByText(a.name)).toBeVisible();
  await expect(page.getByText(b.name)).toBeHidden();
  await search.fill('garden');
  await expect(page.getByText(b.name)).toBeVisible();
  await expect(page.getByText(a.name)).toBeHidden();
  await search.fill(u.displayName.toUpperCase());
  await expect(page.getByText(c.name)).toBeVisible();
  await expect(page.getByText(b.name)).toBeHidden();
  await search.fill('nothing-like-this');
  await expect(page.getByText('No chores match your search.')).toBeVisible();

  // A reload keeps it (URL), storage is untouched; Clear empties and drops the parameter.
  await search.fill('yak');
  await page.reload();
  await expect(search).toHaveValue('yak');
  await expect(page.getByText(b.name)).toBeVisible();
  expect(await page.evaluate(() => localStorage.length + sessionStorage.length)).toBe(0);
  await page.getByRole('button', { name: 'Clear search' }).click();
  await expect(search).toHaveValue('');
  await expect(search).toBeFocused();
  await expect(page).not.toHaveURL(/q=/);
  await expect(page.getByText(a.name)).toBeVisible();
});
