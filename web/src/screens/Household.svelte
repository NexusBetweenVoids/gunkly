<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Confirm from '../components/Confirm.svelte';
  import Field from '../components/Field.svelte';
  import TimeZone from '../components/TimeZone.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { formatDate, t, type Key } from '../lib/i18n';
  import { navigate } from '../lib/router';
  import { app, refreshHouseholds } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { Household, Member, Role } from '../lib/types';
  import { zones } from '../lib/zones';
  import Invites from './household/Invites.svelte';

  let { id }: { id: string } = $props();
  const roleLabel: Record<Role, Key> = { owner: 'role.owner', member: 'role.member', guest: 'role.guest' };

  let hh = $state<Household | null>(null);
  let members = $state<Member[]>([]);
  let loadErr = $state('');

  let name = $state('');
  let timezone = $state('');
  let quiet = $state(false);
  let qStart = $state('22:00');
  let qEnd = $state('07:00');
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let tzErr = $state('');
  let confirmLeave = $state(false);
  let removing = $state<Member | null>(null);

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

  async function remove(m: Member, self: boolean): Promise<void> {
    busy = true;
    memberErr = '';
    try {
      await api('DELETE', `/households/${id}/members/${m.userId}`);
      removing = null;
      confirmLeave = false;
      if (self) {
        await refreshHouseholds();
        navigate('/');
      } else {
        await load();
      }
    } catch (x) {
      memberErr = failure(x).message;
      removing = null;
      confirmLeave = false;
    } finally {
      busy = false;
    }
  }
  const me = $derived(members.find((m) => m.userId === app.user?.id));
</script>

{#if loadErr}
  <Alert message={loadErr} />
{:else if hh}
  <h1>{hh.name}</h1>
  <p class="muted">{t('household.yourRole', { role: t(roleLabel[hh.role]) })}</p>
  <div class="sections">
    {#if isOwner}
      <section class="card" aria-labelledby="h-settings">
        <h2 id="h-settings">{t('household.settings')}</h2>
        <form class="stack" onsubmit={save}>
          <Field label={t('household.name')} bind:value={name} required maxlength={100} />
          <TimeZone bind:value={timezone} error={tzErr} />
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

    <section class="card" aria-labelledby="h-leave">
      <h2 id="h-leave">{t('household.leave')}</h2>
      <p class="muted">{t('household.leaveWarn')}</p>
      <Button variant="danger" onclick={() => (confirmLeave = true)}>{t('household.leave')}</Button>
    </section>
  </div>

  <Confirm
    open={confirmLeave}
    title={t('household.leaveTitle', { name: hh.name })}
    message={t('household.leaveWarn')}
    confirmLabel={t('household.leave')}
    {busy}
    onconfirm={() => me && remove(me, true)}
    oncancel={() => (confirmLeave = false)}
  />
  <Confirm
    open={!!removing}
    title={t('household.removeTitle', { name: removing?.displayName ?? '' })}
    message={t('household.removeWarn')}
    confirmLabel={t('household.remove')}
    {busy}
    onconfirm={() => removing && remove(removing, false)}
    oncancel={() => (removing = null)}
  />
{:else}
  <p role="status" class="muted">{t('common.loading')}</p>
{/if}
