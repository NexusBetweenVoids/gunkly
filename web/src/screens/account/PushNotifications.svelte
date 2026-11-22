<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import { t } from '../../lib/i18n';
  import { deviceSubscription, disablePush, enablePush, iosNeedsHomeScreen, pushError, pushSupported, secure } from '../../lib/push';

  const supported = pushSupported();
  const ios = iosNeedsHomeScreen();
  let here = $state(''); // id of this browser's subscription, '' = none
  let err = $state('');
  let busy = $state(false);

  async function load(): Promise<void> {
    try {
      here = await deviceSubscription();
    } catch (x) {
      err = pushError(x);
    }
  }
  if (supported) void load();

  async function toggle(): Promise<void> {
    busy = true;
    err = '';
    try {
      if (here) await disablePush(here);
      else await enablePush();
      await load();
    } catch (x) {
      err = pushError(x);
    } finally {
      busy = false;
    }
  }
</script>

<section class="card stack-sm" aria-labelledby="a-push">
  <h2 id="a-push">{t('push.title')}</h2>
  {#if !supported}
    <p class="muted" role="status" data-testid="push-unsupported">{ios ? t('push.iosSteps') : secure() ? t('push.unsupported') : t('push.insecure')}</p>
  {:else}
    <p class="status" role="status" data-testid="push-status">{here ? t('push.statusOn') : t('push.statusOff')}</p>
    <div aria-live="polite"><Alert message={err} /></div>
    <div><Button variant={here ? 'quiet' : 'primary'} onclick={toggle} {busy}>{here ? t('push.disable') : t('push.enable')}</Button></div>
    <p class="hint">{t('push.privacy')}</p>
  {/if}
</section>

<style>
  .status {
    margin: 0;
    font-size: 1.0625rem;
    font-weight: 600;
  }
</style>
