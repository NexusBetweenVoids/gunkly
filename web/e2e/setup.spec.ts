import { expect, test } from '@playwright/test';
import { PASSWORD, readState, uniq, writeState } from './helpers';

// Runs first, once: the first-run flow with the setup code from the server log.
test('first-run setup creates the admin and signs in', async ({ page }) => {
  const { setupCode } = readState();
  const username = uniq('admin');
  await page.goto('/');
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole('heading', { name: 'Set up Gunkly' })).toBeVisible();

  await page.getByLabel('Setup code').fill('wrong-code');
  await page.getByLabel('Display name').fill('Admin Person');
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByRole('button', { name: 'Create administrator account' }).click();
  await expect(page.getByText("That setup code isn't correct.")).toBeVisible();

  await page.getByLabel('Setup code').fill(setupCode);
  await page.getByRole('button', { name: 'Create administrator account' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('link', { name: 'Admin' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Chores' })).toBeVisible();

  writeState({ setupCode, admin: { username, password: PASSWORD } });

  // The admin is really signed in: a reload keeps the session.
  await page.reload();
  await expect(page.getByRole('link', { name: 'Admin' })).toBeVisible();
});
