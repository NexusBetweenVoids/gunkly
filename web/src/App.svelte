<script lang="ts">
  import { t, type Key } from './lib/i18n';
  import { route } from './lib/router';
  import { applyTheme, storedTheme, type Theme } from './lib/theme';
  import Home from './screens/Home.svelte';
  import SignIn from './screens/SignIn.svelte';
  import NotFound from './screens/NotFound.svelte';

  const order: Theme[] = ['system', 'light', 'dark'];
  const label: Record<Theme, Key> = { system: 'theme.system', light: 'theme.light', dark: 'theme.dark' };
  let theme = $state<Theme>(storedTheme());

  function cycleTheme(): void {
    theme = order[(order.indexOf(theme) + 1) % order.length];
    applyTheme(theme);
  }
</script>

<header class="bar">
  <nav class="nav" aria-label={t('nav.label')}>
    <a class="brand" href="/">{t('app.name')}</a>
    <ul>
      <li><a href="/" aria-current={$route === '/' ? 'page' : undefined}>{t('nav.home')}</a></li>
      <li>
        <a href="/signin" aria-current={$route === '/signin' ? 'page' : undefined}>{t('nav.signIn')}</a>
      </li>
    </ul>
    <button type="button" class="btn btn-quiet" onclick={cycleTheme}>
      {t('theme.toggle', { mode: t(label[theme]) })}
    </button>
  </nav>
</header>

<main id="main">
  {#if $route === '/'}
    <Home />
  {:else if $route === '/signin'}
    <SignIn />
  {:else}
    <NotFound />
  {/if}
</main>

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
    align-items: center;
    gap: 8px;
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
