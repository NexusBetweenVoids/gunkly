<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { toast } from '../../lib/toast.svelte';

  type Sub = { id: string; endpointHash: string; userAgent: string; createdAt: string };

  const secure = window.isSecureContext;
  const supported = secure && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  let subs = $state<Sub[]>([]);
  let here = $state(''); // id of this browser's subscription, '' = none
  let err = $state('');
  let busy = $state(false);

  async function sha256Hex(s: string): Promise<string> {
    const d = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(s));
    return [...new Uint8Array(d)].map((b) => b.toString(16).padStart(2, '0')).join('');
  }

  async function browserSub(): Promise<PushSubscription | null> {
    return (await navigator.serviceWorker.ready).pushManager.getSubscription();
  }

  async function load(): Promise<void> {
    try {
      subs = await api<Sub[]>('GET', '/account/push-subscriptions');
      const mine = await browserSub();
      const h = mine ? await sha256Hex(mine.endpoint) : '';
      here = subs.find((s) => s.endpointHash === h)?.id ?? '';
    } catch (x) {
      err = failure(x).message;
    }
  }
  if (supported) void load();

  function keyBytes(b64: string): Uint8Array<ArrayBuffer> {
    const bin = atob(b64.replace(/-/g, '+').replace(/_/g, '/'));
    return Uint8Array.from(bin, (c) => c.charCodeAt(0));
  }

  async function enable(): Promise<void> {
    busy = true;
    err = '';
    try {
      if ((await Notification.requestPermission()) !== 'granted') {
        err = t('push.denied');
        return;
      }
      const { publicKey } = await api<{ publicKey: string }>('GET', '/push/vapid-public-key');
      const reg = await navigator.serviceWorker.ready;
      const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) }));
      await api('POST', '/account/push-subscriptions', sub.toJSON());
      toast(t('common.saved'));
      await load();
    } catch (x) {
      err = x instanceof DOMException ? t('push.failed') : failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function remove(id: string): Promise<void> {
    err = '';
    try {
      await api('DELETE', `/account/push-subscriptions/${id}`);
      if (id === here) await (await browserSub())?.unsubscribe();
      await load();
    } catch (x) {
      err = failure(x).message;
    }
  }
</script>

<section class="card" aria-labelledby="a-push">
  <h2 id="a-push">{t('push.title')}</h2>
  <p class="hint">{t('push.privacy')}</p>
  {#if !supported}
    <p class="muted" role="status" data-testid="push-unsupported">{secure ? t('push.unsupported') : t('push.insecure')}</p>
  {:else}
    <div aria-live="polite"><Alert message={err} /></div>
    {#if subs.length}
      <ul class="list">
        {#each subs as s (s.id)}
          <li>
            <span>
              <strong>{s.id === here ? t('push.thisDevice') : t('push.device')}</strong><br />
              <span class="muted">{s.userAgent}</span>
            </span>
            <Button variant="danger" onclick={() => remove(s.id)}>{t('push.remove')}</Button>
          </li>
        {/each}
      </ul>
    {/if}
    {#if !here}
      <div class="mt-md"><Button onclick={enable} {busy}>{t('push.enable')}</Button></div>
    {/if}
  {/if}
</section>
