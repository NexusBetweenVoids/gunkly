import { expect, setTheme, test, ymd } from './helpers';

// README and website screenshots with synthetic demo data (Alex, Sam, "Maple Street 12").
// Skipped in normal runs; `npm run screenshots` writes docs/assets/screens/*.png.
// Run it before every release; the website copies these files (`npm run sync` there).
test.skip(!process.env.GUNKLY_SCREENSHOTS, 'set GUNKLY_SCREENSHOTS=1 (npm run screenshots)');
const OUT = '../docs/assets/screens/';
test('README and website screenshots', async ({ world, browser }) => {
  test.setTimeout(120_000);
  const alex = await world.newUser();
  await alex.api.call('PATCH', '/me', { displayName: 'Alex' });
  const sam = await world.newUser();
  await sam.api.call('PATCH', '/me', { displayName: 'Sam' });
  const h = await world.household(alex, 'Maple Street 12', { areas: true });
  const inv = await alex.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'member', expiresInHours: 24 });
  await sam.api.post(`/invites/${inv.token}/accept`);
  await alex.api.post(`/households/${h.id}/invites`, { role: 'guest', expiresInHours: 72 });
  const members = await alex.api.get<{ userId: string; displayName: string }[]>(`/households/${h.id}/members`);
  const A = members.find((m) => m.displayName === 'Alex')!.userId;
  const S = members.find((m) => m.displayName === 'Sam')!.userId;
  const areas = await alex.api.get<{ id: string; name: string }[]>(`/households/${h.id}/areas`);
  const area = (n: string) => areas.find((a) => a.name === n)?.id ?? null;
  const iv = (every: number, unit: string, due: number) => ({ type: 'interval', interval: { every, unit }, anchor: 'from_completion', firstDue: ymd(due) });
  const chores: [string, string, string[], ReturnType<typeof iv>, boolean?][] = [
    ['Descale the kettle', 'Kitchen', [A], iv(1, 'month', -3)],
    ['Washing-machine filter', 'Appliances', [S], iv(3, 'month', -1)],
    ['Wash the bedding', 'Bathroom', [S], iv(2, 'week', -1)],
    ['Clean the gutters', 'Exterior', [A, S], iv(6, 'month', 0), true],
    ['Water the plants', 'Garden', [S], iv(4, 'day', 0)],
    ['Defrost the freezer', 'Kitchen', [A], iv(6, 'month', 0)],
    ['Take out the recycling', 'General', [A, S], iv(1, 'week', 3), true],
    ['Vacuum the stairs', 'General', [A], iv(1, 'week', 5)],
    ['Clean the oven', 'Kitchen', [S], iv(2, 'month', 8)],
    ['Test smoke alarms', 'General', [A], iv(6, 'month', 12)],
  ];
  let gutters = '';
  for (const [name, a, who, schedule, rotation] of chores) {
    const c = await world.chore(alex, h, { name, areaId: area(a), assignees: who, schedule, ...(rotation ? { rotation: true } : {}) });
    if (name === 'Clean the gutters') gutters = c.id;
  }

  const shot = async (path: string, file: string, theme: 'light' | 'dark', viewport: { width: number; height: number }, scale: number, ready: string | RegExp) => {
    const context = await browser.newContext({ storageState: await alex.api.storageState(), viewport, deviceScaleFactor: scale, colorScheme: theme, baseURL: world.baseURL });
    const page = await context.newPage();
    await page.goto(path);
    await expect(page.getByText(ready).first()).toBeVisible();
    await setTheme(page, theme);
    await page.waitForTimeout(400);
    await page.screenshot({ path: OUT + file });
    await context.close();
  };
  const phone = { width: 360, height: 1015 };
  await shot('/', 'home-phone-light.png', 'light', phone, 1.5, 'Descale the kettle');
  await shot('/', 'home-phone-dark.png', 'dark', phone, 1.5, 'Descale the kettle');
  await shot('/calendar', 'calendar-desktop-dark.png', 'dark', { width: 1100, height: 760 }, 1, 'Subscribe from your own calendar app');
  await shot(`/chores/${gutters}/edit`, 'chore-editor-desktop-light.png', 'light', { width: 1100, height: 900 }, 1, 'Take turns');
  await shot(`/households/${h.id}/people`, 'household-desktop-light.png', 'light', { width: 1000, height: 830 }, 1, /^Invites$/);
});
