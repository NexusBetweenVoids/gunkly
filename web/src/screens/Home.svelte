<script lang="ts">
  import type { Component } from 'svelte';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Initials from '../components/Initials.svelte';
  import { api } from '../lib/api';
  import { dueText } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { formatDate, t, type Key } from '../lib/i18n';
  import { app, currentHousehold } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { Area, Chore, Member, Status } from '../lib/types';

  const groups: { id: Status; title: Key; empty: Key }[] = [
    { id: 'overdue', title: 'home.overdue', empty: 'home.empty.overdue' },
    { id: 'today', title: 'home.dueToday', empty: 'home.empty.dueToday' },
    { id: 'thisWeek', title: 'home.thisWeek', empty: 'home.empty.thisWeek' },
    { id: 'later', title: 'home.later', empty: 'home.empty.later' },
  ];

  let chores = $state<Chore[]>([]);
  let areas = $state<Area[]>([]);
  let members = $state<Member[]>([]);
  let mine = $state(false);
  let archived = $state(false);
  let area = $state('');
  let q = $state(new URLSearchParams(location.search).get('q') ?? ''); // lives in the URL only
  function setQ(v: string): void {
    q = v;
    const u = new URL(location.href);
    if (v) u.searchParams.set('q', v);
    else u.searchParams.delete('q');
    history.replaceState(null, '', u);
  }
  let loaded = $state(false);
  // The offer of reminders on this device is its own small chunk, loaded once a household shows.
  let Prompt = $state.raw<Component | null>(null);
  $effect(() => {
    if (hh && !Prompt) void import('../components/PushPrompt.svelte').then((m) => (Prompt = m.default));
  });
  let err = $state('');
  let doneErr = $state('');

  const hh = $derived(app.hhId);
  const canEdit = $derived(currentHousehold()?.role !== 'guest');
  const base = $derived(`/households/${hh}`);
  const areasOn = $derived(!!currentHousehold()?.areasEnabled);

  async function load(): Promise<void> {
    err = '';
    try {
      const q = archived ? '?archived=1' : '';
      [chores, areas, members] = await Promise.all([
        api<Chore[]>('GET', `${base}/chores${q}`),
        areasOn ? api<Area[]>('GET', `${base}/areas`) : Promise.resolve([] as Area[]),
        api<Member[]>('GET', `${base}/members`),
      ]);
    } catch (x) {
      err = failure(x).message;
    }
    loaded = true;
  }
  $effect(() => {
    void [hh, archived, areasOn];
    if (hh) void load();
  });
  $effect(() => {
    void hh;
    mine = false; // a new household starts on Everyone
  });
  // "Mine" (assigned to me or to anyone) only differs from Everyone when someone else has a chore of their own.
  const mineOk = (c: Chore) => !c.assignees.length || c.assignees.includes(app.user?.id ?? '');
  const mineUseful = $derived(chores.some((c) => !mineOk(c)));
  const showMine = $derived(mine && mineUseful);
  $effect(() => {
    if (loaded && !mineUseful) mine = false;
  });

  const areaName = (id: string | null) => !areasOn ? '' : areas.find((a) => a.id === id)?.name ?? '';
  const who = (c: Chore) => c.assignees.map((id) => members.find((m) => m.userId === id)?.displayName ?? '').filter(Boolean);
  const needle = $derived(q.trim().toLocaleLowerCase());
  const matches = (c: Chore) => !needle || [c.name, areaName(c.areaId), ...who(c)].some((s) => s.toLocaleLowerCase().includes(needle));
  const scoped = $derived(showMine ? chores.filter(mineOk) : chores);
  const visible = $derived(scoped.filter((c) => (!areasOn || !area || c.areaId === area) && matches(c)));
  const rest = $derived(visible.filter((c) => !groups.some((g) => g.id === c.status)));
  const swap = (c: Chore) => (chores = chores.map((x) => (x.id === c.id ? c : x)));

  async function done(c: Chore): Promise<void> {
    doneErr = '';
    const i = chores.findIndex((x) => x.id === c.id);
    chores = chores.filter((x) => x.id !== c.id); // optimistic
    try {
      const r = await api<{ completion: { id: string }; chore: Chore }>('POST', `${base}/chores/${c.id}/complete`);
      if (!archived) chores = [...chores, r.chore];
      toast(t('home.completed', { name: c.name }), {
        label: t('common.undo'),
        run: () => undo(c, r.completion.id),
      });
    } catch (x) {
      chores = [...chores.slice(0, i), c, ...chores.slice(i)]; // roll back
      doneErr = failure(x).message;
    }
  }

  async function undo(c: Chore, id: string): Promise<void> {
    try {
      swap(await api<Chore>('POST', `${base}/chores/${c.id}/completions/${id}/revert`));
      toast(t('home.undone', { name: c.name }));
    } catch (x) {
      doneErr = failure(x).message;
    }
  }
</script>

<h1>{t('home.title')}</h1>
<p class="muted">{t('home.today', { date: formatDate(new Date(), { dateStyle: 'full', timeZone: currentHousehold()?.timezone }) })}</p>

{#if !hh}
  <section class="card stack" aria-labelledby="h-welcome">
    <h2 id="h-welcome">{t('home.welcome')}</h2>
    <p class="muted">{t('home.noHousehold')}</p>
    <p><a class="btn" href="/households/new">{t('household.createFirst')}</a></p>
  </section>
{:else}
  {#if Prompt}<Prompt />{/if}
  <div class="search">
    <label class="visually-hidden" for="chore-search">{t('home.search')}</label>
    <input id="chore-search" type="search" autocomplete="off" placeholder={t('home.search')} value={q} oninput={(e) => setQ(e.currentTarget.value)} />
    {#if q}
      <button type="button" class="btn btn-quiet clear" aria-label={t('home.searchClear')} onclick={() => { setQ(''); document.getElementById('chore-search')?.focus(); }}>&times;</button>
    {/if}
  </div>
  <div class="row toolbar">
    {#if mineUseful}
      <div role="group" aria-label={t('home.whose')} class="seg">
        <button type="button" class="btn btn-quiet" aria-pressed={!showMine} onclick={() => (mine = false)}>{t('home.everyone')}</button>
        <button type="button" class="btn btn-quiet" aria-pressed={showMine} onclick={() => (mine = true)}>{t('home.mine')}</button>
      </div>
    {/if}
    <label class="check"><input type="checkbox" bind:checked={archived} />{t('home.archived')}</label>
  </div>
  {#if areasOn && areas.length}
    <div role="group" aria-label={t('home.areas')} class="row chips">
      <button type="button" class="btn btn-quiet chip" aria-pressed={!area} onclick={() => (area = '')}>{t('home.allAreas')}</button>
      {#each areas as a (a.id)}
        <button type="button" class="btn btn-quiet chip" aria-pressed={area === a.id} onclick={() => (area = a.id)}>{a.name}</button>
      {/each}
    </div>
  {/if}

  <div aria-live="polite"><Alert message={err || doneErr} /></div>

  {#snippet item(c: Chore)}
    <li class="chore">
      <a class="chore-main" href="/chores/{c.id}">
        <strong>{c.name}</strong>
        <span class="muted" class:late={c.status === 'overdue'}>
          {#if areaName(c.areaId)}{areaName(c.areaId)} &middot; {/if}
          {#if c.status === 'paused'}{t('status.paused')}{:else if c.status === 'none' || c.status === 'archived'}{t(c.status === 'none' ? 'status.none' : 'home.archived')}{:else}{dueText(c.dueDate)}{/if}
        </span>
      </a>
      <Initials names={who(c)} />
      {#if canEdit && !archived && c.status !== 'paused' && c.status !== 'none'}
        <Button onclick={() => done(c)} aria-label={t('home.doneFor', { name: c.name })}>{t('common.done')}</Button>
      {/if}
    </li>
  {/snippet}

  {#if !loaded}
    <p role="status" class="muted">{t('common.loading')}</p>
  {:else if archived}
    <section class="card mt-md">
      <h2>{t('home.archived')}</h2>
      {#if visible.length}<ul class="list">{#each visible as c (c.id)}{@render item(c)}{/each}</ul>{:else}<p class="muted">{t('home.empty.archived')}</p>{/if}
    </section>
  {:else if !scoped.length}
    <section class="card mt-md">
      <p>{showMine ? t('home.empty.mine') : t('home.empty.all')}</p>
      {#if canEdit}<a class="btn" href="/chores/new">{t('chore.add')}</a>{/if}
    </section>
  {:else if needle && !visible.length}
    <p class="muted mt-md" role="status">{t('home.searchNone')}</p>
  {:else}
    <div class="groups">
      {#each groups as g (g.id)}
        {@const items = visible.filter((c) => c.status === g.id).sort((a, b) => a.dueDate.localeCompare(b.dueDate))}
        <section class="card" aria-labelledby="group-{g.id}">
          <h2 id="group-{g.id}">
            {t(g.title)}
            <span class="count muted">{t('home.count', { count: items.length })}</span>
          </h2>
          {#if items.length}<ul class="list">{#each items as c (c.id)}{@render item(c)}{/each}</ul>{:else}<p class="muted empty">{t(g.empty)}</p>{/if}
        </section>
      {/each}
      {#if rest.length}
        <section class="card" aria-labelledby="group-rest">
          <h2 id="group-rest">{t('home.other')}</h2>
          <ul class="list">{#each rest as c (c.id)}{@render item(c)}{/each}</ul>
        </section>
      {/if}
    </div>
  {/if}
{/if}

<style>
  .groups {
    display: grid;
    gap: var(--space);
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr));
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
  .search {
    position: relative;
    margin-block-start: var(--space);
  }
  .search input {
    padding-inline-end: var(--tap);
  }
  .search input::-webkit-search-cancel-button {
    display: none;
  }
  .clear {
    position: absolute;
    inset-block-start: 0;
    inset-inline-end: 0;
    font-size: 1.5rem;
  }
  .toolbar {
    justify-content: space-between;
    margin-block: var(--space) 8px;
  }
  .seg {
    display: flex;
    gap: 4px;
  }
  .chips {
    margin-block-end: var(--space);
  }
  /* Phones: one horizontally scrolling row instead of several wrapped rows. */
  @media (max-width: 767px) {
    .chips {
      flex-wrap: nowrap;
      overflow-x: auto;
      scrollbar-width: none;
      padding-block: 2px; /* keep focus rings visible */
      padding-inline: 2px;
    }
    .chips :global(.chip) {
      flex: none;
    }
  }
  .chore {
    flex-wrap: nowrap;
  }
  .chore-main {
    flex: 1;
    display: grid;
    min-inline-size: 0;
    min-block-size: var(--tap);
    align-content: center;
    color: var(--text);
    text-decoration: none;
  }
  .late {
    color: var(--danger);
    font-weight: 600;
  }
</style>
