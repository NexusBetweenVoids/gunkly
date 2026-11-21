<script lang="ts">
  import { formatDate, t, type Key } from '../lib/i18n';

  // Due-status groups, in display order. Chores arrive from /api/v1 later.
  const groups: { id: string; title: Key; empty: Key }[] = [
    { id: 'overdue', title: 'home.overdue', empty: 'home.empty.overdue' },
    { id: 'today', title: 'home.dueToday', empty: 'home.empty.dueToday' },
    { id: 'week', title: 'home.thisWeek', empty: 'home.empty.thisWeek' },
    { id: 'later', title: 'home.later', empty: 'home.empty.later' },
  ];
</script>

<h1>{t('home.title')}</h1>
<p class="muted">{t('home.today', { date: formatDate(new Date()) })}</p>

<div class="groups">
  {#each groups as g (g.id)}
    <section class="card" aria-labelledby="group-{g.id}">
      <h2 id="group-{g.id}">
        {t(g.title)}
        <span class="count muted">{t('home.count', { count: 0 })}</span>
      </h2>
      <p class="muted empty">{t(g.empty)}</p>
    </section>
  {/each}
</div>

<style>
  .groups {
    display: grid;
    gap: var(--space);
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr));
    margin-block-start: var(--space);
  }
  h2 {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
    font-size: 1.125rem;
  }
  .count {
    font-size: 0.875rem;
    font-weight: 400;
  }
  .empty {
    margin: 0;
  }
</style>
