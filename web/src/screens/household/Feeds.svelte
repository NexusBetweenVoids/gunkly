<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { fmtTime } from '../../lib/dates';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { toast } from '../../lib/toast.svelte';

  let { id }: { id: string } = $props();
  const base = $derived(`/households/${id}/feeds`);
  let feeds = $state<{ id: string; createdAt: string }[]>([]);
  let shown = $state<{ url: string } | null>(null);
  let revoking = $state('');
  let err = $state('');

  async function load(): Promise<void> {
    try {
      feeds = await api('GET', base);
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function run(fn: () => Promise<{ url: string } | void>): Promise<void> {
    err = '';
    shown = null;
    try {
      const r = await fn();
      if (r) shown = { url: new URL(r.url, location.origin).href };
      revoking = '';
      await load();
    } catch (x) {
      err = failure(x).message;
    }
  }
</script>

<section class="card" aria-labelledby="h-feeds">
  <h2 id="h-feeds">{t('feeds.title')}</h2>
  <p class="muted">{t('feeds.intro')}</p>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if shown}
    <div class="stack-sm" role="status">
      <p class="label">{t('feeds.newUrl')}</p>
      <p class="mono">{shown.url}</p>
      <p class="hint">{t('feeds.once')}</p>
      <div><Button variant="quiet" onclick={() => copyText(shown!.url)}>{t('common.copy')}</Button></div>
      <details>
        <summary>{t('feeds.howTo')}</summary>
        <ul>
          <li>{t('feeds.google')}</li>
          <li>{t('feeds.apple')}</li>
          <li>{t('feeds.outlook')}</li>
        </ul>
      </details>
    </div>
  {/if}
  {#if feeds.length}
    <ul class="list">
      {#each feeds as f (f.id)}
        <li>
          <span>{t('feeds.created', { date: fmtTime(f.createdAt) })}</span>
          <span class="row">
            <Button variant="quiet" onclick={() => run(() => api('POST', `${base}/${f.id}/regenerate`))}>{t('feeds.regenerate')}</Button>
            <Button variant="danger" onclick={() => (revoking = f.id)}>{t('feeds.revoke')}</Button>
          </span>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted">{t('feeds.none')}</p>
  {/if}
  <div class="mt-md"><Button onclick={() => run(() => api('POST', base))}>{t('feeds.create')}</Button></div>
</section>

<Confirm
  open={!!revoking}
  title={t('feeds.revokeTitle')}
  message={t('feeds.revokeWarn')}
  confirmLabel={t('feeds.revoke')}
  onconfirm={() => run(() => api<void>('DELETE', `${base}/${revoking}`).then(() => toast(t('feeds.revoked'))))}
  oncancel={() => (revoking = '')}
/>
