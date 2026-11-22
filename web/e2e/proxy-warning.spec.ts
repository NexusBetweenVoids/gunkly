import { expect, test } from './helpers';

// The e2e server trusts 127.0.0.1 (every spec sends its own X-Forwarded-For), so the real
// "untrusted proxy" state cannot arise there; the Go tests cover the server side. Here the
// health response is mocked to check what the admin panel shows.
test('admin panel warns about an untrusted reverse proxy and offers the setting', async ({ world }) => {
  const admin = await world.newUser({ admin: true });
  const { page } = await admin.open();
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.route('**/api/v1/admin/health', (route) =>
    route.fulfill({
      json: {
        untrustedProxies: [
          { ip: '192.168.8.1', lastSeen: new Date().toISOString() },
          { ip: '2001:db8::5', lastSeen: new Date().toISOString() },
        ],
      },
    }),
  );
  await page.goto('/admin');
  const warn = page.getByRole('alert').filter({ hasText: 'Reverse proxy not trusted' });
  await expect(warn).toBeVisible();
  await expect(warn.getByTestId('proxy-setting')).toHaveText('GUNKLY_TRUSTED_PROXIES=192.168.8.1/32,2001:db8::5/128');
  await expect(warn.getByRole('link', { name: /reverse proxy/ })).toHaveAttribute('href', /#behind-a-reverse-proxy$/);
  await warn.getByRole('button', { name: 'Copy setting' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('GUNKLY_TRUSTED_PROXIES=192.168.8.1/32,2001:db8::5/128');
});

test('admin panel shows no proxy warning when every forwarding peer is trusted', async ({ world }) => {
  const admin = await world.newUser({ admin: true });
  const { page } = await admin.open();
  await page.goto('/admin');
  await expect(page.getByRole('heading', { name: 'Instance settings' })).toBeVisible();
  await expect(page.getByText('Reverse proxy not trusted')).toHaveCount(0);
});
