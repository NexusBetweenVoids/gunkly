<script lang="ts">
  import { api } from '../lib/api';
  import { fmtTime } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { t, type Key } from '../lib/i18n';
  import type { Delivery, Page } from '../lib/types';
  import Alert from './Alert.svelte';
  import Button from './Button.svelte';

  let { path, title, id }: { path: `/${string}`; title: string; id: string } = $props();
  const TYPE: Record<string, Key> = { before: 'delivery.before', on_due: 'delivery.on_due', overdue: 'delivery.overdue', test: 'delivery.test' };
  const STATUS: Record<string, Key> = { pending: 'delivery.pending', sent: 'delivery.sent', failed: 'delivery.failed' };
  const ICON: Record<string, string> = { pending: '⏳', sent: '✓', failed: '✗' };
  let items = $state<Delivery[]>([]);
  let next = $state('');
  let err = $state('');
  let loaded = $state(false);

  async function load(more = false): Promise<void> {
    try {
      const p = await api<Page<Delivery>>('GET', `${path}?limit=20${more ? `&before=${encodeURIComponent(next)}` : ''}`);
      items = more ? [...items, ...p.items] : p.items;
      next = p.next ?? '';
    } catch (x) {
      err = failure(x).message;
    }
    loaded = true;
  }
</script>

<section class="card" aria-labelledby="dl-{id}">
  <h2 id="dl-{id}">{title}</h2>
  {#if !loaded}
    <Button variant="quiet" onclick={() => load()}>{t('delivery.show')}</Button>
  {/if}
  <div aria-live="polite"><Alert message={err} /></div>
  {#if loaded && !items.length && !err}<p class="muted">{t('delivery.none')}</p>{/if}
  {#if items.length}
    <ul class="list">
      {#each items as d (d.id)}
        <li>
          <span>
            <strong>{d.choreName || t(TYPE[d.type] ?? 'delivery.test')}</strong>
            <span class="muted"> &middot; {t(TYPE[d.type] ?? 'delivery.test')} &middot; {d.channelLabel || d.kind || ''}</span><br />
            <span class="muted">{fmtTime(d.createdAt)}</span>
            {#if d.error}<br /><span class="error-text">{d.error}</span>{/if}
          </span>
          <span class="badge"><span aria-hidden="true">{ICON[d.status] ?? ''}</span> {t(STATUS[d.status] ?? 'delivery.pending')}</span>
        </li>
      {/each}
    </ul>
    {#if next}<Button variant="quiet" onclick={() => load(true)}>{t('common.more')}</Button>{/if}
  {/if}
</section>
