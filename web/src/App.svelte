<script lang="ts">
  import Logo from './components/Logo.svelte';
  import Toast from './components/Toast.svelte';
  import Lazy from './components/Lazy.svelte';
  import StepUp from './components/StepUp.svelte';
  import { t } from './lib/i18n';
  import { navigate, route } from './lib/router';
  import { app, bootstrap, currentHousehold, selectHousehold, signOut } from './lib/session.svelte';
  import Home from './screens/Home.svelte';
  import Invite from './screens/Invite.svelte';
  import NotFound from './screens/NotFound.svelte';
  import Setup from './screens/Setup.svelte';
  import SignIn from './screens/SignIn.svelte';

  void bootstrap();

  const invite = $derived($route.match(/^\/invite\/([^/]+)$/)?.[1]);
  const choreId = $derived($route.match(/^\/chores\/([^/]+)$/)?.[1]);
  const editId = $derived($route.match(/^\/chores\/([^/]+)\/edit$/)?.[1]);
  const hhId = $derived($route.match(/^\/households\/([^/]+)$/)?.[1]);
  const isPublic = $derived($route === '/signin' || $route === '/setup' || !!invite);

  // Route guard: signed-out users only see public screens.
  $effect(() => {
    if (!app.ready) return;
    if (!app.user && !isPublic) {
      if (app.setupNeeded) navigate('/setup', true);
      else navigate(`/signin?next=${encodeURIComponent($route + location.search)}`, true);
    } else if (!app.user && app.setupNeeded && $route === '/signin') {
      navigate('/setup', true);
    } else if (app.user && ($route === '/signin' || $route === '/setup')) {
      navigate('/', true);
    }
  });

  async function out(): Promise<void> {
    await signOut();
    navigate('/signin');
  }
  const here = (p: string) => ($route === p ? 'page' : undefined);
</script>

<header class="bar">
  <nav class="nav" aria-label={t('nav.label')}>
    <a class="brand" href="/"><Logo />{t('app.name')}</a>
    {#if app.user}
      {#if app.households.length}
        <div class="switcher">
          <label class="visually-hidden" for="hh-switch">{t('nav.household')}</label>
          <select id="hh-switch" value={app.hhId} onchange={(e) => selectHousehold(e.currentTarget.value)}>
            {#each app.households as h (h.id)}<option value={h.id}>{h.name}</option>{/each}
          </select>
        </div>
      {/if}
      <ul>
        <li><a href="/households/new" aria-current={here('/households/new')}>{t('nav.newHousehold')}</a></li>
        <li><a href="/account" aria-current={here('/account')}>{t('nav.account')}</a></li>
        {#if app.user.isAdmin}
          <li><a href="/admin" aria-current={here('/admin')}>{t('nav.admin')}</a></li>
        {/if}
        <li><button type="button" class="btn btn-quiet" onclick={out}>{t('nav.signOut')}</button></li>
      </ul>
    {/if}
  </nav>
</header>

{#if app.user && app.hhId && !isPublic}
  <nav class="tabs" aria-label={t('nav.primary')}>
    <ul>
      <li><a href="/" aria-current={here('/')}><span aria-hidden="true">&#x2713;</span>{t('nav.home')}</a></li>
      <li><a href="/calendar" aria-current={here('/calendar')}><span aria-hidden="true">&#x25A6;</span>{t('nav.calendar')}</a></li>
      {#if currentHousehold()?.role !== 'guest'}
        <li><a href="/chores/new" aria-current={here('/chores/new')}><span aria-hidden="true">+</span>{t('nav.add')}</a></li>
      {/if}
      <li><a href="/households/{app.hhId}" aria-current={here(`/households/${app.hhId}`)}><span aria-hidden="true">&#x2699;</span>{t('nav.settings')}</a></li>
    </ul>
  </nav>
{/if}

<main id="main" class:with-tabs={app.user && app.hhId && !isPublic}>
  {#if !app.ready}
    <p role="status" class="muted">{t('common.loading')}</p>
  {:else if $route === '/signin'}
    {#if !app.user}<SignIn />{/if}
  {:else if $route === '/setup'}
    {#if !app.user && app.setupNeeded}<Setup />{:else if !app.user}<NotFound />{/if}
  {:else if invite}
    {#key invite}<Invite token={invite} />{/key}
  {:else if app.user}
    {#if $route === '/'}
      <Home />
    {:else if $route === '/calendar'}
      {#key app.hhId}<Lazy load={() => import('./screens/Calendar.svelte')} />{/key}
    {:else if $route === '/chores/new'}
      {#key app.hhId}<Lazy load={() => import('./screens/ChoreEditor.svelte')} />{/key}
    {:else if choreId}
      {#key `${app.hhId}/${choreId}`}<Lazy load={() => import('./screens/ChoreDetail.svelte')} props={{ id: choreId }} />{/key}
    {:else if editId}
      {#key `${app.hhId}/${editId}`}<Lazy load={() => import('./screens/ChoreEditor.svelte')} props={{ id: editId }} />{/key}
    {:else if $route === '/households/new'}
      <Lazy load={() => import('./screens/NewHousehold.svelte')} />
    {:else if hhId}
      {#key hhId}<Lazy load={() => import('./screens/Household.svelte')} props={{ id: hhId }} />{/key}
    {:else if $route === '/account'}
      <Lazy load={() => import('./screens/Account.svelte')} />
    {:else if $route === '/admin' && app.user.isAdmin}
      <Lazy load={() => import('./screens/Admin.svelte')} />
    {:else if $route === '/admin/updates' && app.user.isAdmin}
      <Lazy load={() => import('./screens/AdminUpdates.svelte')} />
    {:else}
      <NotFound />
    {/if}
  {/if}
</main>

<StepUp />
<Toast />

<style>
  .bar {
    position: sticky;
    inset-block-start: 0;
    background: color-mix(in srgb, var(--bg) 85%, transparent);
    backdrop-filter: saturate(180%) blur(20px);
    border-block-end: 1px solid var(--border);
    z-index: 1;
  }
  .nav {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
    max-inline-size: 960px;
    margin-inline: auto;
    padding-block: 4px;
    padding-inline: var(--space);
  }
  .brand {
    font-weight: 700;
    font-size: 1.25rem;
    color: var(--text);
    text-decoration: none;
    margin-inline-end: auto;
    min-block-size: var(--tap);
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  ul {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  ul a {
    display: inline-flex;
    align-items: center;
    min-block-size: var(--tap);
    padding-inline: 12px;
    border-radius: var(--radius);
    color: var(--text-2);
    text-decoration: none;
  }
  ul a[aria-current='page'] {
    color: var(--text);
    background: var(--surface-2);
    font-weight: 600;
  }
  main {
    max-inline-size: 960px;
    margin-inline: auto;
    padding-block: calc(var(--space) * 1.5);
    padding-inline: var(--space);
  }
  /* Phones: bottom tab bar. Desktop: sidebar at the inline start (mirrors in RTL). */
  .tabs {
    position: fixed;
    inset-inline: 0;
    inset-block-end: 0;
    z-index: 2;
    background: var(--surface);
    border-block-start: 1px solid var(--border);
    padding-block-end: env(safe-area-inset-bottom);
  }
  .tabs ul {
    flex-wrap: nowrap;
    justify-content: space-around;
    gap: 0;
  }
  .tabs li {
    flex: 1;
  }
  .tabs a {
    flex-direction: column;
    justify-content: center;
    inline-size: 100%;
    padding-inline: 4px;
    font-size: 0.8125rem;
    min-block-size: 56px;
  }
  main.with-tabs {
    padding-block-end: 160px; /* room for the tab bar and a toast above it */
  }
  @media (min-width: 768px) {
    .tabs {
      inset-block: 56px 0;
      inset-inline: 0 auto;
      inline-size: 200px;
      padding-block-start: 8px;
      border-block-start: 0;
      border-inline-end: 1px solid var(--border);
    }
    .tabs ul {
      flex-direction: column;
      padding-inline: 8px;
      gap: 4px;
    }
    .tabs a {
      flex-direction: row;
      justify-content: flex-start;
      gap: 10px;
      font-size: 1rem;
      min-block-size: var(--tap);
      padding-inline: 12px;
    }
    main.with-tabs {
      padding-block-end: calc(var(--space) * 1.5);
      margin-inline: 200px auto;
    }
  }
</style>
