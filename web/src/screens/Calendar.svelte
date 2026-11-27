<script lang="ts">
  import { href } from '../lib/base';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import { api } from '../lib/api';
  import { addDays, fmt, parseYmd, today, weekStart, ymd } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { app } from '../lib/session.svelte';

  type Entry = { choreId: string; name: string; dates: string[] };

  let view = $state<'month' | 'week'>('month');
  let sel = $state(today());
  let entries = $state<Entry[]>([]);
  let err = $state('');
  let focus = false;

  const ws = weekStart();
  const dow = (s: string) => parseYmd(s).getUTCDay();
  const weekOf = (s: string) => addDays(s, -((dow(s) - ws + 7) % 7));
  const first = $derived(`${sel.slice(0, 8)}01`);
  const start = $derived(view === 'week' ? weekOf(sel) : weekOf(first));
  const rows = $derived.by(() => {
    if (view === 'week') return 1;
    const dim = new Date(Date.UTC(+sel.slice(0, 4), +sel.slice(5, 7), 0)).getUTCDate();
    return Math.ceil(((dow(first) - ws + 7) % 7 + dim) / 7);
  });
  const days = $derived(Array.from({ length: rows * 7 }, (_, i) => addDays(start, i)));
  const byDate = $derived.by(() => {
    const m: Record<string, Entry[]> = {};
    for (const e of entries) for (const d of e.dates) (m[d] ??= []).push(e);
    return m;
  });
  const heads = $derived(Array.from({ length: 7 }, (_, i) => addDays(weekOf('2024-01-03'), i)));
  const title = $derived(
    view === 'month'
      ? fmt(sel, { month: 'long', year: 'numeric' })
      : `${fmt(days[0], { dateStyle: 'medium' })} – ${fmt(days[6], { dateStyle: 'medium' })}`,
  );

  const from = $derived(days[0]);
  const to = $derived(days[days.length - 1]);
  $effect(() => {
    const hh = app.hhId;
    if (!hh) return;
    api<{ items: Entry[] }>('GET', `/households/${hh}/calendar?from=${from}&to=${to}`)
      .then((r) => ((entries = r.items), (err = '')))
      .catch((x) => (err = failure(x).message));
  });
  $effect(() => {
    void [sel, days];
    if (focus) {
      focus = false;
      document.getElementById(`cal-${sel}`)?.focus();
    }
  });

  function shift(dir: number): void {
    if (view === 'week') sel = addDays(sel, 7 * dir);
    else sel = ymd(new Date(Date.UTC(+sel.slice(0, 4), +sel.slice(5, 7) - 1 + dir, 1)));
  }

  function key(e: KeyboardEvent): void {
    const col = (dow(sel) - ws + 7) % 7;
    const step: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7, Home: -col, End: 6 - col };
    const rtl = document.documentElement.dir === 'rtl';
    let n = step[e.key];
    if (rtl && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) n = -n;
    if (e.key === 'PageUp' || e.key === 'PageDown') {
      e.preventDefault();
      focus = true;
      shift(e.key === 'PageUp' ? -1 : 1);
    } else if (n !== undefined) {
      e.preventDefault();
      focus = true;
      sel = addDays(sel, n);
    }
  }
  const list = $derived(byDate[sel] ?? []);
</script>

<h1>{t('nav.calendar')}</h1>
<div class="row toolbar">
  <div class="row">
    <Button variant="quiet" aria-label={t('calendar.prev')} onclick={() => shift(-1)}>&lsaquo;</Button>
    <Button variant="quiet" aria-label={t('calendar.next')} onclick={() => shift(1)}>&rsaquo;</Button>
    <Button variant="quiet" onclick={() => (sel = today())}>{t('home.dueToday')}</Button>
  </div>
  <div role="group" aria-label={t('calendar.view')} class="row">
    <button type="button" class="btn btn-quiet" aria-pressed={view === 'month'} onclick={() => (view = 'month')}>{t('calendar.month')}</button>
    <button type="button" class="btn btn-quiet" aria-pressed={view === 'week'} onclick={() => (view = 'week')}>{t('calendar.week')}</button>
  </div>
</div>
<h2 id="cal-title" aria-live="polite">{title}</h2>
<div aria-live="polite"><Alert message={err} /></div>

<table role="grid" aria-labelledby="cal-title" aria-readonly="true" class="cal" class:week={view === 'week'}>
  <thead>
    <tr>{#each heads as d (d)}<th role="columnheader" scope="col" abbr={fmt(d, { weekday: 'long' })}>{fmt(d, { weekday: 'short' })}</th>{/each}</tr>
  </thead>
  <tbody>
    {#each { length: rows } as _, r (r)}
      <tr>
        {#each days.slice(r * 7, r * 7 + 7) as d (d)}
          {@const n = byDate[d]?.length ?? 0}
          <td
            role="gridcell"
            id="cal-{d}"
            tabindex={d === sel ? 0 : -1}
            aria-selected={d === sel}
            aria-current={d === today() ? 'date' : undefined}
            class:out={view === 'month' && d.slice(0, 7) !== sel.slice(0, 7)}
            class:sel={d === sel}
            class:today={d === today()}
            onclick={() => (sel = d)}
            onkeydown={(e) => {
              key(e);
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                sel = d;
              }
            }}
          >
            <span class="num" aria-hidden="true">{+d.slice(8)}</span>
            <span class="visually-hidden">{fmt(d, { dateStyle: 'full' })}, {t('home.count', { count: n })}</span>
            {#if n}
              {#if view === 'week'}
                <ul aria-hidden="true">{#each byDate[d] as e (e.choreId)}<li>{e.name}</li>{/each}</ul>
              {:else}<span class="pill" aria-hidden="true">{n}</span>{/if}
            {/if}
          </td>
        {/each}
      </tr>
    {/each}
  </tbody>
</table>

<section class="card mt-md" aria-labelledby="cal-day" aria-live="polite">
  <h2 id="cal-day">{fmt(sel, { dateStyle: 'full' })}</h2>
  {#if list.length}
    <ul class="list">{#each list as e (e.choreId)}<li><a href={href(`/chores/${e.choreId}`)}>{e.name}</a></li>{/each}</ul>
  {:else}
    <p class="muted">{t('calendar.none')}</p>
  {/if}
  <p class="hint mt-md"><a href={href('/settings/household-connections')}>{t('calendar.feeds')}</a></p>
</section>

<style>
  .toolbar {
    justify-content: space-between;
    margin-block-end: 8px;
  }
  .cal {
    inline-size: 100%;
    table-layout: fixed;
    border-collapse: collapse;
    background: var(--surface);
    border-radius: var(--radius);
  }
  th {
    padding-block: 6px;
    font-size: 0.8125rem;
    color: var(--text-2);
  }
  td {
    border: 1px solid var(--border);
    min-block-size: 56px;
    padding: 4px;
    vertical-align: start;
    cursor: pointer;
    overflow: hidden;
  }
  .week td {
    min-block-size: 140px;
  }
  td.out {
    color: var(--text-2);
    background: var(--surface-2);
  }
  td.sel {
    outline: 3px solid var(--accent);
    outline-offset: -3px;
  }
  td:focus-visible {
    outline: 3px solid var(--focus);
    outline-offset: -3px;
  }
  td.today .num {
    background: var(--accent);
    color: var(--accent-text);
    border-radius: 999px;
    padding-inline: 6px;
    font-weight: 700;
  }
  .pill {
    display: inline-block;
    margin-inline-start: 4px;
    padding-inline: 6px;
    border-radius: 999px;
    border: 1px solid var(--border);
    font-size: 0.75rem;
    font-weight: 600;
  }
  td ul {
    list-style: none;
    margin: 4px 0 0;
    padding: 0;
    font-size: 0.75rem;
    display: grid;
    gap: 2px;
  }
  td li {
    background: var(--surface-2);
    border-radius: 4px;
    padding-inline: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
