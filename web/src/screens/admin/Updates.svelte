<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Markdown from '../../components/Markdown.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, t } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';

  type U = { running: string; latest: string; newer: boolean; ahead: boolean; checking: boolean; changelog: string; checkedAt: string | null; enabled: boolean };
  let u = $state<U | null>(null);
  let err = $state('');

  const RETRY_MS = 3000;
  const MAX_TRIES = 5;
  let tries = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  function apply(next: U): void {
    u = next;
    clearTimeout(timer);
    if (next.checking && tries < MAX_TRIES) {
      tries++;
      timer = setTimeout(() => void load(), RETRY_MS);
    }
  }

  async function load(): Promise<void> {
    try {
      apply(await api<U>('GET', '/admin/updates'));
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();
  $effect(() => () => clearTimeout(timer));

  async function checkNow(): Promise<void> {
    err = '';
    tries = 0;
    try {
      apply(await api<U>('POST', '/admin/updates/check'));
    } catch (x) {
      err = failure(x).message;
    }
  }

  async function toggle(on: boolean): Promise<void> {
    err = '';
    try {
      await withStepUp(() => api('PATCH', '/admin/settings', { updateCheck: on }));
    } catch (x) {
      err = failure(x).message;
    }
    tries = 0;
    await load();
  }
</script>

<section class="card" aria-labelledby="ad-updates">
  <h2 id="ad-updates">{t('updates.title')}</h2>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if u}
    <p>{t('updates.running', { v: u.running })}</p>
    {#if !u.enabled}
      <p class="muted">{t('updates.off')}</p>
    {:else}
      <p role="status" aria-live="polite">{u.checking ? t('updates.checking') : ''}</p>
    {/if}
    {#if u.enabled && !u.latest}
      {#if !u.checking}<p class="muted">{t('updates.never')}</p>{/if}
    {:else if u.enabled}
      {#if u.ahead}
        <p>{t('updates.unconfirmed', { running: u.running })}</p>
      {:else}
        <p>{t(u.newer ? 'updates.newer' : 'updates.current', { v: u.latest })}</p>
      {/if}
      {#if u.checkedAt}<p class="muted">{t('updates.checked', { date: formatDate(new Date(u.checkedAt)) })}</p>{/if}
      {#if u.changelog}
        <h3>{t('updates.changelog')}</h3>
        <div class="changelog" data-testid="changelog"><Markdown source={u.changelog} /></div>
      {/if}
    {/if}
    <p>
      <button type="button" class="btn" disabled={!u.enabled || u.checking} onclick={checkNow}>{t('updates.checkNow')}</button>
      {#if !u.enabled}<span class="hint">{t('updates.checkOffHint')}</span>{/if}
    </p>
    <label class="check"><input type="checkbox" checked={u.enabled} onchange={(e) => toggle(e.currentTarget.checked)} />{t('updates.enable')}</label>
    <p class="hint">{t('updates.privacy')}</p>
  {/if}
</section>

<style>
  .changelog {
    max-block-size: 24rem;
    overflow: auto;
  }
</style>
