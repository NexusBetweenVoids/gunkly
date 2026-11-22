<script lang="ts">
  import AssistantConnect from './AssistantConnect.svelte';
  import ConnectedApps from './ConnectedApps.svelte';
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { failure } from '../../lib/errors';
  import { formatDate, t, type Key } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';
  import type { Household } from '../../lib/types';

  type Scope = 'chores:read' | 'chores:complete' | 'chores:write';
  type Token = {
    id: string;
    name: string;
    scopes: Scope[];
    households: { id: string; name: string }[];
    createdAt: string;
    lastUsedAt: string | null;
    expiresAt: string | null;
    token?: string;
  };
  const scopes: { id: Scope; label: Key; short: Key }[] = [
    { id: 'chores:read', label: 'tokens.scopeRead', short: 'tokens.shortRead' },
    { id: 'chores:complete', label: 'tokens.scopeComplete', short: 'tokens.shortComplete' },
    { id: 'chores:write', label: 'tokens.scopeWrite', short: 'tokens.shortWrite' },
  ];
  const expiries: { days: string; label: Key }[] = [
    { days: '30', label: 'tokens.exp30' },
    { days: '90', label: 'tokens.exp90' },
    { days: '365', label: 'tokens.exp365' },
    { days: '', label: 'tokens.expNever' },
  ];
  const when = (s: string) => formatDate(new Date(s), { dateStyle: 'medium' });

  let tokens = $state<Token[]>([]);
  let households = $state<Household[]>([]);
  let err = $state('');
  let creating = $state(false);
  let name = $state('');
  let picked = $state<string[]>([]);
  let granted = $state<Scope[]>(['chores:read', 'chores:complete']);
  let days = $state('365');
  let secret = $state('');
  let revoking = $state<Token | null>(null);
  let busy = $state(false);
  let cerr = $state('');

  async function load(): Promise<void> {
    try {
      [tokens, households] = await Promise.all([api<Token[]>('GET', '/account/tokens'), api<Household[]>('GET', '/households')]);
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  function openCreate(): void {
    name = '';
    picked = households.length === 1 ? [households[0]!.id] : [];
    granted = ['chores:read', 'chores:complete'];
    days = '365';
    cerr = '';
    creating = true;
  }

  function close(): void {
    creating = false;
    secret = ''; // the secret leaves memory with the dialog
  }

  async function create(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!picked.length) return void (cerr = t('tokens.needHousehold'));
    if (!granted.length) return void (cerr = t('tokens.needScope'));
    busy = true;
    cerr = '';
    try {
      const made = await withStepUp(() =>
        api<Token>('POST', '/account/tokens', { name, scopes: granted, households: picked, expiresInDays: days ? Number(days) : null }),
      );
      secret = made.token ?? '';
      await load();
    } catch (x) {
      cerr = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function revoke(): Promise<void> {
    if (!revoking) return;
    busy = true;
    try {
      await api('DELETE', `/account/tokens/${revoking.id}`);
      toast(t('tokens.revoked'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
      revoking = null;
    }
    await load();
  }
</script>

<AssistantConnect {households} oncreated={load} />

<ConnectedApps />

<section class="card" aria-labelledby="a-tokens">
  <h2 id="a-tokens">{t('tokens.title')}</h2>
  <p class="muted">{t('tokens.intro')}</p>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if tokens.length === 0}
    <p class="muted">{t('tokens.none')}</p>
  {:else}
    <ul class="list">
      {#each tokens as k (k.id)}
        <li>
          <span>
            <strong>{k.name}</strong>
            {#each k.scopes as s (s)}<span class="badge">{t(scopes.find((x) => x.id === s)?.short ?? 'tokens.shortRead')}</span>{' '}{/each}
            <br /><span class="muted">{k.households.map((h) => h.name).join(', ')}</span>
            <br /><span class="muted">
              {t('tokens.created', { date: when(k.createdAt) })} · {k.lastUsedAt ? t('tokens.lastUsed', { date: when(k.lastUsedAt) }) : t('tokens.neverUsed')} ·
              {k.expiresAt ? t('tokens.expiresOn', { date: when(k.expiresAt) }) : t('tokens.neverExpires')}
            </span>
          </span>
          <Button variant="danger" onclick={() => (revoking = k)}>{t('tokens.revoke')}</Button>
        </li>
      {/each}
    </ul>
  {/if}
  {#if households.length === 0}
    <p class="hint">{t('tokens.needHouseholdFirst')}</p>
  {:else}
    <div class="mt-md"><Button onclick={openCreate}>{t('tokens.create')}</Button></div>
  {/if}
</section>

<Dialog open={creating} title={secret ? t('tokens.createdTitle') : t('tokens.create')} onclose={close}>
  {#if secret}
    <div class="stack">
      <p class="mono" aria-label={t('tokens.secretLabel')}>{secret}</p>
      <p>{t('tokens.once')}</p>
      <div class="row">
        <Button onclick={() => copyText(secret)}>{t('common.copy')}</Button>
        <Button variant="quiet" onclick={close}>{t('common.done')}</Button>
      </div>
    </div>
  {:else}
    <form class="stack" onsubmit={create}>
      <Field label={t('tokens.name')} hint={t('tokens.nameHint')} bind:value={name} required maxlength={100} />
      <fieldset class="plain-fieldset stack-sm">
        <legend class="label">{t('tokens.households')}</legend>
        {#each households as h (h.id)}
          <label class="check"><input type="checkbox" value={h.id} bind:group={picked} />{h.name}</label>
        {/each}
      </fieldset>
      <fieldset class="plain-fieldset stack-sm">
        <legend class="label">{t('tokens.permissions')}</legend>
        {#each scopes as s (s.id)}
          <label class="check"><input type="checkbox" value={s.id} bind:group={granted} />{t(s.label)}</label>
        {/each}
        <p class="hint">{t('tokens.permissionsHint')}</p>
      </fieldset>
      <Field label={t('tokens.expires')}>
        {#snippet children({ id })}
          <select {id} bind:value={days}>
            {#each expiries as x (x.days)}<option value={x.days}>{t(x.label)}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <div aria-live="polite"><Alert message={cerr} /></div>
      <div class="row">
        <Button type="submit" {busy}>{t('tokens.create')}</Button>
        <Button variant="quiet" onclick={close}>{t('common.cancel')}</Button>
      </div>
    </form>
  {/if}
</Dialog>

<Confirm
  open={!!revoking}
  title={t('tokens.revokeTitle', { name: revoking?.name ?? '' })}
  message={t('tokens.revokeWarn')}
  confirmLabel={t('tokens.revoke')}
  {busy}
  onconfirm={revoke}
  oncancel={() => (revoking = null)}
/>
