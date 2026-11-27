import { api, ApiError } from './api';
import { useLanguage } from './i18n';
import type { Household, Instance, Me, SessionView } from './types';

const HH_KEY = 'gunkly.household';

export const app = $state({
  ready: false,
  user: null as Me | null,
  instance: { openSignup: false, emailEnabled: false, hostedMode: false, requirePow: false } as Instance,
  setupNeeded: false,
  households: [] as Household[],
  hhId: '',
});

export function currentHousehold(): Household | undefined {
  return app.households.find((h) => h.id === app.hhId);
}

export function selectHousehold(id: string): void {
  app.hhId = id;
  try {
    localStorage.setItem(HH_KEY, id);
  } catch {
    // Storage blocked: selection lasts for this page only.
  }
}

export async function refreshHouseholds(prefer?: string): Promise<void> {
  app.households = await api<Household[]>('GET', '/households');
  let saved = '';
  try {
    saved = localStorage.getItem(HH_KEY) ?? '';
  } catch {
    // ignore
  }
  const want = [prefer, app.hhId, saved].find((id) => id && app.households.some((h) => h.id === id));
  app.hhId = want ?? app.households[0]?.id ?? '';
}

export async function refreshMe(): Promise<void> {
  app.user = await api<Me>('GET', '/me');
}

// Account changes made in another tab (confirming the email address from the link in a second tab, for
// example) reach this one: the other tab announces them on a BroadcastChannel, and a tab that comes back
// into view while the email is still unconfirmed checks again. Both re-read /me only.
const channel = typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('gunkly-account');
let lastCheck = 0;

/** Tell other open tabs that this account changed. */
export function announceAccountChange(): void {
  channel?.postMessage('me');
}

function recheck(): void {
  if (!app.user) return;
  lastCheck = Date.now();
  void refreshMe().catch(() => {});
}

channel?.addEventListener('message', recheck);
if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && app.user?.email && !app.user.emailVerified && Date.now() - lastCheck > 5000) recheck();
  });
}

/** Adopt a signed-in session response and load what the shell needs. */
export async function adopt(s: SessionView): Promise<void> {
  app.user = s.user ?? null;
  app.setupNeeded = false;
  if (app.user && useLanguage(app.user.locale)) return; // reloading in the account's language
  if (app.user) await refreshHouseholds().catch(() => {});
}

export async function bootstrap(): Promise<void> {
  try {
    const [s, instance] = await Promise.all([
      api<SessionView>('GET', '/session'),
      api<Instance>('GET', '/instance').catch(() => app.instance),
    ]);
    app.instance = instance;
    if (s.authenticated) await adopt(s);
    else app.setupNeeded = (await api<{ needed: boolean }>('GET', '/setup')).needed;
  } catch (e) {
    if (!(e instanceof ApiError)) throw e;
  }
  app.ready = true;
}

export function clearSession(): void {
  app.user = null;
  app.households = [];
  app.hhId = '';
}

export async function signOut(): Promise<void> {
  await api<void>('DELETE', '/session').catch(() => {});
  clearSession();
}
