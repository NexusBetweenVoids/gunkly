<script lang="ts">
  import { appOrigin } from '../../lib/base';
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { failure } from '../../lib/errors';
  import { formatDate, t, type Key } from '../../lib/i18n';
  import { toast } from '../../lib/toast.svelte';
  import type { Invite, Role } from '../../lib/types';

  const roleLabel: Record<Role, Key> = { owner: 'role.owner', member: 'role.member', guest: 'role.guest' };
  let { id }: { id: string } = $props();
  const expiries: { hours: number; label: Key }[] = [
    { hours: 24, label: 'invite.exp1' },
    { hours: 72, label: 'invite.exp3' },
    { hours: 168, label: 'invite.exp7' },
  ];

  let invites = $state<Invite[]>([]);
  let role = $state<Role>('member');
  let hours = $state(72);
  let several = $state(false);
  let maxUses = $state(5);
  let link = $state('');
  let busy = $state(false);
  let err = $state('');

  async function load(): Promise<void> {
    try {
      invites = await api<Invite[]>('GET', `/households/${id}/invites`);
    } catch (x) {
      err = failure(x).message;
    }
  }
  $effect(() => {
    void id;
    void load();
  });

  async function create(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      const inv = await api<Invite>('POST', `/households/${id}/invites`, { role, expiresInHours: hours, maxUses: several ? Math.min(100, Math.max(1, Math.floor(maxUses) || 1)) : 1 });
      link = `${appOrigin()}/invite/${inv.token}`;
      await load();
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function revoke(inv: Invite): Promise<void> {
    err = '';
    try {
      await api('DELETE', `/households/${id}/invites/${inv.id}`);
      toast(t('invite.revoked'));
    } catch (x) {
      err = failure(x).message;
    }
    await load();
  }
</script>

<section class="card" aria-labelledby="h-invites">
  <h2 id="h-invites">{t('invite.heading')}</h2>
  <form class="stack" onsubmit={create}>
    <div class="row">
      <Field label={t('invite.role')}>
        {#snippet children({ id: fid })}
          <select id={fid} bind:value={role}>
            {#each ['member', 'guest', 'owner'] as const as r (r)}<option value={r}>{t(roleLabel[r])}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <Field label={t('invite.expires')}>
        {#snippet children({ id: fid })}
          <select id={fid} bind:value={hours}>
            {#each expiries as x (x.hours)}<option value={x.hours}>{t(x.label)}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <Field label={t('invite.usesLabel')}>
        {#snippet children({ id: fid })}
          <select id={fid} bind:value={several}>
            <option value={false}>{t('invite.usesOne')}</option>
            <option value={true}>{t('invite.usesMany')}</option>
          </select>
        {/snippet}
      </Field>
      {#if several}
        <Field label={t('invite.usesCount')}>
          {#snippet children({ id: fid })}
            <input id={fid} type="number" min="2" max="100" inputmode="numeric" bind:value={maxUses} />
          {/snippet}
        </Field>
      {/if}
    </div>
    <div><Button type="submit" {busy}>{t('invite.create')}</Button></div>
  </form>
  <Alert message={err} />
  {#if link}
    <div class="stack-sm mt-md">
      <p class="label" id="new-invite">{t('invite.newLink')}</p>
      <p class="mono" aria-labelledby="new-invite">{link}</p>
      <p class="hint">{t('invite.linkOnce')}</p>
      <div><Button variant="quiet" onclick={() => copyText(link)}>{t('common.copy')}</Button></div>
    </div>
  {/if}
  <h3 class="mt-lg">{t('invite.active')}</h3>
  {#if invites.length === 0}
    <p class="muted">{t('invite.none')}</p>
  {:else}
    <ul class="list">
      {#each invites as inv (inv.id)}
        <li>
          <span>
            <span class="badge">{t(roleLabel[inv.role])}</span>
            <span class="muted">{t('invite.usesLeft', { remaining: inv.maxUses - inv.uses, max: inv.maxUses })}</span>
            <span class="muted">{t('invite.expiresOn', { date: formatDate(new Date(inv.expiresAt), { dateStyle: 'medium', timeStyle: 'short' }) })}</span>
          </span>
          <Button variant="danger" onclick={() => revoke(inv)}>{t('invite.revoke')}</Button>
        </li>
      {/each}
    </ul>
  {/if}
</section>
