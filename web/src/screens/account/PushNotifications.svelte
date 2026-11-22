<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';

  type Sub = { id: string; endpointHash: string };

  const secure = window.isSecureContext;
  const supported = secure && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  let here = $state(''); // id of this browser's subscription, '' = none
  let err = $state('');
  let busy = $state(false);

  const browserSub = async () => (await navigator.serviceWorker.ready).pushManager.getSubscription();

  async function load(): Promise<void> {
    try {
      const mine = await browserSub();
      const h = mine ? [...new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(mine.endpoint)))].map((b) => b.toString(16).padStart(2, '0')).join('') : '';
      here = (await api<Sub[]>('GET', '/account/push-subscriptions')).find((s) => s.endpointHash === h)?.id ?? '';
    } catch (x) {
      err = failure(x).message;
    }
  }
  if (supported) void load();

  async function toggle(): Promise<void> {
    busy = true;
    err = '';
    try {
      if (here) {
        await api('DELETE', `/account/push-subscriptions/${here}`);
        await (await browserSub())?.unsubscribe();
      } else {
        const { publicKey } = await api<{ publicKey: string }>('GET', '/push/vapid-public-key');
        const key = Uint8Array.from(atob(publicKey.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));
        const reg = await navigator.serviceWorker.ready;
        const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }));
        await api('POST', '/account/push-subscriptions', sub.toJSON());
      }
      await load();
    } catch (x) {
      err = x instanceof DOMException ? t(Notification.permission === 'denied' ? 'push.denied' : 'push.failed') : failure(x).message;
    } finally {
      busy = false;
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
    <Button variant={here ? 'quiet' : 'primary'} onclick={toggle} {busy}>{here ? t('push.disable') : t('push.enable')}</Button>
  {/if}
</section>
