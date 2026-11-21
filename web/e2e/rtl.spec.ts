import { expect, test, uniq } from './helpers';

// Desktop project only: it resizes the viewport itself to cover phone and desktop layouts.
test('RTL: navigation mirrors and nothing overflows at 360px', async ({ world }) => {
  test.setTimeout(60_000);
  const u = await world.newUser();
  const h = await world.household(u);
  const c = await world.chore(u, h, { name: uniq('Rtl ') });
  const { page } = await u.open();
  const rtl = () => page.evaluate(() => (document.documentElement.dir = 'rtl'));
  const box = async (name: string) => (await page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name }).boundingBox())!;

  // Phone: bottom tab bar. Home is first in reading order: left in LTR, right in RTL.
  await page.setViewportSize({ width: 360, height: 740 });
  await page.goto('/');
  await expect(page.getByRole('link', { name: new RegExp(c.name) })).toBeVisible();
  const [ltrHome, ltrSettings] = [await box('Home'), await box('Settings')];
  expect(ltrHome.x).toBeLessThan(ltrSettings.x);
  await rtl();
  const [rtlHome, rtlSettings] = [await box('Home'), await box('Settings')];
  expect(rtlHome.x).toBeGreaterThan(rtlSettings.x);

  const paths = ['/', '/calendar', '/chores/new', `/chores/${c.id}`, `/households/${h.id}`, '/account'];
  for (const p of paths) {
    await page.goto(p);
    await rtl();
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    const w = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
    expect.soft(w.sw, `overflow in RTL on ${p}`).toBeLessThanOrEqual(w.cw);
  }

  // Desktop: the sidebar sits at the inline start, so the right edge in RTL.
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('/');
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await expect(nav).toBeVisible();
  const ltrNav = (await nav.boundingBox())!;
  expect(ltrNav.x).toBe(0);
  await rtl();
  const rtlNav = (await nav.boundingBox())!;
  expect(rtlNav.x + rtlNav.width).toBeCloseTo(1280, 0);
  const main = (await page.locator('main').boundingBox())!;
  expect(main.x + main.width).toBeLessThanOrEqual(rtlNav.x + 1); // content does not sit under the sidebar
});
