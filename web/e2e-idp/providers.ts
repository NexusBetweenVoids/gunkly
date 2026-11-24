import { type Page, type Request, expect } from '@playwright/test';

// Per-provider facts and login-page drivers. Versions are pinned in test/idp/*/compose.yml so these
// selectors only change when someone bumps an image on purpose. Test-only credentials (see test/idp/README.md).
export const PASSWORD = 'ci-only-password-1';
export const USERS = {
  sso: { username: 'ci-sso-user', displayName: 'Ci Sso' },
  link: { username: 'ci-link-user', displayName: 'Ci Link' },
};
export type Who = (typeof USERS)['sso'];

const kind = process.env.IDP_PROVIDER;
if (kind !== 'keycloak' && kind !== 'authentik') throw new Error('IDP_PROVIDER must be keycloak or authentik');

/** The button label on Gunkly's sign-in page (GUNKLY_OIDC_NAME). */
export const NAME = kind === 'keycloak' ? 'Keycloak' : 'authentik';

/**
 * Runs `trigger` (a click that sends the browser to the provider), signs in at the provider if it asks
 * for a login, and returns once the browser is back on Gunkly. `prompted` tells whether the provider
 * showed its login form (false when its own session signed us in silently).
 */
export async function throughProvider(page: Page, gunkly: string, trigger: () => Promise<void>, who: Who): Promise<{ prompted: boolean }> {
  let callbackSeen = false;
  const onRequest = (r: Request): void => {
    if (r.isNavigationRequest() && r.url().startsWith(`${gunkly}/api/v1/auth/oidc/callback`)) callbackSeen = true;
  };
  page.on('request', onRequest);
  try {
    await trigger();
    const first = kind === 'keycloak' ? page.locator('#username') : page.locator('input[name="uidField"]');
    const password = kind === 'keycloak' ? page.locator('#password') : page.locator('input[name="password"]');
    let prompted = false;
    const deadline = Date.now() + 60_000;
    let left = false; // the browser has been on the provider
    for (;;) {
      // page.url() can lag behind (and stay stale) after a redirect chain, so read the live location;
      // done once back on a Gunkly app page after the callback or after a visit to the provider.
      const live = await page.evaluate(() => location.href).catch(() => page.url());
      const here = new URL(live);
      if (here.origin !== gunkly) left = true;
      if ((callbackSeen || left) && here.origin === gunkly && !here.pathname.startsWith('/api/') && !here.pathname.startsWith('/signin')) {
        return { prompted };
      }
      // Keycloak asks only for the password when a session exists and the login is forced (prompt=login).
      const askedFor = kind === 'keycloak' ? password : first;
      if (!prompted && (await askedFor.isVisible().catch(() => false))) {
        prompted = true;
        if (await first.isVisible().catch(() => false)) await first.fill(who.username);
        if (kind === 'keycloak') {
          await password.fill(PASSWORD);
          await page.locator('#kc-login').click();
        } else {
          await page.locator('button[type="submit"]').click();
          await password.waitFor({ state: 'visible', timeout: 30_000 });
          await page.waitForTimeout(1000); // the flow executor re-renders the stage once; fill after that
          await password.fill(PASSWORD);
          await expect(password).toHaveValue(PASSWORD);
          await page.locator('button[type="submit"]').click();
        }
      }
      if (Date.now() > deadline) throw new Error(`still at ${live} after 60s`);
      await page.waitForTimeout(200);
    }
  } finally {
    page.off('request', onRequest);
  }
}
