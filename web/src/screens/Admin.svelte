<script lang="ts">
  import { href } from '../lib/base';
  import SettingsLayout, { type Group } from '../components/SettingsLayout.svelte';
  import { t } from '../lib/i18n';
  import Ai from './admin/Ai.svelte';
  import EmailWarning from './admin/EmailWarning.svelte';
  import ProxyWarning from './admin/ProxyWarning.svelte';
  import SecurityEvents from './admin/SecurityEvents.svelte';
  import ServiceCards from './admin/ServiceCards.svelte';
  import Settings from './admin/Settings.svelte';
  import Updates from './admin/Updates.svelte';
  import Users from './admin/Users.svelte';

  let { section }: { section?: string } = $props();
  const groups: Group[] = [
    { id: 'users', label: t('admin.group.users'), hint: t('admin.group.usersHint'), icon: 'users' },
    { id: 'signin', label: t('admin.group.signin'), hint: t('admin.group.signinHint'), icon: 'lock' },
    { id: 'email', label: t('admin.group.email'), hint: t('admin.group.emailHint'), icon: 'mail' },
    { id: 'connections', label: t('admin.group.connections'), hint: t('admin.group.connectionsHint'), icon: 'plug' },
    { id: 'ai', label: t('admin.group.ai'), hint: t('admin.group.aiHint'), icon: 'sparkle' },
    { id: 'data', label: t('admin.group.data'), hint: t('admin.group.dataHint'), icon: 'clock' },
    { id: 'limits', label: t('admin.group.limits'), hint: t('admin.group.limitsHint'), icon: 'gauge' },
    { id: 'updates', label: t('admin.group.updates'), hint: t('admin.group.updatesHint'), icon: 'refresh' },
  ];
</script>

<SettingsLayout title={t('admin.title')} base="/admin" {groups} {section}>
  {#snippet intro()}<EmailWarning /><ProxyWarning /><SecurityEvents view="banner" {section} /><p class="hint">{t('guide.againLead')} <a href={href('/setup/guide')}>{t('guide.again')}</a></p>{/snippet}
  {#snippet children(g: string)}
    {#if g === 'users'}
      <Users />
      <SecurityEvents view="list" />
    {:else if g === 'updates'}
      <Updates />
      <ServiceCards which="updatehook" />
    {:else if g === 'ai'}
      <Ai />
    {:else if g === 'email'}
      <ServiceCards which="email" />
    {:else}
      <Settings part={g as 'signin' | 'connections' | 'data' | 'limits'} title={groups.find((x) => x.id === g)!.label} />
      {#if g === 'signin'}
        <ServiceCards which="sso" />
        <ServiceCards which="pow" />
      {:else if g === 'connections'}
        <ServiceCards which="metrics" />
      {/if}
    {/if}
  {/snippet}
</SettingsLayout>
