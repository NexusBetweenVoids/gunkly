<script lang="ts">
  import { api } from '../lib/api';
  import { t } from '../lib/i18n';
  import { deviceSubscription, enablePush, pushError, pushSupported } from '../lib/push';
  import { app } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import Alert from './Alert.svelte';
  import Button from './Button.svelte';

  // Home: a one-time, dismissible offer of reminders on this device. Shown only to someone who can
  // get reminders (not a guest), whose browser supports push, who has no push subscription here and
  // no other personal route (email reminders or a messaging channel). "Not now" is remembered per device.
  const KEY = 'gunkly.pushPrompt';
  const dismissed = (): boolean => {
    try {
      return localStorage.getItem(KEY) === 'no';
    } catch {
      return false;
    }
  };

  let show = $state(false);
  let busy = $state(false);
  let err = $state('');

  const eligible = () =>
    !!app.user && app.households.some((h) => h.role !== 'guest') && pushSupported() && Notification.permission !== 'denied' && !dismissed() && !app.user.emailReminders;

  async function check(): Promise<void> {
    if (!eligible()) return;
    try {
      const [here, channels] = await Promise.all([deviceSubscription(), api<unknown[]>('GET', '/account/channels')]);
      show = !here && !channels.length;
    } catch {
      // Not offering is always safe.
    }
  }
  void check();

  function later(): void {
    try {
      localStorage.setItem(KEY, 'no');
    } catch {
      // Storage blocked: it comes back next visit.
    }
    show = false;
  }

  async function turnOn(): Promise<void> {
    busy = true;
    err = '';
    try {
      await enablePush();
      show = false;
      toast(t('home.push.on'));
    } catch (x) {
      err = pushError(x);
    } finally {
      busy = false;
    }
  }
</script>

{#if show}
  <section class="card stack-sm" aria-labelledby="h-push" data-testid="push-prompt">
    <h2 id="h-push">{t('home.push.title')}</h2>
    <p class="muted">{t('home.push.body')}</p>
    <div aria-live="polite"><Alert message={err} /></div>
    <div class="row">
      <Button onclick={turnOn} {busy}>{t('home.push.turnOn')}</Button>
      <Button variant="quiet" onclick={later}>{t('home.push.notNow')}</Button>
    </div>
  </section>
{/if}
