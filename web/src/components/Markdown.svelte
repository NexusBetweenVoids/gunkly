<script lang="ts">
  // Renders the Markdown subset of lib/markdown.ts with plain Svelte markup: text is always a text
  // node (never {@html}), links are http(s) only and open in a new tab without a referrer.
  import { parse, type Block, type Inline } from '../lib/markdown';

  /** Headings start at this level (the notes sit under the page's own h2/h3). */
  let { source, headingStart = 4 }: { source: string; headingStart?: number } = $props();
  const blocks = $derived(parse(source));
  // The shallowest heading in the notes maps to headingStart; deeper ones follow, capped at h6.
  const top = $derived(Math.min(6, ...blocks.map((b) => (b.t === 'h' ? b.level : 6))));
  const tag = (level: number): string => `h${Math.min(6, headingStart + level - top)}`;
</script>

{#snippet inl(nodes: Inline[])}{#each nodes as n, i (i)}{#if n.t === 'text'}{n.v}{:else if n.t === 'code'}<code>{n.v}</code>{:else if n.t === 'strong'}<strong>{@render inl(n.c)}</strong>{:else if n.t === 'em'}<em>{@render inl(n.c)}</em>{:else}<a href={n.href} target="_blank" rel="noopener noreferrer nofollow">{@render inl(n.c)}</a>{/if}{/each}{/snippet}

{#snippet many(list: Block[])}
  {#each list as b, i (i)}
    {#if b.t === 'h'}
      <svelte:element this={tag(b.level)} class="md-h">{@render inl(b.c)}</svelte:element>
    {:else if b.t === 'p'}
      <p>{@render inl(b.c)}</p>
    {:else if b.t === 'code'}
      <pre><code>{b.v}</code></pre>
    {:else if b.t === 'hr'}
      <hr />
    {:else if b.t === 'ul'}
      <ul>
        {#each b.items as it, j (j)}<li>{@render inl(it.c)}{#if it.sub.length}{@render many(it.sub)}{/if}</li>{/each}
      </ul>
    {:else}
      <ol>
        {#each b.items as it, j (j)}<li>{@render inl(it.c)}{#if it.sub.length}{@render many(it.sub)}{/if}</li>{/each}
      </ol>
    {/if}
  {/each}
{/snippet}

<div class="md">{@render many(blocks)}</div>

<style>
  .md {
    overflow-wrap: anywhere;
  }
  .md :global(.md-h) {
    margin-block: 1em 0.4em;
    font-size: 1rem;
  }
  .md :global(.md-h:first-child) {
    margin-block-start: 0;
  }
  .md :global(ul),
  .md :global(ol) {
    margin-block: 0.4em;
    padding-inline-start: 1.4em;
  }
  .md :global(li + li) {
    margin-block-start: 0.3em;
  }
  .md :global(pre) {
    overflow-x: auto;
    white-space: pre;
  }
</style>
