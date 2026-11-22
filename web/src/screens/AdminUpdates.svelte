<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { formatDate, t } from '../lib/i18n';
  import { withStepUp } from '../lib/stepup.svelte';

  type U = { running: string; latest: string; newer: boolean; changelog: string; checkedAt: string | null; enabled: boolean };
  let u = $state<U | null>(null);
  let err = $state('');

  async function load(): Promise<void> {
    try {
      u = await api<U>('GET', '/admin/updates');
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function toggle(on: boolean): Promise<void> {
    err = '';
    try {
      await withStepUp(() => api('PATCH', '/admin/settings', { updateCheck: on }));
      await load();
    } catch (x) {
      err = failure(x).message;
      await load();
    }
  }
</script>

<h1>{t('updates.title')}</h1>
<a href="/admin">{t('updates.back')}</a>
<section class="card" aria-labelledby="up-h">
  <h2 id="up-h">{t('updates.version')}</h2>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if u}
    <p>{t('updates.running', { v: u.running })}</p>
    {#if !u.enabled}
      <p class="muted">{t('updates.off')}</p>
    {:else if !u.latest}
      <p class="muted">{t('updates.never')}</p>
    {:else}
      <p role="status">{u.newer ? t('updates.newer', { v: u.latest }) : t('updates.current', { v: u.latest })}</p>
      {#if u.checkedAt}<p class="muted">{t('updates.checked', { date: formatDate(new Date(u.checkedAt)) })}</p>{/if}
      {#if u.changelog}
        <h3>{t('updates.changelog')}</h3>
        <pre class="changelog">{u.changelog}</pre>
      {/if}
    {/if}
    <label class="check"><input type="checkbox" checked={u.enabled} onchange={(e) => toggle(e.currentTarget.checked)} />{t('updates.enable')}</label>
    <p class="hint">{t('updates.privacy')}</p>
  {:else if !err}
    <p role="status" class="muted">{t('common.loading')}</p>
  {/if}
</section>

<style>
  .changelog {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    max-block-size: 24rem;
    overflow: auto;
    font-family: inherit;
  }
</style>
