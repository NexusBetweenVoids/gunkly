<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, has, t, type Key } from '../../lib/i18n';

  type Ev = { event: string; ip: string; createdAt: string };
  let events = $state<Ev[]>([]);
  let err = $state('');

  const label = (ev: string) => (has(`audit.${ev}`) ? t(`audit.${ev}` as Key) : ev);

  api<Ev[]>('GET', '/account/audit')
    .then((r) => (events = r))
    .catch((x) => (err = failure(x).message));
</script>

<section class="card" aria-labelledby="a-audit">
  <h2 id="a-audit">{t('audit.title')}</h2>
  <p class="muted">{t('audit.intro')}</p>
  <Alert message={err} />
  {#if events.length === 0 && !err}
    <p class="muted">{t('audit.none')}</p>
  {:else}
    <ul class="list events">
      {#each events as e, i (i)}
        <li>
          <strong>{label(e.event)}</strong>
          <span class="muted">{formatDate(new Date(e.createdAt), { dateStyle: 'medium', timeStyle: 'short' })} · {e.ip}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  /* Same text sizes as the "Ways to sign in" rows above. */
  .events strong {
    font-size: 1rem;
    font-weight: 700;
  }
  .events .muted {
    font-size: 0.875rem;
    color: var(--text-2);
  }
</style>
