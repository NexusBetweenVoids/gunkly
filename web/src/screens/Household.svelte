<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Confirm from '../components/Confirm.svelte';
  import Channels from '../components/Channels.svelte';
  import Deliveries from '../components/Deliveries.svelte';
  import Field from '../components/Field.svelte';
  import Select from '../components/Select.svelte';
  import TimeZone from '../components/TimeZone.svelte';
  import Dialog from '../components/Dialog.svelte';
  import { api, ApiError } from '../lib/api';
  import { failure } from '../lib/errors';
  import { formatDate, t, type Key } from '../lib/i18n';
  import { navigate } from '../lib/router.svelte';
  import { app, refreshHouseholds } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { Household, Member, Role } from '../lib/types';
  import { zones } from '../lib/zones';
  import Lazy from '../components/Lazy.svelte';
  import SettingsLayout, { type Group } from '../components/SettingsLayout.svelte';
  import Areas from './household/Areas.svelte';
  import Feeds from './household/Feeds.svelte';
  import Invites from './household/Invites.svelte';

  const roleLabel: Record<Role, Key> = { owner: 'role.owner', member: 'role.member', guest: 'role.guest' };
  let { id, section }: { id: string; section?: string } = $props();

  let hh = $state<Household | null>(null);
  let members = $state<Member[]>([]);
  let loadErr = $state('');

  let name = $state('');
  let timezone = $state('');
  let preset = $state('');
  let areasEnabled = $state(false);
  let quiet = $state(false);
  let qStart = $state('22:00');
  let qEnd = $state('07:00');
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let tzErr = $state('');
  let confirmLeave = $state(false);
  let removing = $state<Member | null>(null);
  // Leaving can need a successor (owner_required) or a delete confirmation (household_would_be_deleted).
  let candidates = $state<{ userId: string; displayName: string }[] | null>(null);
  let newOwner = $state('');
  let confirmDelete = $state(false);
  let leaveErr = $state('');

  const isOwner = $derived(hh?.role === 'owner');
  const hm = (m: number) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
  const mins = (v: string) => Number(v.slice(0, 2)) * 60 + Number(v.slice(3, 5));

  async function load(): Promise<void> {
    loadErr = '';
    try {
      [hh, members] = await Promise.all([
        api<Household>('GET', `/households/${id}`),
        api<Member[]>('GET', `/households/${id}/members`),
      ]);
      name = hh.name;
      timezone = hh.timezone;
      preset = hh.defaultReminderPreset ?? '';
      areasEnabled = hh.areasEnabled;
      quiet = !!hh.quietHours;
      if (hh.quietHours) [qStart, qEnd] = [hm(hh.quietHours.start), hm(hh.quietHours.end)];
    } catch (x) {
      hh = null;
      loadErr = failure(x).message;
    }
  }
  $effect(() => {
    void id;
    void load();
  });

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = { code: '', message: '' };
    tzErr = zones.includes(timezone) || timezone === hh?.timezone ? '' : t('field.timezoneInvalid');
    if (tzErr) return;
    busy = true;
    try {
      await api('PATCH', `/households/${id}`, {
        name: name.trim(),
        timezone,
        defaultReminderPreset: preset,
        areasEnabled,
        quietHours: quiet ? { start: mins(qStart), end: mins(qEnd) } : null,
      });
      await refreshHouseholds();
      await load();
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }

  let memberErr = $state('');
  async function setRole(m: Member, role: string): Promise<void> {
    memberErr = '';
    try {
      await api('PATCH', `/households/${id}/members/${m.userId}`, { role });
      toast(t('common.saved'));
    } catch (x) {
      memberErr = failure(x).message;
    }
    await load();
  }

  async function remove(m: Member): Promise<void> {
    busy = true;
    memberErr = '';
    try {
      await api('DELETE', `/households/${id}/members/${m.userId}`);
      removing = null;
      await load();
    } catch (x) {
      memberErr = failure(x).message;
      removing = null;
    } finally {
      busy = false;
    }
  }
  async function leave(extra: { newOwner?: string; confirmDelete?: boolean } = {}): Promise<void> {
    busy = true;
    leaveErr = '';
    try {
      await api('DELETE', `/households/${id}/members/${me?.userId}`, extra);
      confirmLeave = false;
      candidates = null;
      confirmDelete = false;
      await refreshHouseholds();
      navigate('/');
    } catch (x) {
      confirmLeave = false;
      if (x instanceof ApiError && x.code === 'owner_required') {
        const hs = (x.body as { households?: { id: string; candidates: { userId: string; displayName: string }[] }[] } | null)?.households ?? [];
        candidates = (hs.find((h) => h.id === id) ?? hs[0])?.candidates ?? [];
        newOwner = candidates[0]?.userId ?? '';
      } else if (x instanceof ApiError && x.code === 'household_would_be_deleted') {
        candidates = null;
        confirmDelete = true;
      } else {
        leaveErr = failure(x).message;
        candidates = null;
        confirmDelete = false;
      }
    } finally {
      busy = false;
    }
  }
  const me = $derived(members.find((m) => m.userId === app.user?.id));

  // Groups this member can see; guests get people, connections and data only.
  const groups = $derived.by((): Group[] => {
    if (!hh) return [];
    const g = (id: string, label: Key, hint: Key, icon: Group['icon']): Group => ({ id, label: t(label), hint: t(hint), icon });
    const member = hh.role !== 'guest';
    return [
      ...(isOwner || (member && hh.areasEnabled) ? [g('general', 'household.group.general', 'household.group.generalHint', 'sliders')] : []),
      g('people', 'household.group.people', 'household.group.peopleHint', 'users'),
      ...(member ? [g('notifications', 'household.group.notifications', 'household.group.notificationsHint', 'bell')] : []),
      g('connections', 'household.group.connections', 'household.group.connectionsHint', 'link'),
      g('data', 'household.group.data', 'household.group.dataHint', 'download'),
    ];
  });
</script>

{#if loadErr}
  <Alert message={loadErr} />
{:else if hh}
  <SettingsLayout title={hh.name} base="/households/{id}" {groups} {section}>
    {#snippet intro()}<p class="muted">{t('household.yourRole', { role: t(roleLabel[hh!.role]) })}</p>{/snippet}
    {#snippet children(g: string)}
      {#if g === 'general'}
        {#if isOwner}
          <section class="card" aria-labelledby="h-settings">
            <h2 id="h-settings">{t('household.settings')}</h2>
            <form class="stack" onsubmit={save}>
              <Field label={t('household.name')} bind:value={name} required maxlength={100} />
              <TimeZone bind:value={timezone} error={tzErr} />
              <Select
                label={t('household.defaultReminder')}
                hint={t('household.defaultReminderHint')}
                bind:value={preset}
                options={[
                  { value: '', label: t('reminder.none') },
                  { value: 'gentle', label: t('reminder.gentle') },
                  { value: 'nag', label: t('reminder.nag') },
                ]}
              />
              <div class="stack-sm">
                <label class="check"><input type="checkbox" bind:checked={areasEnabled} />{t('household.areasEnabled')}</label>
                <p class="hint">{t('household.areasEnabledHint')}</p>
              </div>
              <div class="stack-sm">
                <label class="check"><input type="checkbox" bind:checked={quiet} />{t('household.quiet')}</label>
                {#if quiet}
                  <div class="row">
                    <Field label={t('household.quietStart')} type="time" bind:value={qStart} required />
                    <Field label={t('household.quietEnd')} type="time" bind:value={qEnd} required />
                  </div>
                  <p class="hint">{t('household.quietHint')}</p>
                {/if}
              </div>
              <Alert message={err.message} />
              <div><Button type="submit" {busy}>{t('common.save')}</Button></div>
            </form>
          </section>
        {/if}
          {#if hh!.role !== 'guest' && hh!.areasEnabled}<Areas {id} owner={isOwner} />{/if}
      {:else if g === 'people'}
        <section class="card" aria-labelledby="h-members">
          <h2 id="h-members">{t('household.members')}</h2>
          <Alert message={memberErr} />
          <ul class="list">
            {#each members as m (m.userId)}
              {@const self = m.userId === app.user?.id}
              <li>
                <span>
                  <strong>{m.displayName}</strong>
                  {#if self}<span class="badge">{t('household.you')}</span>{/if}
                  <br /><span class="muted">{t('household.joined', { date: formatDate(new Date(m.joinedAt), { dateStyle: 'medium' }) })}</span>
                </span>
                <span class="row">
                  {#if isOwner && !self}
                    <label class="visually-hidden" for="role-{m.userId}">{t('household.roleFor', { name: m.displayName })}</label>
                    <select id="role-{m.userId}" value={m.role} onchange={(e) => setRole(m, e.currentTarget.value)}>
                      {#each ['owner', 'member', 'guest'] as const as r (r)}
                        <option value={r}>{t(roleLabel[r])}</option>
                      {/each}
                    </select>
                    <Button variant="danger" onclick={() => (removing = m)}>{t('household.remove')}</Button>
                  {:else}
                    <span class="badge">{t(roleLabel[m.role])}</span>
                  {/if}
                </span>
              </li>
            {/each}
          </ul>
        </section>
          {#if isOwner}<Invites {id} />{/if}
      {:else if g === 'notifications'}
        <Channels base="/households/{id}/channels" title={t('channels.householdTitle')} intro={t('channels.householdIntro')} id="hh" />
        <Deliveries path="/households/{id}/deliveries" title={t('delivery.householdTitle')} id="hh" />
      {:else if g === 'connections'}
        <Feeds {id} />
        {#if isOwner && app.instance.webhooksEnabled}
          <Lazy load={() => import('./household/Webhooks.svelte')} ns={['household']} props={{ id }} />
        {/if}
      {:else}
        {#if hh!.role !== 'guest'}<Lazy load={() => import('./household/Transfer.svelte')} props={{ id }} />{/if}
        <section class="card" aria-labelledby="h-more">
          <h2 id="h-more">{t('household.more')}</h2>
          <p class="muted">{t('household.moreHint')}</p>
          <p><a class="btn btn-quiet" href="/households/new">{t('household.createAnother')}</a></p>
        </section>

        <section class="card" aria-labelledby="h-leave">
          <h2 id="h-leave">{t('household.leave')}</h2>
          <p class="muted">{t('household.leaveWarn')}</p>
          <div aria-live="polite"><Alert message={leaveErr} /></div>
          <Button variant="danger" onclick={() => (confirmLeave = true)}>{t('household.leave')}</Button>
        </section>
      {/if}
    {/snippet}
  </SettingsLayout>

  <Confirm
    open={confirmLeave}
    title={t('household.leaveTitle', { name: hh.name })}
    message={t('household.leaveWarn')}
    confirmLabel={t('household.leave')}
    {busy}
    onconfirm={() => leave()}
    oncancel={() => (confirmLeave = false)}
  />
  <Dialog open={!!candidates} title={t('household.leaveOwnerTitle', { name: hh.name })} onclose={() => (candidates = null)}>
    <form
      class="stack"
      onsubmit={(e) => {
        e.preventDefault();
        void leave({ newOwner });
      }}
    >
      <p>{t('household.leaveOwnerWarn')}</p>
      <Select label={t('household.leaveNewOwner')} bind:value={newOwner} options={(candidates ?? []).map((c) => ({ value: c.userId, label: c.displayName }))} />
      <div class="row">
        <Button type="submit" variant="danger" {busy}>{t('household.leave')}</Button>
        <Button variant="quiet" onclick={() => (candidates = null)}>{t('common.cancel')}</Button>
      </div>
    </form>
  </Dialog>
  <Confirm
    open={confirmDelete}
    title={t('household.leaveDeleteTitle', { name: hh.name })}
    message={t('household.leaveDeleteWarn')}
    confirmLabel={t('household.leaveDeleteConfirm')}
    {busy}
    onconfirm={() => leave({ confirmDelete: true })}
    oncancel={() => (confirmDelete = false)}
  />
  <Confirm
    open={!!removing}
    title={t('household.removeTitle', { name: removing?.displayName ?? '' })}
    message={t('household.removeWarn')}
    confirmLabel={t('household.remove')}
    {busy}
    onconfirm={() => removing && remove(removing)}
    oncancel={() => (removing = null)}
  />
{:else}
  <p role="status" class="muted">{t('common.loading')}</p>
{/if}
