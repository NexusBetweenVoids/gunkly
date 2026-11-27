<script lang="ts" module>
  // Set by the banner's link: the list scrolls itself into view once it has loaded.
  let reveal = false;
</script>

<script lang="ts">
  // Recent security alert events, from the admin health check. Each event kind has its own text,
  // secEvents.<event>, filled with the account, the household and the event's details (meta).
  // "banner": one line above the admin groups, linking to the list; "list": the full list (Users group).
  // Events the viewing admin caused are marked "by you"; when all of them are, the banner shows on
  // the Users group only, so an admin's own routine changes don't follow them around.
  import { tick } from 'svelte';
  import { href } from '../../lib/base';
  import { navigate } from '../../lib/router.svelte';
  import Icon from '../../components/Icon.svelte';
  import { api } from '../../lib/api';
  import { formatDate, has, t, type Key } from '../../lib/i18n';

  let { view, section = '' }: { view: 'banner' | 'list'; section?: string } = $props();

  type Ev = { event: string; householdId: string; username: string; byYou?: boolean; meta?: Record<string, string>; at: string };
  let events = $state<Ev[]>([]);
  // A failure here only means no notice: the rest of the page works without it.
  api<{ securityEvents?: Ev[] }>('GET', '/admin/health')
    .then(async (h) => {
      events = h.securityEvents ?? [];
      if (view === 'list' && reveal) await show();
    })
    .catch(() => {});
  async function show(): Promise<void> {
    reveal = false;
    await tick();
    const el = document.getElementById('sec-title');
    el?.scrollIntoView({ block: 'start' });
    el?.focus({ preventScroll: true });
  }
  function review(e: MouseEvent): void {
    e.preventDefault();
    const list = document.getElementById('sec-title');
    if (list) {
      reveal = true;
      void show();
      return;
    }
    reveal = true;
    navigate('/admin/users');
  }
  const when = (s: string) => formatDate(new Date(s), { dateStyle: 'medium', timeStyle: 'short' });
  const text = (e: Ev) => {
    const k = `secEvents.${e.event}`;
    const meta = { ...(e.meta ?? {}) };
    const sk = `secEvents.state.${meta.state}`;
    if (meta.state && has(sk)) meta.state = t(sk as Key);
    const params = { ...meta, username: e.username || t('secEvents.deletedUser'), household: e.householdId };
    return has(k) ? t(k as Key, params) : e.event;
  };
  const mine = $derived(events.filter((e) => e.byYou).length);
  const allMine = $derived(events.length > 0 && mine === events.length);
  const onUsers = $derived(section === '' || section === 'users');
  const showBanner = $derived(events.length > 0 && (!allMine || onUsers));
</script>

{#if view === 'banner'}
  {#if showBanner}
    <p class="banner" class:calm={allMine} role="status">
      <Icon name="shield" />
      <span class="what">
        {t('secEvents.banner', { count: events.length })}{#if mine}<span class="muted"> · {allMine ? t('secEvents.allByYou', { count: mine }) : t('secEvents.someByYou', { count: mine })}</span>{/if}
      </span>
      <a href={href('/admin/users')} onclick={review}>{t('secEvents.review')} <span aria-hidden="true">&rsaquo;</span></a>
    </p>
  {/if}
{:else if events.length}
  <section class="card stack-sm" id="security-events" aria-labelledby="sec-title">
    <h2 id="sec-title" class="card-title" tabindex="-1">{t('secEvents.title')}</h2>
    <p class="muted">{t('secEvents.intro')}</p>
    <ul class="list">
      {#each events as e, i (i)}
        <li>
          <span class="ev">{text(e)}{#if e.byYou}&nbsp;<span class="badge">{t('secEvents.byYou')}</span>{/if}</span>
          <span class="muted when">{when(e.at)}</span>
        </li>
      {/each}
    </ul>
    <p class="hint">{t('secEvents.hint')}</p>
  </section>
{/if}

<style>
  .banner {
    --warn: #8a5a00;
    --warn-bg: #f7e6c4;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 12px;
    margin-block: 0 var(--space);
    padding-block: 10px;
    padding-inline: 16px;
    border-radius: var(--radius);
    background: var(--warn-bg);
    color: var(--text);
  }
  .banner.calm {
    --warn: var(--text-2);
    --warn-bg: var(--surface);
  }
  @media (prefers-color-scheme: dark) {
    :global(:root:not([data-theme='light'])) .banner:not(.calm) {
      --warn: #f0c674;
      --warn-bg: #4a3f2a;
    }
  }
  :global(:root[data-theme='dark']) .banner:not(.calm) {
    --warn: #f0c674;
    --warn-bg: #4a3f2a;
  }
  .banner :global(svg) {
    flex: none;
    color: var(--warn);
  }
  .what {
    flex: 1;
    min-inline-size: 12rem;
  }
  .banner a {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-block-size: var(--tap);
    font-weight: 700;
    text-decoration: none;
  }
  #sec-title:focus {
    outline: none;
  }
  /* Each event on its own line with its time underneath. */
  .list > li {
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
  }
  .when {
    font-size: 0.875rem;
  }
  .ev {
    overflow-wrap: anywhere;
  }
</style>
