<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Channels from '../../components/Channels.svelte';
  import Deliveries from '../../components/Deliveries.svelte';
  import Icon from '../../components/Icon.svelte';
  import { api } from '../../lib/api';
  import { href } from '../../lib/base';
  import { failure } from '../../lib/errors';
  import { locale, t } from '../../lib/i18n';
  import { deviceSubscription, disablePush, enablePush, iosNeedsHomeScreen, pushError, pushSupported, secure } from '../../lib/push';
  import { app } from '../../lib/session.svelte';
  import type { Channel, Me } from '../../lib/types';
  import MethodRow from './MethodRow.svelte';
  import QuietHours from './QuietHours.svelte';
  import StatusLine from './StatusLine.svelte';

  // Settings, Notifications: first the answer to "will I get reminders?", then the three ways to get
  // them (this device, email, messaging apps), quiet hours and the delivery log.
  const supported = pushSupported();
  const ios = iosNeedsHomeScreen();
  let here = $state(''); // id of this browser's subscription, '' = none
  let others = $state(0); // subscriptions of the account's other devices
  let pushLoaded = $state(false);
  let pushErr = $state('');
  let pushBusy = $state(false);
  let channels = $state<Channel[]>([]);
  let channelsLoaded = $state(false);
  let appsOpen = $state(false);
  let mailErr = $state('');
  let mailBusy = $state(false);

  const emailOn = $derived(app.instance.emailEnabled);
  const verified = $derived(!!app.user?.email && !!app.user.emailVerified);
  const mailing = $derived(emailOn && verified && !!app.user?.emailReminders);

  async function loadPush(): Promise<void> {
    try {
      here = supported ? await deviceSubscription() : '';
      const all = await api<{ id: string }[]>('GET', '/account/push-subscriptions');
      others = all.filter((s) => s.id !== here).length;
    } catch (x) {
      pushErr = pushError(x);
    }
    pushLoaded = true;
  }
  void loadPush();
  api<Channel[]>('GET', '/account/channels')
    .then((r) => (channels = r))
    .catch(() => {}) // the list below shows its own error
    .finally(() => (channelsLoaded = true));

  async function togglePush(): Promise<void> {
    pushBusy = true;
    pushErr = '';
    try {
      if (here) await disablePush(here);
      else await enablePush();
      await loadPush();
    } catch (x) {
      pushErr = pushError(x);
    } finally {
      pushBusy = false;
    }
  }

  async function setMail(on: boolean): Promise<void> {
    mailBusy = true;
    mailErr = '';
    try {
      app.user = await api<Me>('PATCH', '/me', { emailReminders: on });
    } catch (x) {
      mailErr = failure(x).message;
    } finally {
      mailBusy = false;
    }
  }

  const hm = (m: number) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
  const status = $derived.by(() => {
    if (!pushLoaded || !channelsLoaded) return null;
    const ways: string[] = [];
    if (here) ways.push(t('notif.onDevice'));
    if (others) ways.push(t('notif.onOthers', { count: others }));
    if (mailing) ways.push(t('notif.byEmail'));
    if (channels.length) ways.push(t('notif.inApps', { count: channels.length }));
    if (!ways.length) return { tone: 'warn', title: t('notif.none'), detail: t('notif.noneDetail') } as const;
    const q = app.user?.quietHours;
    const detail = [
      q ? t('notif.quiet', { start: hm(q.start), end: hm(q.end) }) : '',
      app.households.length ? t('notif.household') : '',
    ].filter(Boolean);
    return {
      tone: 'ok',
      title: t('notif.some', { list: new Intl.ListFormat(locale, { type: 'conjunction' }).format(ways) }),
      detail: detail.join(' · '),
    } as const;
  });
</script>

{#if status}
  <StatusLine tone={status.tone} icon={status.tone === 'ok' ? 'bell' : 'bellOff'} title={status.title} detail={status.detail} testid="reminder-status" />
{/if}

<section class="card" aria-labelledby="a-routes">
  <h2 id="a-routes" class="card-title">{t('notif.waysTitle')}</h2>
  <div class="methods">
    <MethodRow
      id="a-push"
      name={t('push.title')}
      hint={supported && pushLoaded && !here ? '' : t('notif.deviceHint')}
      state={!supported ? t('ways.unavailable') : pushLoaded ? (here ? t('ways.on') : t('ways.off')) : ''}
      tone={here ? 'ok' : ''}
      testid="push-status"
    >
      {#snippet notes()}
        {#if !supported}
          <p class="hint" data-testid="push-unsupported">{ios ? t('push.iosSteps') : secure() ? t('push.unsupported') : t('push.insecure')}</p>
        {:else if pushLoaded && !here}
          <!-- Before turning it on: what the browser vendor can see. -->
          <p class="hint">{t('push.privacy')}</p>
        {/if}
        <div aria-live="polite"><Alert message={pushErr} /></div>
      {/snippet}
      {#snippet action()}
        {#if supported && pushLoaded}
          <Button
            variant={here ? 'quiet' : 'primary'}
            aria-label={here ? t('push.disable') : t('push.enable')}
            onclick={togglePush}
            busy={pushBusy}>{here ? t('ways.turnOff') : t('ways.turnOn')}</Button
          >
        {/if}
      {/snippet}
    </MethodRow>

    {#if emailOn}
      <MethodRow
        id="a-email-rem"
        name={t('notif.email')}
        hint={verified ? t('emailReminders.hint', { email: app.user?.email ?? '' }) : ''}
        state={!verified ? t('ways.unavailable') : app.user?.emailReminders ? t('ways.on') : t('ways.off')}
        tone={mailing ? 'ok' : ''}
      >
        {#snippet notes()}
          {#if verified}
            <p class="hint">{t('emailReminders.changed')}</p>
          {:else}
            <p class="hint" data-testid="email-reminders-hint">{t('emailReminders.needVerified')}</p>
          {/if}
          <div aria-live="polite"><Alert message={mailErr} /></div>
        {/snippet}
        {#snippet action()}
          {#if verified}
            <Button variant="quiet" busy={mailBusy} onclick={() => setMail(!app.user?.emailReminders)}
              >{app.user?.emailReminders ? t('ways.turnOff') : t('ways.turnOn')}</Button
            >
          {:else}
            <a class="btn btn-quiet" href={href('/settings/profile')}>{app.user?.email ? t('ways.confirmEmail') : t('ways.addEmail')}</a>
          {/if}
        {/snippet}
      </MethodRow>
    {/if}

    <MethodRow
      id="a-apps"
      name={t('channels.advancedTitle')}
      hint={t('channels.advancedHint')}
      state={channelsLoaded ? (channels.length ? t('notif.appCount', { count: channels.length }) : t('ways.none')) : ''}
      tone={channels.length ? 'ok' : ''}
      open={appsOpen}
    >
      {#snippet action()}
        <Button variant="quiet" class="disclosure" aria-expanded={appsOpen} onclick={() => (appsOpen = !appsOpen)}>
          {channels.length ? t('ways.manage') : t('ways.setUp')}
          <span class="chev" class:open={appsOpen}><Icon name="chevron" size={18} /></span>
        </Button>
      {/snippet}
      <Channels base="/account/channels" title={t('channels.personalTitle')} id="me" bare bind:items={channels} />
    </MethodRow>
  </div>
</section>

<QuietHours />
<Deliveries path="/account/deliveries" title={t('delivery.personalTitle')} id="me" />

<style>
  .methods {
    margin-block-start: 4px;
  }
  .methods > :global(.method:first-child) {
    border-block-start: 0;
  }
  .chev {
    display: inline-flex;
    margin-inline-start: 4px;
    transition: transform var(--dur) var(--ease);
  }
  .chev.open {
    transform: rotate(90deg);
  }
  :global([dir='rtl']) .chev:not(.open) {
    transform: scaleX(-1);
  }
</style>
