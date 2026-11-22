<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';

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
    } catch (x) {
      err = failure(x).message;
    }
    await load();
  }
</script>

<section class="card" aria-labelledby="ad-updates">
  <h2 id="ad-updates">{t('updates.title')}</h2>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if u}
    <p>{t('updates.running', { v: u.running })}</p>
    {#if u.enabled && u.latest}
      <p role="status">{t(u.newer ? 'updates.newer' : 'updates.current', { v: u.latest })}</p>
      {#if u.changelog}<pre class="changelog">{u.changelog}</pre>{/if}
    {/if}
    <label class="check"><input type="checkbox" checked={u.enabled} onchange={(e) => toggle(e.currentTarget.checked)} />{t('updates.enable')}</label>
    <p class="hint">{t('updates.privacy')}</p>
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
