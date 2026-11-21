import { api, ApiError } from './api';
import type { Household, Me, SessionView } from './types';

const HH_KEY = 'gunkly.household';

export const app = $state({
  ready: false,
  user: null as Me | null,
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

/** Adopt a signed-in session response and load what the shell needs. */
export async function adopt(s: SessionView): Promise<void> {
  app.user = s.user ?? null;
  app.setupNeeded = false;
  if (app.user) await refreshHouseholds().catch(() => {});
}

export async function bootstrap(): Promise<void> {
  try {
    const s = await api<SessionView>('GET', '/session');
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
