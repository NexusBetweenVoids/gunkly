<script lang="ts">
  import type { Snippet } from 'svelte';

  // One row in a "ways to ..." list (sign-in methods, reminder routes): name and a short hint, a state
  // pill and one action. The flow behind the action (a form, a list, new recovery codes) opens below
  // the row while `open` is set. Each row is a region named by its heading.
  let {
    id,
    name,
    hint = '',
    state = '',
    tone = '',
    testid,
    action,
    notes,
    open = false,
    children,
  }: {
    id: string;
    name: string;
    hint?: string;
    state?: string;
    tone?: '' | 'ok' | 'warn';
    testid?: string;
    action?: Snippet;
    notes?: Snippet;
    open?: boolean;
    children?: Snippet;
  } = $props();
</script>

<section class="method" aria-labelledby={id}>
  <div class="head">
    <div class="info">
      <h3 id={id}>{name}</h3>
      {#if hint}<p class="hint">{hint}</p>{/if}
      {@render notes?.()}
    </div>
    {#if state}<span class="badge state {tone}" data-testid={testid}>{state}</span>{/if}
    <div class="act">{@render action?.()}</div>
  </div>
  {#if open && children}
    <div class="panel" id="{id}-panel">{@render children()}</div>
  {/if}
</section>

<style>
  .method {
    padding-block: 14px;
    border-block-start: 1px solid var(--border);
  }
  .head {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    grid-template-areas: 'info state act';
    align-items: center;
    gap: 8px 14px;
  }
  .info {
    grid-area: info;
    display: grid;
    gap: 2px;
  }
  .info > :global(p) {
    margin: 0;
  }
  h3 {
    margin: 0;
    font-size: 1rem;
    font-weight: 700;
  }
  .state {
    grid-area: state;
    white-space: nowrap;
  }
  .act {
    grid-area: act;
    display: flex;
    justify-content: flex-end;
    min-inline-size: 8.5rem;
  }
  .act :global(.btn) {
    min-inline-size: 8.5rem;
  }
  .panel {
    margin-block-start: 12px;
    padding-block: 14px;
    padding-inline: 16px;
    border-radius: var(--radius);
    background: var(--bg);
  }
  /* Phones: the text gets the full width; the pill and the action share the line below. */
  @media (max-width: 35rem) {
    .head {
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-areas: 'info info' 'state act';
    }
    .state {
      justify-self: start;
    }
    .act {
      min-inline-size: 0;
    }
    .act:empty {
      display: none;
    }
  }
</style>
