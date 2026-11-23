<script lang="ts" module>
  import type { IconName } from './Icon.svelte';
  export type Group = { id: string; label: string; hint: string; icon: IconName };
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  // A settings screen split into groups, each at its own URL (base/group).
  // Phones: the bare URL lists the groups; a group URL shows only that group
  // with a back link. Wide screens: the list stays on the side and the bare URL
  // shows the first group.
  let {
    title,
    base,
    groups,
    section,
    intro,
    children,
  }: { title: string; base: string; groups: Group[]; section?: string; intro?: Snippet; children: Snippet<[string]> } = $props();

  const picked = $derived(groups.find((g) => g.id === section));
  const active = $derived(picked ?? groups[0]);
</script>

<h1>{title}</h1>
{@render intro?.()}
<div class="settings" class:bare={!picked}>
  <nav aria-label={title}>
    <ul>
      {#each groups as g (g.id)}
        <li>
          <a href="{base}/{g.id}" aria-current={g.id === active?.id ? 'page' : undefined}>
            <Icon name={g.icon} />
            <span><strong>{g.label}</strong><small>{g.hint}</small></span>
          </a>
        </li>
      {/each}
    </ul>
  </nav>
  {#if active}
    <div class="content">
      <a class="back" href={base}><span aria-hidden="true">&lsaquo;</span> {title}</a>
      <h2 class="visually-hidden">{active.label}</h2>
      {#key active.id}<div class="sections">{@render children(active.id)}</div>{/key}
    </div>
  {/if}
</div>

<style>
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
    nav small {
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
