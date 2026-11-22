import { api } from './api';
import { failure } from './errors';
import { t } from './i18n';

// Web Push for this browser, shared by the Home prompt and Account, Notifications.
// Always imported lazily (never from the entry chunk).

type Sub = { id: string; endpointHash: string };

export const secure = (): boolean => window.isSecureContext;
export const pushSupported = (): boolean => secure() && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;

/** iPhone/iPad in a Safari tab: push only works once the site is on the Home Screen. */
export function iosNeedsHomeScreen(): boolean {
  const ios = /iP(hone|ad|od)/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const standalone = (navigator as Navigator & { standalone?: boolean }).standalone === true || matchMedia('(display-mode: standalone)').matches;
  return ios && !standalone;
}

const browserSub = async () => (await navigator.serviceWorker.ready).pushManager.getSubscription();

/** The id of this browser's subscription on the server, '' = none. */
export async function deviceSubscription(): Promise<string> {
  const mine = await browserSub();
  if (!mine) return '';
  const h = [...new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(mine.endpoint)))].map((b) => b.toString(16).padStart(2, '0')).join('');
  return (await api<Sub[]>('GET', '/account/push-subscriptions')).find((s) => s.endpointHash === h)?.id ?? '';
}

/** Asks for permission (the browser prompt) and registers this device. */
export async function enablePush(): Promise<void> {
  const { publicKey } = await api<{ publicKey: string }>('GET', '/push/vapid-public-key');
  const key = Uint8Array.from(atob(publicKey.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));
  const reg = await navigator.serviceWorker.ready;
  const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }));
  await api('POST', '/account/push-subscriptions', sub.toJSON());
}

export async function disablePush(id: string): Promise<void> {
  await api('DELETE', `/account/push-subscriptions/${id}`);
  await (await browserSub())?.unsubscribe();
}

/** A message for a failed enable/disable. */
export const pushError = (x: unknown): string =>
  x instanceof DOMException ? t(Notification.permission === 'denied' ? 'push.denied' : 'push.failed') : failure(x).message;
