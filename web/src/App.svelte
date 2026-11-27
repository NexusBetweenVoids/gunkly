<script lang="ts">
  import HeaderMenu from './components/HeaderMenu.svelte';
  import Logo from './components/Logo.svelte';
  import Toast from './components/Toast.svelte';
  import Lazy from './components/Lazy.svelte';
  import { stepUp } from './lib/stepup.svelte';
  import { api } from './lib/api';
  import { failure } from './lib/errors';
  import { has, t, type Key } from './lib/i18n';
  import { navigate, route } from './lib/router.svelte';
  import { app, bootstrap, currentHousehold, selectHousehold, signOut } from './lib/session.svelte';
  import { toast } from './lib/toast.svelte';
  import Home from './screens/Home.svelte';
  import NotFound from './screens/NotFound.svelte';
  import SignIn from './screens/SignIn.svelte';

  void bootstrap();

  const invite = $derived(route.path.match(/^\/invite\/([^/]+)$/)?.[1]);
  const choreId = $derived(route.path.match(/^\/chores\/([^/]+)$/)?.[1]);
  const editId = $derived(route.path.match(/^\/chores\/([^/]+)\/edit$/)?.[1]);
  const hhMatch = $derived(route.path.match(/^\/households\/([^/]+)(?:\/([a-z]+))?$/));
  const hhId = $derived(hhMatch?.[1] !== 'new' ? hhMatch?.[1] : undefined);
  // Settings screens take an optional group: /settings/security, /settings/household-people, /admin/limits.
  const settingsMatch = $derived(route.path.match(/^\/settings(?:\/([a-z-]+))?$/));
  // Addresses from before Settings was one screen (bookmarks, emails): /account/x, /households/{id}/x.
  const accountMatch = $derived(route.path.match(/^\/account(?:\/([a-z]+))?$/));
  const adminMatch = $derived(route.path.match(/^\/admin(?:\/([a-z]+))?$/));
  // Public screens: signed-out users may see them (the last two also signed in: emailed links).
  const PUBLIC = ['/signin', '/setup', '/signup', '/forgot-password', '/reset-password', '/verify-email'];
  const isPublic = $derived(PUBLIC.includes(route.path) || !!invite);

  // Route guard: signed-out users only see public screens.
  $effect(() => {
    if (!app.ready) return;
    if (!app.user && !isPublic) {
      if (app.setupNeeded) navigate('/setup', true);
      else navigate(`/signin?next=${encodeURIComponent(route.path + location.search)}`, true);
    } else if (!app.user && app.setupNeeded && route.path === '/signin') {
      navigate('/setup', true);
    } else if (app.user && ['/signin', '/setup', '/signup', '/forgot-password'].includes(route.path)) {
      navigate('/', true);
    }
  });

  // Old settings addresses move to /settings, keeping the group and the query (?sso=…).
  $effect(() => {
    if (!app.ready || !app.user) return;
    if (accountMatch) {
      navigate(`/settings${accountMatch[1] ? `/${accountMatch[1]}` : ''}${location.search}`, true);
    } else if (hhId && app.households.some((h) => h.id === hhId)) {
      selectHousehold(hhId);
      navigate(`/settings/household${hhMatch?.[2] ? `-${hhMatch[2]}` : ''}${location.search}`, true);
    }
  });

  // Single sign-on flows that start from a signed-in page (linking, step-up) come back to it with
  // ?sso=<result>: say what happened once and drop the parameter. (The sign-in page shows its own.)
  $effect(() => {
    if (!app.ready || !app.user) return;
    const u = new URL(location.href);
    const code = u.searchParams.get('sso');
    if (!code) return;
    u.searchParams.delete('sso');
    history.replaceState(null, '', u.pathname + u.search);
    if (code === 'linked' || code === 'stepup') toast(t(code === 'linked' ? 'sso.linked' : 'sso.stepup'));
    else toast(t(has(`err.sso_${code}`) ? (`err.sso_${code}` as Key) : 'err.sso_failed'));
  });

  async function out(): Promise<void> {
    await signOut();
    navigate('/signin');
  }
  const here = (p: string) => (route.path === p || (p !== '/' && route.path.startsWith(`${p}/`)) ? 'page' : undefined);

  // Hosted only: signed in with an address that isn't confirmed yet (self-hosted
  // instances never require it; the Account screen has a quiet note instead).
  const unverified = $derived(!!app.user && app.instance.hostedMode && !!app.user.email && !app.user.emailVerified);
  let resending = $state(false);
  async function resend(): Promise<void> {
    resending = true;
    try {
      await api<unknown>('POST', '/account/email/resend');
      toast(t('banner.sent'));
    } catch (x) {
      toast(failure(x).message);
    } finally {
      resending = false;
    }
  }
</script>

<header class="bar">
  <nav class="nav" aria-label={t('nav.label')}>
    <a class="brand" href="/"><Logo /><span class="brand-name">{t('app.name')}</span></a>
    {#if app.user}
      {#if app.households.length === 1}
        <p class="switcher hh-name" data-testid="hh-name"><span class="visually-hidden">{t('nav.household')}: </span>{app.households[0]?.name}</p>
      {:else if app.households.length > 1}
        <div class="switcher">
          <label class="visually-hidden" for="hh-switch">{t('nav.household')}</label>
          <select id="hh-switch" value={app.hhId} onchange={(e) => selectHousehold(e.currentTarget.value)}>
            {#each app.households as h (h.id)}<option value={h.id}>{h.name}</option>{/each}
          </select>
        </div>
      {/if}
      <ul class="links">
        <!-- With a household, Settings is in the tab bar. -->
        {#if !app.hhId}<li><a href="/settings" aria-current={here('/settings')}>{t('nav.settings')}</a></li>{/if}
        {#if app.user.isAdmin}
          <li><a href="/admin" aria-current={here('/admin')}>{t('nav.admin')}</a></li>
        {/if}
        <li><button type="button" class="btn btn-quiet" onclick={out}>{t('nav.signOut')}</button></li>
      </ul>
      <HeaderMenu onsignout={out} />
    {/if}
  </nav>
</header>

{#if unverified && app.user}
  <div class="notice" role="status">
    <p>{t('banner.unverifiedHosted', { email: app.user.email ?? '' })}</p>
    <button type="button" class="btn btn-quiet" disabled={resending} onclick={resend}>{t('banner.resend')}</button>
  </div>
{/if}

{#if app.user && app.hhId && !isPublic}
  <nav class="tabs" aria-label={t('nav.primary')}>
    <ul>
      <li><a href="/" aria-current={here('/')}><span aria-hidden="true">&#x2713;</span>{t('nav.home')}</a></li>
      <li><a href="/calendar" aria-current={here('/calendar')}><span aria-hidden="true">&#x25A6;</span>{t('nav.calendar')}</a></li>
      {#if currentHousehold()?.role !== 'guest'}
        <li><a href="/chores/new" aria-current={here('/chores/new')}><span aria-hidden="true">+</span>{t('nav.add')}</a></li>
      {/if}
      <li><a href="/settings" aria-current={here('/settings')}><span aria-hidden="true">&#x2699;</span>{t('nav.settings')}</a></li>
    </ul>
  </nav>
{/if}

<main id="main" class:with-tabs={app.user && app.hhId && !isPublic}>
  {#if !app.ready}
    <p role="status" class="muted">{t('common.loading')}</p>
  {:else if route.path === '/signin'}
    {#if !app.user}<SignIn />{/if}
  {:else if route.path === '/setup'}
    {#if !app.user && app.setupNeeded}<Lazy load={() => import('./screens/Setup.svelte')} ns={['auth']} />{:else if !app.user}<NotFound />{/if}
  {:else if route.path === '/signup'}
    {#if !app.user}<Lazy load={() => import('./screens/Signup.svelte')} ns={['auth']} />{/if}
  {:else if route.path === '/forgot-password'}
    {#if !app.user}<Lazy load={() => import('./screens/ForgotPassword.svelte')} ns={['auth']} />{/if}
  {:else if route.path === '/reset-password'}
    <Lazy load={() => import('./screens/ResetPassword.svelte')} ns={['auth']} />
  {:else if route.path === '/verify-email'}
    <Lazy load={() => import('./screens/VerifyEmail.svelte')} ns={['auth']} />
  {:else if invite}
    {#key invite}<Lazy load={() => import('./screens/Invite.svelte')} ns={['auth']} props={{ token: invite }} />{/key}
  {:else if app.user}
    {#if route.path === '/'}
      <Home />
    {:else if route.path === '/calendar'}
      {#key app.hhId}<Lazy load={() => import('./screens/Calendar.svelte')} ns={['calendar']} />{/key}
    {:else if route.path === '/chores/new'}
      {#key app.hhId}<Lazy load={() => import('./screens/ChoreEditor.svelte')} ns={['chores']} />{/key}
    {:else if choreId}
      {#key `${app.hhId}/${choreId}`}<Lazy load={() => import('./screens/ChoreDetail.svelte')} ns={['chores']} props={{ id: choreId }} />{/key}
    {:else if editId}
      {#key `${app.hhId}/${editId}`}<Lazy load={() => import('./screens/ChoreEditor.svelte')} ns={['chores']} props={{ id: editId }} />{/key}
    {:else if route.path === '/welcome'}
      <Lazy load={() => import('./screens/Welcome.svelte')} ns={['household', 'chores', 'account']} />
    {:else if route.path === '/households/new'}
      <Lazy load={() => import('./screens/NewHousehold.svelte')} ns={['household']} />
    {:else if settingsMatch}
      {#key app.hhId}<Lazy load={() => import('./screens/Settings.svelte')} ns={['account', 'household', 'channels']} props={{ section: settingsMatch[1] }} />{/key}
    {:else if accountMatch || (hhId && app.households.some((h) => h.id === hhId))}
      <p role="status" class="muted">{t('common.loading')}</p>
    {:else if route.path === '/setup/guide' && app.user.isAdmin}
      <Lazy load={() => import('./screens/admin/SetupGuide.svelte')} ns={['admin']} />
    {:else if adminMatch && app.user.isAdmin}
      <Lazy load={() => import('./screens/Admin.svelte')} ns={['admin']} props={{ section: adminMatch[1] }} />
    {:else}
      <NotFound />
    {/if}
  {/if}
</main>

{#if stepUp.open}<Lazy load={() => import('./components/StepUp.svelte')} ns={['auth']} />{/if}
<Toast />

<style>
  .notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 4px 12px;
    max-inline-size: 960px;
    margin-inline: auto;
    padding-block: 8px;
    padding-inline: var(--space);
    background: var(--surface-2);
    border-block-end: 1px solid var(--border);
  }
  .notice p {
    margin: 0;
  }
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
    flex-wrap: nowrap;
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
    flex: none;
    min-block-size: var(--tap);
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .switcher {
    flex: 1 1 auto;
    min-inline-size: 0;
  }
  .hh-name {
    margin: 0;
    padding-inline: 12px;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .switcher select {
    min-inline-size: 0;
    text-overflow: ellipsis;
  }
  /* Phones: logo, switcher and one menu button; the links live in the menu. */
  .links {
    display: none;
  }
  .brand-name {
    position: absolute;
    clip-path: inset(50%);
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
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
  /* A modal dialog owns the screen: the tab bar steps aside. */
  :global(body:has(dialog[open])) .tabs {
    visibility: hidden;
  }
  @media (min-width: 768px) {
    .nav {
      flex-wrap: wrap;
    }
    .links {
      display: flex;
    }
    .brand-name {
      position: static;
      clip-path: none;
      inline-size: auto;
      block-size: auto;
      overflow: visible;
    }
    .switcher {
      flex: 0 1 260px;
    }
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
