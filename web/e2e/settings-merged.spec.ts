import { axe, expect, headerItem, test } from './helpers';
import { IDP_CLIENT } from './idp';

test('one Settings screen: your groups, then the current household; old addresses move there', async ({ world }) => {
  const u = await world.newUser();
  const h1 = await world.household(u);
  const h2 = await world.household(u);
  const { page } = await u.open();
  await page.setViewportSize({ width: 1100, height: 900 });
  await page.goto('/settings');
  const nav = page.getByRole('navigation', { name: 'Settings' });
  await expect(nav.getByRole('heading', { name: 'You', exact: true })).toBeVisible();
  for (const g of ['Profile & email', 'Sign-in & security', 'AI', 'Integrations', 'Your data']) await expect(nav.getByRole('link', { name: g, exact: true })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'People', exact: true })).toBeVisible();
  await axe(page, 'settings');

  // An old household address picks that household and opens its group here.
  await page.goto(`/households/${h1.id}/people`);
  await expect(page).toHaveURL(/\/settings\/household-people$/);
  await expect(nav.getByRole('heading', { name: h1.name, exact: true })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Members' })).toBeVisible();
  await page.goto(`/households/${h2.id}`);
  await expect(page).toHaveURL(/\/settings\/household$/);
  await expect(nav.getByRole('heading', { name: h2.name, exact: true })).toBeVisible();
  // Switching household in the header switches the household part.
  await page.getByLabel('Household', { exact: true }).selectOption(h1.id);
  await expect(nav.getByRole('heading', { name: h1.name, exact: true })).toBeVisible();

  // Old account addresses keep their group (and query).
  await page.goto('/account/security');
  await expect(page).toHaveURL(/\/settings\/security$/);
  await expect(page.getByRole('region', { name: 'Password' })).toBeVisible();
  // Someone else's household is not found, not shown.
  await page.goto('/households/0190f0f0-0000-7000-8000-000000000000/people');
  await expect(page.getByText('Page not found')).toBeVisible();
});

test('settings without a household: only your groups', async ({ world }) => {
  const u = await world.newUser();
  const { page } = await u.open();
  await page.setViewportSize({ width: 1100, height: 900 });
  await page.goto('/settings/profile');
  const nav = page.getByRole('navigation', { name: 'Settings' });
  await expect(nav.getByRole('link', { name: 'Profile & email' })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'People', exact: true })).toHaveCount(0);
  // Without a household there is no tab bar, so the header links to Settings.
  await expect(await headerItem(page, 'link', 'Settings')).toBeVisible();
});

test('AI: explains what it does and how to set it up', async ({ world }) => {
  const u = await world.newUser();
  await world.household(u);
  const { page } = await u.open();
  await page.goto('/settings/ai');
  const hero = page.getByRole('region', { name: 'Use Gunkly with your AI assistant' });
  await expect(hero.getByText(/^Connect the AI assistant you already use/)).toBeVisible();
  await expect(hero.getByRole('heading', { name: 'How to set it up' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Connect an AI assistant' }).getByRole('button', { name: 'Connect an assistant' })).toBeVisible();
  await axe(page, 'ai settings');
});

test('Home Assistant: install and add buttons, and a prefilled key with the address', async ({ world, baseURL }) => {
  const u = await world.newUser();
  const h1 = await world.household(u);
  const h2 = await world.household(u);
  const { page } = await u.open();
  await page.goto('/settings/integrations');
  const card = page.getByRole('region', { name: 'Home Assistant' });
  await expect(card.getByRole('link', { name: 'Open in Home Assistant' })).toHaveAttribute(
    'href',
    'https://my.home-assistant.io/redirect/hacs_repository/?owner=NexusBetweenVoids&repository=gunkly-homeassistant&category=integration',
  );
  await expect(card.getByRole('listitem')).toHaveCount(3); // install, restart, key (adding is offered with the key)
  await axe(page, 'home assistant card');

  await card.getByRole('button', { name: 'Create a key for Home Assistant' }).click();
  const dialog = page.getByRole('dialog', { name: 'Create access token' });
  await expect(dialog.getByLabel('Name')).toHaveValue('Home Assistant');
  await expect(dialog.getByLabel(h1.name)).toBeChecked();
  await expect(dialog.getByLabel(h2.name)).toBeChecked();
  await expect(dialog.getByLabel('Add, change and delete chores and their reminders')).toBeChecked();
  await dialog.getByRole('button', { name: 'Create access token' }).click();
  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  await confirm.getByLabel('Password').fill(u.password);
  await confirm.getByRole('button', { name: 'Confirm' }).click();
  const shown = page.getByRole('dialog', { name: 'Your new access token' });
  await expect(shown.getByText(/^gk_/)).toBeVisible();
  await expect(shown.getByText(new URL(baseURL!).origin, { exact: true })).toBeVisible();
  await expect(shown.getByRole('link', { name: 'Add to Home Assistant' })).toHaveAttribute('href', 'https://my.home-assistant.io/redirect/config_flow_start/?domain=gunkly');
});

test('admin: single sign-on set in the environment is shown locked, never the secret', async ({ world }) => {
  const admin = await world.newUser({ admin: true });
  const { page } = await admin.open();
  await page.goto('/admin/signin');
  await expect(page.getByRole('heading', { name: 'Single sign-on (SSO)' })).toBeVisible();
  await expect(page.getByTestId('sso-status')).toContainText('On');
  // Set in the environment: a read-only list with one note naming the variables; the secret never.
  const card = page.getByRole('region', { name: 'Single sign-on (SSO)' });
  const locked = card.getByTestId('cfg-sso-locked');
  await expect(locked.getByText('Set in the server\'s environment (GUNKLY_OIDC_*). Change it there.')).toBeVisible();
  await expect(locked.getByText(IDP_CLIENT, { exact: true })).toBeVisible();
  await expect(locked.getByText('Set (hidden)')).toBeVisible();
  await expect(card.getByRole('textbox', { name: 'Client ID' })).toHaveCount(0);
  // Only the accounts settings (kept by Gunkly, not the environment) can be saved here.
  await expect(card.getByLabel('Create accounts for new single sign-on users')).toBeVisible();
  await expect(card.getByRole('button', { name: 'Save' })).toHaveCount(1);
  await expect(page.getByText(/\/api\/v1\/auth\/oidc\/callback$/)).toBeVisible();
});
