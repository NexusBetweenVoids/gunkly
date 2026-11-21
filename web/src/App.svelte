<script lang="ts">
  import Toast from './components/Toast.svelte';
  import StepUp from './components/StepUp.svelte';
  import { t } from './lib/i18n';
  import { navigate, route } from './lib/router';
  import { app, bootstrap, selectHousehold, signOut } from './lib/session.svelte';
  import Account from './screens/Account.svelte';
  import Admin from './screens/Admin.svelte';
  import Home from './screens/Home.svelte';
  import Household from './screens/Household.svelte';
  import Invite from './screens/Invite.svelte';
  import NewHousehold from './screens/NewHousehold.svelte';
  import NotFound from './screens/NotFound.svelte';
  import Setup from './screens/Setup.svelte';
  import SignIn from './screens/SignIn.svelte';

  void bootstrap();

  const invite = $derived($route.match(/^\/invite\/([^/]+)$/)?.[1]);
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
    <a class="brand" href="/">{t('app.name')}</a>
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
        <li><a href="/" aria-current={here('/')}>{t('nav.home')}</a></li>
        {#if app.hhId}
          <li><a href="/households/{app.hhId}" aria-current={here(`/households/${app.hhId}`)}>{t('nav.household')}</a></li>
        {/if}
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

<main id="main">
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
    {:else if $route === '/households/new'}
      <NewHousehold />
    {:else if hhId}
      <Household id={hhId} />
    {:else if $route === '/account'}
      <Account />
    {:else if $route === '/admin' && app.user.isAdmin}
      <Admin />
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
</style>
