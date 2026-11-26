import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, PASSWORD, test, ymd } from './helpers';

// A stand-in for an OpenAI-compatible endpoint on loopback (the e2e server never calls out).
function fakeModel(reply: () => string): Promise<{ url: string; server: Server; bodies: string[] }> {
  const bodies: string[] = [];
  const server = createServer((req, res) => {
    let b = '';
    req.on('data', (c) => (b += c));
    req.on('end', () => {
      bodies.push(b);
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: reply() } }] }));
    });
  });
  return new Promise((ok) => server.listen(0, '127.0.0.1', () => ok({ url: `http://127.0.0.1:${(server.address() as AddressInfo).port}/v1`, server, bodies })));
}

test('AI chore suggestions: the admin sets up an endpoint, a member reviews and adds suggestions', async ({ world }) => {
  const reply = JSON.stringify({
    chores: [
      { name: 'Vacuum the living room', every: 1, unit: 'week', weekdays: ['SA'], firstDue: ymd(0), assignees: [], time: '10:00' },
      { name: 'Descale the kettle', every: 3, unit: 'month', firstDue: ymd(0), assignees: [] },
    ],
  });
  const model = await fakeModel(() => reply);
  const admin = await world.newUser({ admin: true });
  try {
    await admin.api.call('POST', '/session/step-up', { password: PASSWORD }, 204);
    const { page } = await admin.open();
    await page.goto('/admin/ai');
    const card = page.getByRole('region', { name: 'AI chore suggestions' });
    await card.getByLabel('Turn on AI chore suggestions').check();
    await card.getByLabel('API address').fill(model.url);
    await card.getByLabel('Model').fill('test-model');
    await card.getByLabel('API key (optional)').fill('sk-e2e-secret');
    await card.getByRole('button', { name: 'Save' }).click();
    await expect(card.getByText('Saved (hidden)')).toBeVisible(); // the key is never shown again
    await card.getByRole('button', { name: 'Test connection' }).click();
    await expect(card.getByRole('status')).toContainText('It works');

    const member = await world.newUser();
    await world.household(member);
    const m = await member.open();
    await m.page.goto('/chores/new');
    await m.page.getByRole('button', { name: 'Describe with AI' }).click();
    await m.page.getByLabel('What needs doing?').fill('Vacuum the living room on Saturdays at 10, descale the kettle every 3 months');
    await m.page.getByRole('button', { name: 'Suggest chores' }).click();
    const box = m.page.getByTestId('ai-suggest');
    await expect(box.getByText('Vacuum the living room')).toBeVisible();
    await expect(box.getByText('Descale the kettle')).toBeVisible();
    // Add one as it is.
    await box.getByRole('listitem').filter({ hasText: 'Descale the kettle' }).getByRole('button', { name: 'Add' }).click();
    await expect(box.getByRole('listitem').filter({ hasText: 'Descale the kettle' }).getByText('Added')).toBeVisible();
    // Open the other in the form, change it, save.
    await box.getByRole('listitem').filter({ hasText: 'Vacuum the living room' }).getByRole('button', { name: 'Edit' }).click();
    await expect(m.page.getByRole('dialog')).toHaveCount(0);
    await expect(m.page.getByLabel('Name', { exact: true })).toHaveValue('Vacuum the living room');
    await expect(m.page.getByRole('button', { name: /^Repeat: Every week/ })).toBeVisible();
    await m.page.getByLabel('Name', { exact: true }).fill('Vacuum downstairs');
    await m.page.getByRole('button', { name: 'Save' }).click();
    await expect(m.page.getByRole('heading', { name: 'Vacuum downstairs' })).toBeVisible();

    const hh = (await member.api.get<{ id: string }[]>('/households'))[0];
    const names = (await member.api.get<{ name: string }[]>(`/households/${hh.id}/chores`)).map((c) => c.name).sort();
    expect(names).toEqual(['Descale the kettle', 'Vacuum downstairs']);
    // The prompt carried the text, not the key; the key went in the header only.
    expect(model.bodies.at(-1)).toContain('descale the kettle every 3 months');
    expect(model.bodies.join('')).not.toContain('sk-e2e-secret');
  } finally {
    await admin.api.call('PATCH', '/admin/config', { values: { 'ai.enabled': null, 'ai.url': null, 'ai.model': null, 'ai.key': null } });
    model.server.close();
  }
});
