<script lang="ts">
  import { t } from '../lib/i18n';
  import { route } from '../lib/router.svelte';
  import { app } from '../lib/session.svelte';

  let { onsignout }: { onsignout: () => void } = $props();
  let open = $state(false);
  let btn = $state<HTMLButtonElement>();
  let panel = $state<HTMLElement>();
  const uid = $props.id();
  const here = (p: string) => (route.path === p || route.path.startsWith(`${p}/`) ? 'page' : undefined);
  const items = () => [...(panel?.querySelectorAll<HTMLElement>('a, button') ?? [])];

  function close(refocus: boolean): void {
    open = false;
    if (refocus) btn?.focus();
  }
  function keydown(e: KeyboardEvent): void {
    if (!open) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      close(true);
      return;
    }
    const list = items();
    const i = list.indexOf(document.activeElement as HTMLElement);
    const to = { ArrowDown: (i + 1) % list.length, ArrowUp: (i - 1 + list.length) % list.length, Home: 0, End: list.length - 1 }[e.key];
    if (to !== undefined) {
      e.preventDefault();
      list[to]?.focus();
    }
  }
  function focusout(e: FocusEvent): void {
    const next = e.relatedTarget as Node | null;
    if (open && next && !panel?.contains(next) && next !== btn) open = false;
  }
  $effect(() => {
    void route.path;
    open = false;
  });
  $effect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      const n = e.target as Node;
      if (!panel?.contains(n) && !btn?.contains(n)) open = false;
    };
    document.addEventListener('pointerdown', away);
    return () => document.removeEventListener('pointerdown', away);
  });
  function toggle(): void {
    open = !open;
    if (open) queueMicrotask(() => items()[0]?.focus());
  }
</script>

<div class="menu" onkeydown={keydown} onfocusout={focusout} role="presentation">
  <button bind:this={btn} type="button" class="btn btn-quiet trigger" aria-expanded={open} aria-controls="menu-{uid}" onclick={toggle}>
    <span class="visually-hidden">{t('nav.menu')}</span>
    <svg aria-hidden="true" viewBox="0 0 24 24" width="22" height="22"><path d="M4 7h16M4 12h16M4 17h16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" /></svg>
  </button>
  <ul bind:this={panel} id="menu-{uid}" class="panel" hidden={!open}>
    <!-- With a household, Settings is in the tab bar. -->
        {#if !app.hhId}<li><a href="/settings" aria-current={here('/settings')}>{t('nav.settings')}</a></li>{/if}
    {#if app.user?.isAdmin}
      <li><a href="/admin" aria-current={here('/admin')}>{t('nav.admin')}</a></li>
    {/if}
    <li><button type="button" onclick={onsignout}>{t('nav.signOut')}</button></li>
  </ul>
</div>

<style>
  .menu {
    position: relative;
  }
  .panel {
    position: absolute;
    inset-block-start: 100%;
    inset-inline-end: 0;
    z-index: 3;
    min-inline-size: 200px;
    margin: 4px 0 0;
    padding: 6px;
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: var(--shadow);
  }
  .panel[hidden] {
    display: none;
  }
  .panel a,
  .panel button {
    display: flex;
    align-items: center;
    inline-size: 100%;
    min-block-size: var(--tap);
    padding-inline: 12px;
    border: 0;
    border-radius: var(--radius);
    background: transparent;
    color: var(--text);
    text-align: start;
    text-decoration: none;
    cursor: pointer;
  }
  .panel a:hover,
  .panel button:hover,
  .panel a[aria-current='page'] {
    background: var(--surface-2);
  }
  .panel a[aria-current='page'] {
    font-weight: 600;
  }
  @media (min-width: 768px) {
    .menu {
      display: none;
    }
  }
</style>
