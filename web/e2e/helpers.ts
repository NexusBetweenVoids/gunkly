import { createHmac, randomBytes } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import AxeBuilder from '@axe-core/playwright';
import {
  type APIRequestContext,
  type Browser,
  type BrowserContext,
  type Page,
  expect,
  request,
  test as base,
} from '@playwright/test';
import { mailFile, stateFile } from './global-setup';
import { decodeBody, readMails } from './smtp';

export const ZONE = 'Europe/Berlin';
export const PASSWORD = 'correct horse battery staple';

type State = { setupCode: string; admin?: { username: string; password: string } };
export const readState = (): State => JSON.parse(readFileSync(stateFile, 'utf8'));
export const writeState = (s: State): void => writeFileSync(stateFile, JSON.stringify(s));

let ipCounter = Math.floor(Math.random() * 200) + 1;
/** A unique fake client IP per simulated device (the server trusts X-Forwarded-For from loopback in e2e). */
export const nextIP = (): string => `10.${Math.floor(++ipCounter / 250) % 250}.${ipCounter % 250}.${1 + Math.floor(Math.random() * 200)}`;
export const uniq = (p = 'u'): string => `${p}${randomBytes(4).toString('hex')}`;

/** YYYY-MM-DD in the household zone, `plus` days from today. */
export function ymd(plus = 0): string {
  const d = new Date(Date.now() + plus * 86_400_000);
  return new Intl.DateTimeFormat('en-CA', { timeZone: ZONE }).format(d);
}

export class Api {
  constructor(readonly ctx: APIRequestContext) {}
  private async csrf(): Promise<string> {
    let c = (await this.ctx.storageState()).cookies.find((x) => x.name === 'gunkly_csrf');
    if (!c) {
      await this.ctx.get('/api/v1/session');
      c = (await this.ctx.storageState()).cookies.find((x) => x.name === 'gunkly_csrf');
    }
    return decodeURIComponent(c?.value ?? '');
  }
  async call<T = unknown>(method: string, path: string, data?: unknown, status?: number): Promise<T> {
    const res = await this.ctx.fetch(`/api/v1${path}`, {
      method,
      data,
      headers: method === 'GET' ? {} : { 'X-CSRF-Token': await this.csrf() },
    });
    if (status ? res.status() !== status : !res.ok()) {
      throw new Error(`${method} ${path} -> ${res.status()} ${await res.text()}`);
    }
    return (res.status() === 204 ? undefined : await res.json()) as T;
  }
  get = <T>(p: string) => this.call<T>('GET', p);
  post = <T>(p: string, d?: unknown) => this.call<T>('POST', p, d ?? {});
  storageState = () => this.ctx.storageState();
}

/** Number of emails sent so far; pass it to nextMail as `after` to wait for a new one. */
export const mailMark = (): number => readMails(mailFile).length;

export type Sent = { to: string; subject: string; body: string; link: string; code: string };

/** Waits for an email to `to` whose subject matches, sent after `after` (from mailMark). */
export async function nextMail(to: string, subject: RegExp, after: number): Promise<Sent> {
  const deadline = Date.now() + 10_000;
  for (;;) {
    for (const m of readMails(mailFile).slice(after)) {
      const subj = /^Subject: (.*)$/m.exec(m.data)?.[1] ?? '';
      if (!m.to.includes(to) || !subject.test(subj)) continue;
      const body = decodeBody(m.data);
      const url = /https?:\/\/\S+/.exec(body)?.[0];
      return {
        to,
        subject: subj,
        body,
        link: url ? new URL(url).pathname + new URL(url).search : '',
        code: /code is (\d{6})/.exec(body)?.[1] ?? '',
      };
    }
    if (Date.now() > deadline) throw new Error(`no email to ${to} matching ${subject} (${readMails(mailFile).length - after} new emails)`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

export type Chore = { id: string; name: string; status: string };
export type Household = { id: string; name: string };

export type User = {
  username: string;
  password: string;
  displayName: string;
  api: Api;
  /** A browser context signed in as this user (shares the API session). */
  open: () => Promise<{ context: BrowserContext; page: Page }>;
};

export type World = {
  baseURL: string;
  admin: Api;
  signIn: (username: string, password: string) => Promise<Api>;
  newUser: (opts?: { admin?: boolean }) => Promise<User>;
  /** A fresh client with no session, with its own IP, tracked for CSP violations. */
  newContext: (opts?: Parameters<Browser['newContext']>[0]) => Promise<BrowserContext>;
  household: (u: User, name?: string) => Promise<Household>;
  chore: (u: User, h: Household, over?: Record<string, unknown>) => Promise<Chore>;
};

const CSP_LISTENER = `addEventListener('securitypolicyviolation', (e) =>
  console.error('Content Security Policy violation: ' + e.violatedDirective + ' ' + e.blockedURI));`;

function track(ctx: BrowserContext, bad: string[]): void {
  void ctx.addInitScript(CSP_LISTENER);
  ctx.on('console', (m) => {
    if (m.text().includes('Content Security Policy')) bad.push(m.text());
  });
}

let instanceReady = false;

export const test = base.extend<{ world: World; csp: string[] }>({
  csp: [
    async ({ context }, use) => {
      const bad: string[] = [];
      track(context, bad);
      await use(bad);
      expect(bad, 'Content Security Policy violations').toEqual([]);
    },
    { auto: true },
  ],
  world: async ({ browser, baseURL, csp }, use) => {
    const url = baseURL!;
    const owned: (() => Promise<void>)[] = [];
    const client = async () => {
      const ctx = await request.newContext({ baseURL: url, extraHTTPHeaders: { 'X-Forwarded-For': nextIP() } });
      owned.push(() => ctx.dispose());
      return new Api(ctx);
    };
    const signIn = async (username: string, password: string) => {
      const a = await client();
      await a.post('/session', { login: username, password });
      return a;
    };
    const { admin: creds } = readState();
    if (!creds) throw new Error('setup.spec.ts must run first');
    const admin = await signIn(creds.username, creds.password);
    await admin.call('POST', '/session/step-up', { password: creds.password }, 204);
    // The e2e SMTP server listens on loopback, which the egress guard blocks by default.
    // Once per worker: the shared admin has a per-user write budget that every extra call eats into.
    if (!instanceReady) {
      await admin.call('PATCH', '/admin/settings', { egressGuard: false, updateCheck: false }); // and it must never ask GitHub for releases
      instanceReady = true;
    }

    const newContext: World['newContext'] = async (opts = {}) => {
      const headers = { 'X-Forwarded-For': nextIP(), ...(opts.extraHTTPHeaders ?? {}) };
      const ctx = await browser.newContext({ baseURL: url, timezoneId: ZONE, locale: 'en-US', ...opts, extraHTTPHeaders: headers });
      track(ctx, csp);
      owned.push(() => ctx.close());
      return ctx;
    };
    const w: World = {
      baseURL: url,
      admin,
      signIn,
      newContext,
      async newUser(opts = {}) {
        const username = uniq('user');
        const displayName = `Test ${username}`;
        await admin.post('/admin/users', { username, password: PASSWORD, displayName, isAdmin: !!opts.admin });
        const a = await signIn(username, PASSWORD);
        return {
          username,
          password: PASSWORD,
          displayName,
          api: a,
          open: async () => {
            const context = await newContext({ storageState: await a.storageState() });
            return { context, page: await context.newPage() };
          },
        };
      },
      household: (u, name = uniq('Home ')) =>
        u.api.post<Household>('/households', { name, timezone: ZONE, starterAreas: true }),
      chore: (u, h, over = {}) =>
        u.api.post<Chore>(`/households/${h.id}/chores`, {
          name: uniq('Chore '),
          notes: '',
          link: '',
          areaId: null,
          assignees: [],
          kind: 'recurring',
          schedule: { type: 'interval', interval: { every: 1, unit: 'week' }, anchor: 'from_completion', firstDue: ymd(0) },
          reminderPreset: '',
          ...over,
        }),
    };
    await use(w);
    for (const close of owned) await close().catch(() => {});
  },
});
export { expect };

/** Base32 (RFC 4648) decode, for the TOTP secret shown by the app. */
function b32(s: string): Buffer {
  const A = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const ch of s.replace(/[\s=]/g, '').toUpperCase()) bits += A.indexOf(ch).toString(2).padStart(5, '0');
  const out: number[] = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) out.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(out);
}

/** RFC 6238 code (SHA-1, 6 digits, 30 s) for the step `offset` periods from now. */
export function totp(secret: string, offset = 0): string {
  const counter = Math.floor(Date.now() / 30_000) + offset;
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(counter));
  const h = createHmac('sha1', b32(secret)).update(msg).digest();
  const o = h[h.length - 1] & 0xf;
  const n = ((h[o] & 0x7f) << 24) | (h[o + 1] << 16) | (h[o + 2] << 8) | h[o + 3];
  return String(n % 1_000_000).padStart(6, '0');
}

export const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];

/** Fails the test on any WCAG 2.2 AA violation on the current page. */
export async function axe(page: Page, what: string): Promise<void> {
  const r = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  const lines = r.violations.map((v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(' ')).join(' | ')}`);
  expect.soft(lines, `axe violations on ${what}`).toEqual([]);
}

export async function setTheme(page: Page, theme: 'light' | 'dark'): Promise<void> {
  await page.evaluate((t) => {
    document.documentElement.dataset.theme = t;
  }, theme);
}

export { join };

/** A header action (New household, Account, Admin, Sign out): opens the phone menu first when the links are folded into it. */
export async function headerItem(page: Page, role: 'link' | 'button', name: string) {
  const nav = page.getByRole('navigation', { name: 'Main' });
  const menu = nav.getByRole('button', { name: 'Menu' });
  if (await menu.isVisible()) {
    if ((await menu.getAttribute('aria-expanded')) !== 'true') await menu.click();
  }
  return nav.getByRole(role, { name });
}
