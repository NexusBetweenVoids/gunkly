<script lang="ts">
  import Lazy from '../components/Lazy.svelte';
  import SettingsLayout, { type Group, type GroupSet } from '../components/SettingsLayout.svelte';
  import { t, type Key } from '../lib/i18n';
  import { currentHousehold } from '../lib/session.svelte';
  import type { Role } from '../lib/types';
  import Email from './account/Email.svelte';
  import Notifications from './account/Notifications.svelte';
  import Profile from './account/Profile.svelte';
  import Security from './account/Security.svelte';
  import SetupAgain from './account/SetupAgain.svelte';
  import HouseholdSettings, { householdGroups } from './household/HouseholdSettings.svelte';

  // One settings screen: your own settings, then the current household's
  // (group ids prefixed "household-"). Instance administration stays separate.
  let { section }: { section?: string } = $props();
  const HH = 'household-';
  const roleLabel: Record<Role, Key> = { owner: 'role.owner', member: 'role.member', guest: 'role.guest' };
  const mine: Group[] = [
    { id: 'profile', label: t('account.group.profile'), hint: t('account.group.profileHint'), icon: 'user' },
    { id: 'security', label: t('account.group.security'), hint: t('account.group.securityHint'), icon: 'lock' },
    { id: 'notifications', label: t('account.group.notifications'), hint: t('account.group.notificationsHint'), icon: 'bell' },
    { id: 'ai', label: t('account.group.ai'), hint: t('account.group.aiHint'), icon: 'sparkle' },
    { id: 'integrations', label: t('account.group.integrations'), hint: t('account.group.integrationsHint'), icon: 'plug' },
    { id: 'data', label: t('account.group.data'), hint: t('account.group.dataHint'), icon: 'download' },
  ];
  const hh = $derived(currentHousehold());
  const theirs = $derived(hh ? householdGroups(hh).map((g) => ({ ...g, id: HH + g.id })) : []);
  const sets = $derived<GroupSet[]>([
    { id: 'you', label: t('settings.you'), groups: mine },
    ...(hh ? [{ id: 'household', label: hh.name, hint: t('household.yourRole', { role: t(roleLabel[hh.role]) }), groups: theirs }] : []),
  ]);
  // "/settings/household" opens the household's first group.
  const open = $derived(section === 'household' ? theirs[0]?.id : section);
</script>

<SettingsLayout title={t('settings.title')} base="/settings" {sets} section={open}>
  {#snippet children(g: string)}
    {#if g.startsWith(HH) && hh}
      <HouseholdSettings id={hh.id} group={g.slice(HH.length)} />
    {:else if g === 'profile'}
      <Profile />
      <Email />
      <SetupAgain />
    {:else if g === 'security'}
      <Security />
    {:else if g === 'notifications'}
      <Notifications />
    {:else if g === 'ai'}
      <Lazy load={() => import('./account/Ai.svelte')} />
    {:else if g === 'integrations'}
      <Lazy load={() => import('./account/Integrations.svelte')} />
    {:else}
      <Lazy load={() => import('./account/ExportData.svelte')} />
      <Lazy load={() => import('./account/DeleteAccount.svelte')} />
    {/if}
  {/snippet}
</SettingsLayout>
