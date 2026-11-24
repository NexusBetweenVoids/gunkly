<script lang="ts">
  import Channels from '../components/Channels.svelte';
  import Deliveries from '../components/Deliveries.svelte';
  import Lazy from '../components/Lazy.svelte';
  import SettingsLayout, { type Group } from '../components/SettingsLayout.svelte';
  import { t } from '../lib/i18n';
  import { app } from '../lib/session.svelte';
  import Audit from './account/Audit.svelte';
  import Email from './account/Email.svelte';
  import EmailReminders from './account/EmailReminders.svelte';
  import QuietHours from './account/QuietHours.svelte';
  import EmailCodes from './account/EmailCodes.svelte';
  import Passkeys from './account/Passkeys.svelte';
  import Password from './account/Password.svelte';
  import PushNotifications from './account/PushNotifications.svelte';
  import Profile from './account/Profile.svelte';
  import Sessions from './account/Sessions.svelte';
  import Sso from './account/Sso.svelte';
  import Totp from './account/Totp.svelte';

  let { section }: { section?: string } = $props();
  const groups: Group[] = [
    { id: 'profile', label: t('account.group.profile'), hint: t('account.group.profileHint'), icon: 'user' },
    { id: 'security', label: t('account.group.security'), hint: t('account.group.securityHint'), icon: 'lock' },
    { id: 'notifications', label: t('account.group.notifications'), hint: t('account.group.notificationsHint'), icon: 'bell' },
    { id: 'integrations', label: t('account.group.integrations'), hint: t('account.group.integrationsHint'), icon: 'plug' },
    { id: 'data', label: t('account.group.data'), hint: t('account.group.dataHint'), icon: 'download' },
  ];
</script>

<SettingsLayout title={t('nav.account')} base="/account" {groups} {section}>
  {#snippet children(g: string)}
    {#if g === 'profile'}
      <Profile />
      <Email />
    {:else if g === 'security'}
      <Password />
      <Passkeys />
      {#if app.instance.sso}<Sso />{/if}
      <Totp />
      <EmailCodes />
      <Sessions />
      <Audit />
    {:else if g === 'notifications'}
      <PushNotifications />
      <EmailReminders />
      <QuietHours />
      <details class="card advanced">
        <summary>{t('channels.advancedTitle')}</summary>
        <p class="hint">{t('channels.advancedHint')}</p>
        <Channels base="/account/channels" title={t('channels.personalTitle')} id="me" bare />
      </details>
      <Deliveries path="/account/deliveries" title={t('delivery.personalTitle')} id="me" />
    {:else if g === 'integrations'}
      <Lazy load={() => import('./account/Integrations.svelte')} />
    {:else}
      <Lazy load={() => import('./account/ExportData.svelte')} />
      <Lazy load={() => import('./account/DeleteAccount.svelte')} />
    {/if}
  {/snippet}
</SettingsLayout>
