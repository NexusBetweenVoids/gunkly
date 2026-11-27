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
