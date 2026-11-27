<script lang="ts" module>
  import { href } from '../lib/base';
  import type { IconName } from './Icon.svelte';
  export type Group = { id: string; label: string; hint: string; icon: IconName };
  /** A labelled run of groups in the list (Settings: "You", then the household). */
  export type GroupSet = { id: string; label: string; hint?: string; groups: Group[] };
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  // A settings screen split into groups, each at its own URL (base/group).
  // Phones: the bare URL lists the groups; a group URL shows only that group
  // with a back link. Wide screens: the list stays on the side and the bare URL
  // shows the first group. Pass groups, or sets for a list with headings.
  let {
    title,
    base,
    groups = [],
    sets,
    section,
    intro,
    children,
  }: { title: string; base: string; groups?: Group[]; sets?: GroupSet[]; section?: string; intro?: Snippet; children: Snippet<[string]> } = $props();

  const lists = $derived(sets ?? [{ id: 'all', label: '', groups }]);
  const all = $derived(lists.flatMap((l) => l.groups));
  const picked = $derived(all.find((g) => g.id === section));
  const active = $derived(picked ?? all[0]);
</script>

<h1>{title}</h1>
{@render intro?.()}
<div class="settings" class:bare={!picked}>
  <nav aria-label={title}>
    {#each lists as l (l.id)}
      {#if l.label}
        <h2 class="set" id="set-{l.id}">{l.label}</h2>
        {#if l.hint}<p class="set-hint">{l.hint}</p>{/if}
      {/if}
      <ul aria-labelledby={l.label ? `set-${l.id}` : undefined}>
        {#each l.groups as g (g.id)}
          <li>
            <a href={href(`${base}/${g.id}`)} aria-current={g.id === active?.id ? 'page' : undefined}>
              <Icon name={g.icon} />
              <span><strong>{g.label}</strong><small>{g.hint}</small></span>
            </a>
          </li>
        {/each}
      </ul>
    {/each}
  </nav>
  {#if active}
    <div class="content">
      <a class="back" href={href(base)}><span aria-hidden="true">&lsaquo;</span> {title}</a>
      <h2 class="visually-hidden">{active.label}</h2>
      {#key active.id}<div class="sections">{@render children(active.id)}</div>{/key}
    </div>
  {/if}
</div>

<style>
  .set {
    margin-block: var(--space) 8px;
    padding-inline: 4px;
    font-size: 0.8125rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-2);
  }
  .set:first-child {
    margin-block-start: 0;
  }
  .set-hint {
    margin-block: -6px 8px;
    padding-inline: 4px;
    font-size: 0.8125rem;
    color: var(--text-2);
  }
  nav ul {
    list-style: none;
    margin: 0;
    padding: 0;
    background: var(--surface);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow);
    overflow: hidden;
  }
  nav li + li {
    border-block-start: 1px solid var(--border);
  }
  nav a {
    display: flex;
    align-items: center;
    gap: 14px;
    min-block-size: var(--tap);
    padding-block: 12px;
    padding-inline: 16px;
    color: var(--text);
    text-decoration: none;
  }
  nav a::after {
    content: '\203A';
    margin-inline-start: auto;
    color: var(--text-2);
    font-size: 1.25rem;
  }
  :global([dir='rtl']) nav a::after {
    content: '\2039';
  }
  nav a :global(svg) {
    flex: none;
    color: var(--text-2);
  }
  nav span {
    display: grid;
    gap: 2px;
  }
  nav small {
    color: var(--text-2);
    font-size: 0.8125rem;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: var(--tap);
    margin-block-end: 4px;
    text-decoration: none;
    font-weight: 600;
  }

  /* Phones: either the list or one group. */
  @media (max-width: 47.99rem) {
    .bare .content,
    .settings:not(.bare) nav {
      display: none;
    }
  }

  /* Wide screens: list on the side, the open group next to it. */
  @media (min-width: 48rem) {
    .settings {
      display: grid;
      grid-template-columns: 15rem minmax(0, 1fr);
      gap: calc(var(--space) * 1.5);
      align-items: start;
    }
    nav {
      position: sticky;
      inset-block-start: var(--space);
    }
    nav a::after {
      content: none;
    }
    nav a small {
      display: none;
    }
    nav a[aria-current='page'] {
      background: var(--surface-2);
      font-weight: 600;
    }
    nav a[aria-current='page'] :global(svg) {
      color: var(--accent);
    }
    .back {
      display: none;
    }
  }
</style>
