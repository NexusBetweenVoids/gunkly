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
const IDP_ORIGIN = kind === 'keycloak' ? 'http://localhost:8080' : 'http://localhost:9000';

/**
 * Runs `trigger` (a click that sends the browser to the provider), signs in at the provider if it asks
 * for a login, and returns once the browser is back on Gunkly. `prompted` tells whether the provider
 * showed its login form (false when its own session signed us in silently).
 */
export async function throughProvider(page: Page, gunkly: string, trigger: () => Promise<void>, who: Who): Promise<{ prompted: boolean }> {
  let seenProvider = false;
  const onRequest = (r: Request): void => {
    if (r.isNavigationRequest() && r.url().startsWith(IDP_ORIGIN)) seenProvider = true;
  };
  page.on('request', onRequest);
  try {
    await trigger();
    const first = kind === 'keycloak' ? page.locator('#username') : page.locator('input[name="uidField"]');
    const password = kind === 'keycloak' ? page.locator('#password') : page.locator('input[name="password"]');
    let prompted = false;
    const deadline = Date.now() + 60_000;
    for (;;) {
      if (seenProvider && new URL(page.url()).origin === gunkly) return { prompted };
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
      if (Date.now() > deadline) throw new Error(`still at ${page.url()} after 60s`);
      await page.waitForTimeout(200);
    }
  } finally {
    page.off('request', onRequest);
  }
}
