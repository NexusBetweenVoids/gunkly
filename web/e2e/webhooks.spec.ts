import { createHmac } from 'node:crypto';
import { createServer, type IncomingMessage } from 'node:http';
import type { AddressInfo } from 'node:net';
import { axe, expect, test } from './helpers';

// Outgoing webhooks (ADR 0017): off by default; an admin turns them on; an owner adds one, sees the
// secret once, sends a test and finds the signed delivery in the log. The e2e instance has the egress
// guard off (helpers.ts), so a receiver on loopback over http is allowed.
test('webhooks: admin enables, owner adds one, secret shown once, test is delivered and signed', async ({ world }) => {
  test.setTimeout(100_000); // a real event waits for the scheduler's next tick (30 s)
  const received: { headers: IncomingMessage['headers']; body: string }[] = [];
  const receiver = createServer((req, res) => {
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => {
      received.push({ headers: req.headers, body });
      res.writeHead(204).end();
    });
  }).listen(0, '127.0.0.1');
  await new Promise((r) => receiver.once('listening', r));
  const target = `http://127.0.0.1:${(receiver.address() as AddressInfo).port}/hook`;
  try {
    const owner = await world.newUser();
    const h = await world.household(owner);
    const { page } = await owner.open();

    // Off by default: nothing is offered and the routes do not exist.
    await page.goto(`/households/${h.id}`);
    await expect(page.getByRole('heading', { name: 'Households', level: 2 })).toBeVisible();
    await expect(page.getByText('Webhooks (advanced)')).toHaveCount(0);
    expect((await owner.api.ctx.get(`/api/v1/households/${h.id}/webhooks`)).status()).toBe(404);

    // The admin turns it on in Administration.
    const admin = await world.newUser({ admin: true });
    const ap = (await admin.open()).page;
    await ap.goto('/admin');
    const settings = ap.getByRole('region', { name: 'Instance settings' });
    const box = settings.getByLabel('Let household owners add outgoing webhooks');
    await expect(box).not.toBeChecked();
    await box.check();
    await settings.getByRole('button', { name: 'Save' }).click();
    const confirm = ap.getByRole('dialog', { name: "Confirm it's you" });
    await confirm.waitFor();
    await confirm.getByLabel('Password').fill(admin.password);
    await confirm.getByRole('button', { name: 'Confirm' }).click();
    await expect(ap.getByText('Saved.')).toBeVisible();

    // The owner finds the collapsed section and adds a webhook.
    await page.reload();
    const section = page.locator('details', { hasText: 'Webhooks (advanced)' }).first();
    await section.getByText('Webhooks (advanced)').first().click();
    await expect(section.getByText('No webhooks yet.')).toBeVisible();
    await section.getByLabel('Web address').fill(target);
    await section.getByLabel('Description (optional)').fill('Automation');
    await section.getByLabel('completion undone').uncheck();
    await axe(page, 'webhooks section');
    await section.getByRole('button', { name: 'Add webhook' }).click();
    const confirm2 = page.getByRole('dialog', { name: "Confirm it's you" });
    await confirm2.waitFor();
    await confirm2.getByLabel('Password').fill(owner.password);
    await confirm2.getByRole('button', { name: 'Confirm' }).click();

    // The secret is shown once, with a copy button and a verification hint.
    const secretLine = section.getByText(/^whsec_/);
    await expect(secretLine).toBeVisible();
    const secret = (await secretLine.textContent())!.trim();
    expect(secret).toMatch(/^whsec_[\w-]{43}$/);
    await expect(section.getByText(/can not be shown again/)).toBeVisible();
    await expect(section.getByRole('button', { name: 'Copy' })).toBeVisible();
    await section.getByText('How to check a message').click();
    await expect(section.getByText(/openssl dgst -sha256 -hmac/)).toBeVisible();
    await section.getByRole('button', { name: 'I have saved it' }).click();
    const row = section.getByRole('listitem').filter({ hasText: 'Automation' }).first();
    await expect(row).toContainText('127.0.0.1');
    await expect(section).not.toContainText(secret);
    await page.reload(); // the address and the secret never come back
    await page.locator('details', { hasText: 'Webhooks (advanced)' }).first().getByText('Webhooks (advanced)').first().click();
    await expect(page.locator('body')).not.toContainText(secret);
    await expect(page.locator('body')).not.toContainText('/hook');

    // Send a test: the receiver gets a signed ping and the log shows the delivery.
    const sec = page.locator('details', { hasText: 'Webhooks (advanced)' }).first();
    await sec.getByRole('button', { name: 'Send test' }).click();
    await expect(page.getByText('Test sent (HTTP 204).')).toBeVisible();
    await expect.poll(() => received.length).toBe(1);
    const got = received[0]!;
    expect(got.headers['gunkly-event']).toBe('ping');
    expect(got.headers['user-agent']).toMatch(/^Gunkly-Webhook\//);
    const m = /^t=(\d+),v1=([0-9a-f]{64})$/.exec(String(got.headers['gunkly-signature']))!;
    expect(m).not.toBeNull();
    expect(createHmac('sha256', secret).update(`${m[1]}.${got.body}`).digest('hex')).toBe(m[2]);
    expect(JSON.parse(got.body)).toMatchObject({ type: 'ping', version: 1, householdId: h.id });
    await expect(sec.getByRole('button', { name: 'Log' })).toHaveAttribute('aria-expanded', 'true');
    await expect(sec.getByText('HTTP 204')).toBeVisible();

    // A real event: completing a chore is delivered too.
    const chore = await world.chore(owner, h);
    await owner.api.post(`/households/${h.id}/chores/${chore.id}/complete`, {});
    // chore.created and chore.completed arrive together on the scheduler's next tick (every 30 s).
    await expect.poll(() => received.map((r) => r.headers['gunkly-event']), { timeout: 40_000 }).toEqual(expect.arrayContaining(['chore.created', 'chore.completed']));

    // Turn it off and delete it.
    await sec.getByRole('button', { name: 'Turn off' }).click();
    await expect(sec.getByText('Off', { exact: true })).toBeVisible();
    await sec.getByRole('button', { name: 'Delete' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click();
    await expect(sec.getByText('No webhooks yet.')).toBeVisible();
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { webhooksEnabled: false }); // leave the shared instance as it was
    receiver.close();
  }
});
