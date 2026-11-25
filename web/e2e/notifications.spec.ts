import { type Page } from '@playwright/test';
import { expect, mailMark, nextMail, PASSWORD, test, uniq } from './helpers';

/** Replaces the browser's subscribe call: records that the permission path ran, then fails like a denied prompt. */
async function stubSubscribe(page: Page): Promise<void> {
  await page.addInitScript(() => {
    (window as unknown as { __subscribed: number }).__subscribed = 0;
    Notification.requestPermission = async () => 'granted';
    PushManager.prototype.subscribe = () => {
      (window as unknown as { __subscribed: number }).__subscribed++;
      return Promise.reject(new DOMException('denied', 'NotAllowedError'));
    };
  });
}

/** Headless Chromium reports notifications as "denied"; a real browser starts at "default" (can ask). */
const askable = (page: Page) => page.addInitScript(() => Object.defineProperty(Notification, 'permission', { get: () => 'default' }));

/** Replaces subscribe with one that resolves a REAL-shaped PushSubscription (toJSON() includes expirationTime: null), as Chrome, Safari and Firefox do. */
async function stubRealSubscription(page: Page, endpoint: string): Promise<void> {
  await page.addInitScript((ep) => {
    const b64u = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    const auth = crypto.getRandomValues(new Uint8Array(16));
    let p256dh = new Uint8Array(65);
    const ready = crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']).then(async (k) => {
      p256dh = new Uint8Array(await crypto.subtle.exportKey('raw', k.publicKey)); // a real P-256 point, like the browser's
    });
    const sub = {
      endpoint: ep,
      expirationTime: null,
      options: {},
      toJSON: () => ({ endpoint: ep, expirationTime: null, keys: { p256dh: b64u(p256dh), auth: b64u(auth) } }),
      unsubscribe: async () => {
        current = null;
        return true;
      },
    };
    let current: typeof sub | null = null;
    (window as unknown as { __subscribed: number }).__subscribed = 0;
    Notification.requestPermission = async () => 'granted';
    PushManager.prototype.subscribe = async function (o?: PushSubscriptionOptionsInit) {
      if (!(o?.applicationServerKey instanceof Uint8Array) || o.applicationServerKey.length !== 65) throw new TypeError('bad applicationServerKey');
      (window as unknown as { __subscribed: number }).__subscribed++;
      await ready;
      current = sub;
      return sub as unknown as PushSubscription;
    };
    PushManager.prototype.getSubscription = async () => current as unknown as PushSubscription | null;
  }, endpoint);
}

const subscribeCalls = (page: Page) => page.evaluate(() => (window as unknown as { __subscribed: number }).__subscribed);


// Few, broad tests on purpose: every test signs in the shared admin, whose per-minute budget is small.

test('account: notifications are ordered this device, email, then messaging apps; each case explains itself', async ({ world }) => {
  const u = await world.newUser();
  const h = await world.household(u);
  const { page } = await u.open();
  await page.goto('/settings/notifications');
  const device = page.getByRole('region', { name: 'This device' });
  const email = page.getByRole('region', { name: 'Email me reminders' });
  await expect(device.getByRole('button', { name: 'Turn on reminders on this device' })).toBeVisible();
  await expect(device.getByText(/browser vendor's push service/)).toBeVisible();
  await expect(email).toBeVisible();
  const advanced = page.locator('details.advanced');
  await expect(advanced.locator('summary')).toHaveText('Messaging apps (advanced)');
  await expect(advanced).not.toHaveAttribute('open', '');
  await expect(page.getByRole('button', { name: 'Add channel' })).toBeHidden();
  const y = async (l: ReturnType<Page['locator']>) => (await l.boundingBox())!.y;
  expect(await y(device)).toBeLessThan(await y(email));
  expect(await y(email)).toBeLessThan(await y(advanced));
  await advanced.locator('summary').click();
  await expect(page.getByRole('button', { name: 'Add channel' })).toBeVisible();

  // A browser without the Push API gets a one-line explanation instead of a button.
  const plain = await u.open();
  await plain.page.addInitScript(() => {
    // @ts-expect-error simulate a browser without the Push API
    delete window.PushManager;
  });
  await plain.page.goto('/settings/notifications');
  const card = plain.page.getByRole('region', { name: 'This device' });
  await expect(card.getByTestId('push-unsupported')).toHaveText("This browser can't show reminders. Use email or a messaging app below instead.");
  await expect(card.getByRole('button')).toHaveCount(0);

  // On an iPhone in Safari the Home Screen steps are shown.
  const ios = await world.newContext({
    storageState: await u.api.storageState(),
    userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1',
  });
  const iosPage = await ios.newPage();
  await iosPage.addInitScript(() => {
    // @ts-expect-error Safari tabs have no Push API
    delete window.PushManager;
  });
  await iosPage.goto('/settings/notifications');
  await expect(iosPage.getByTestId('push-unsupported')).toContainText('Add to Home Screen');
  await expect(iosPage.getByTestId('push-unsupported')).toContainText('Safari');

  // Household settings explain what a household channel does.
  await page.goto(`/households/${h.id}/notifications`);
  await expect(page.getByRole('region', { name: 'Household notification channels' }).getByText(/family Telegram or Discord group/)).toBeVisible();
});

test('account: turning on reminders saves a real-shaped subscription (Apple and FCM endpoints) and shows it on', async ({ world }) => {
  const u = await world.newUser();
  for (const endpoint of [
    'https://web.push.apple.com/QGxPq3-_Zy9aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ',
    'https://fcm.googleapis.com/fcm/send/dUMMY123:APA91bHxYzAbCdEfGhIjKlMnOpQrStUvWxYz-_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN',
  ]) {
    const { page } = await u.open();
    await askable(page);
    await stubRealSubscription(page, endpoint);
    const saved = page.waitForResponse((r) => r.url().endsWith('/account/push-subscriptions') && r.request().method() === 'POST');
    await page.goto('/settings/notifications');
    const card = page.getByRole('region', { name: 'This device' });
    await card.getByRole('button', { name: 'Turn on reminders on this device' }).click();
    expect((await saved).status()).toBe(201);
    await expect(card.getByTestId('push-status')).toContainText('On:');
    await expect(card.getByRole('button', { name: 'Turn off reminders on this device' })).toBeVisible();
    await expect(card.getByRole('alert')).toHaveCount(0);
  }
});

test('account: the email reminders switch needs a confirmed address and then works', async ({ world }) => {
  const name = uniq('mailer');
  const email = `${name}@example.test`;
  const mark = mailMark();
  await world.admin.post('/admin/users', { username: name, password: PASSWORD, displayName: 'Mail Person', email });
  const api = await world.signIn(name, PASSWORD);
  const sent = await nextMail(email, /Confirm your email address/, mark);
  const ctx = await world.newContext({ storageState: await api.storageState() });
  const page = await ctx.newPage();
  await page.goto('/settings/notifications');
  const card = page.getByRole('region', { name: 'Email me reminders' });
  await expect(card.getByTestId('email-reminders-hint')).toContainText('add your email address and confirm it first');
  await expect(card.getByRole('checkbox')).toHaveCount(0);

  await api.post('/account/email/verify', { token: new URL(sent.link, 'http://x').searchParams.get('token') });
  await page.reload();
  const box = page.getByRole('region', { name: 'Email me reminders' }).getByRole('checkbox', { name: 'Email me reminders' });
  await expect(box).not.toBeChecked();
  await box.check();
  await expect(box).toBeChecked();
  await expect.poll(async () => (await api.get<{ emailReminders: boolean }>('/me')).emailReminders).toBe(true);
  await page.reload();
  await expect(page.getByRole('checkbox', { name: 'Email me reminders' })).toBeChecked();
  await page.getByRole('checkbox', { name: 'Email me reminders' }).uncheck();
  await expect.poll(async () => (await api.get<{ emailReminders: boolean }>('/me')).emailReminders).toBe(false);
});

test('home: the reminder offer asks once; "Not now" hides it for good on this device', async ({ world }) => {
  const u = await world.newUser();
  await world.household(u);
  const { page } = await u.open();
  await askable(page);
  await page.goto('/');
  const card = page.getByTestId('push-prompt');
  await expect(card.getByRole('heading', { name: 'Get reminders on this device?' })).toBeVisible();
  await card.getByRole('button', { name: 'Not now' }).click();
  await expect(card).toBeHidden();
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Chores' })).toBeVisible();
  await page.waitForLoadState('networkidle');
  await expect(page.getByTestId('push-prompt')).toHaveCount(0);

  // Another device (fresh storage): "Turn on" asks the browser for permission and explains a refusal.
  const other = await u.open();
  await askable(other.page);
  await stubSubscribe(other.page);
  await other.page.goto('/');
  const card2 = other.page.getByTestId('push-prompt');
  await card2.getByRole('button', { name: 'Turn on' }).click();
  await expect.poll(() => subscribeCalls(other.page)).toBe(1);
  await expect(card2.getByRole('alert')).toContainText(/Could not turn on notifications|blocked/);
  await expect(card2.getByRole('button', { name: 'Not now' })).toBeVisible(); // still dismissible

  // A device where the browser has blocked notifications is never asked.
  const blocked = await u.open();
  await blocked.page.addInitScript(() => Object.defineProperty(Notification, 'permission', { get: () => 'denied' }));
  await blocked.page.goto('/');
  await expect(blocked.page.getByRole('heading', { name: 'Chores' })).toBeVisible();
  await blocked.page.waitForLoadState('networkidle');
  await expect(blocked.page.getByTestId('push-prompt')).toHaveCount(0);
});

test('home: no offer for guests, or for anyone who already has another way to get reminders', async ({ world }) => {
  const owner = await world.newUser();
  const h = await world.household(owner);
  const guest = await world.newUser();
  const inv = await owner.api.post<{ token: string }>(`/households/${h.id}/invites`, { role: 'guest', expiresInHours: 24 });
  await guest.api.post(`/invites/${inv.token}/accept`);
  const g = await guest.open();
  await askable(g.page);
  await g.page.goto('/');
  await expect(g.page.getByRole('heading', { name: 'Chores' })).toBeVisible();
  await g.page.waitForLoadState('networkidle');
  await expect(g.page.getByTestId('push-prompt')).toHaveCount(0);

  // The owner is offered it, until a messaging channel exists.
  const o = await owner.open();
  await askable(o.page);
  await o.page.goto('/');
  await expect(o.page.getByTestId('push-prompt')).toBeVisible();
  await owner.api.post('/account/channels', { url: 'ntfy://ntfy.sh/gunkly-e2e-topic', label: 'Phone' });
  const c = await owner.open();
  await askable(c.page);
  await c.page.goto('/');
  await expect(c.page.getByRole('heading', { name: 'Chores' })).toBeVisible();
  await c.page.waitForLoadState('networkidle');
  await expect(c.page.getByTestId('push-prompt')).toHaveCount(0);
});

test('admin: release notes show as formatted text, never as HTML, with only http(s) links', async ({ world }) => {
  const admin = await world.newUser({ admin: true });
  const { page } = await admin.open();
  const notes = [
    '### Changed',
    '',
    '- A **bold** change with `code` and [the docs](https://gunkly.app/docs/)',
    '  - a nested point',
    '- <img src=x onerror="window.__pwned=1"><script>window.__pwned=2</script>',
    '- [click](javascript:window.__pwned=3) and [data](data:text/html,x)',
  ].join('\n');
  // The e2e server never calls out, so the API answer is stubbed with these notes.
  await page.route('**/api/v1/admin/updates', (r) =>
    r.fulfill({ json: { running: '0.5.4', latest: '9.9.9', newer: true, ahead: false, checking: false, changelog: notes, checkedAt: null, enabled: true } }),
  );
  await page.goto('/admin/updates');
  const log = page.getByTestId('changelog');
  await expect(log.getByRole('heading', { name: 'Changed' })).toBeVisible();
  await expect(log.locator('strong')).toHaveText('bold');
  await expect(log.locator('code')).toHaveText('code');
  await expect(log.locator('ul ul li')).toHaveText('a nested point');
  const links = log.getByRole('link');
  await expect(links).toHaveCount(1);
  await expect(links).toHaveAttribute('href', 'https://gunkly.app/docs/');
  await expect(links).toHaveAttribute('rel', 'noopener noreferrer nofollow');
  await expect(links).toHaveAttribute('target', '_blank');
  // Raw HTML and unsafe links are shown as text; nothing ran and no element was created from them.
  await expect(log.getByText('<script>window.__pwned=2</script>', { exact: false })).toBeVisible();
  await expect(log.getByText('[click](javascript:window.__pwned=3)', { exact: false })).toBeVisible();
  await expect(log.locator('img, script, iframe')).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();
});

test('admin: the Updates section shows the running version and the check can be switched off', async ({ world }) => {
  const admin = await world.newUser({ admin: true });
  await admin.api.call('POST', '/session/step-up', { password: admin.password }, 204);
  const { page } = await admin.open();
  await page.goto('/admin/updates');
  const card = page.getByRole('region', { name: 'Updates' });
  await expect(card.getByText(/^Running version: /)).toBeVisible();
  // The e2e server has the check off: the button is there but disabled, with an explanation.
  await expect(card.getByRole('button', { name: 'Check now' })).toBeDisabled();
  await expect(card.getByText('Turn on the background check to look for updates.')).toBeVisible();
  const box = card.getByRole('checkbox', { name: 'Check for updates' });
  try {
    await box.check();
    await expect.poll(async () => (await admin.api.get<{ enabled: boolean }>('/admin/updates')).enabled).toBe(true);
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { updateCheck: false }); // the e2e server must never call out
  }
  await page.reload();
  await expect(card.getByRole('checkbox', { name: 'Check for updates' })).not.toBeChecked();
});
