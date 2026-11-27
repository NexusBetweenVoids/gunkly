import { expect, test } from './helpers';

// A browser in another language gets the app in that language (translations load on demand).
for (const [lang, signIn] of [['nl', 'Inloggen'], ['de', 'Anmelden'], ['fr', 'Se connecter'], ['es', 'Iniciar sesión']] as const) {
  test(`sign-in page in ${lang}`, async ({ world }) => {
    const page = await (await world.newContext({ locale: lang })).newPage();
    await page.goto('/signin');
    await expect(page.getByRole('heading', { name: signIn })).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('lang', lang);
  });
}

// The language chosen in Settings, Profile wins over the browser's, survives a reload, and
// "follow the browser" goes back.
test('profile language setting changes the app language', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.goto('/settings/profile');
  await page.getByLabel('Language').selectOption('nl');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('heading', { name: 'Profiel', exact: true }).first()).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'nl');
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Instellingen', exact: true })).toBeVisible();
  await page.getByLabel('Taal').selectOption('');
  await page.getByRole('button', { name: 'Opslaan' }).click();
  await expect(page.getByRole('heading', { name: 'Profile', exact: true }).first()).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
});
