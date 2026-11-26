<script lang="ts">
  // A modal panel with a title and a Done button: a bottom sheet on phones, a centred dialog on wider
  // screens. Changes inside apply straight away; Done, Escape or a tap on the backdrop just close it.
  import type { Snippet } from 'svelte';
  import { t } from '../lib/i18n';

  let { open, title, onclose, children }: { open: boolean; title: string; onclose: () => void; children: Snippet } = $props();
  let el = $state<HTMLDialogElement>();
  const uid = $props.id();
  const tid = `s-${uid}`;

  $effect(() => {
    if (!el) return;
    if (open && !el.open) el.showModal();
    else if (!open && el.open) el.close();
  });

  // A click whose target is the <dialog> itself landed on the backdrop (the content fills the box).
  const backdrop = (e: MouseEvent) => {
    if (e.target === el) onclose();
  };
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog class="sheet" bind:this={el} aria-labelledby={tid} {onclose} onclick={backdrop}>
  {#if open}
    <div class="inner">
      <div class="head">
        <h2 id={tid}>{title}</h2>
        <button type="button" class="btn btn-quiet" onclick={onclose}>{t('common.done')}</button>
      </div>
      {@render children()}
    </div>
  {/if}
</dialog>

<style>
  .sheet {
    padding: 0;
    max-block-size: min(88dvh, 760px);
  }
  .inner {
    display: grid;
    gap: var(--space);
    padding: calc(var(--space) * 1.25);
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space);
  }
  .head h2 {
    margin: 0;
    font-size: 1.375rem;
  }
  @media (max-width: 767px) {
    .sheet {
      margin-block: auto 0;
      margin-inline: 0;
      inline-size: 100%;
      max-inline-size: 100%;
      border-end-start-radius: 0;
      border-end-end-radius: 0;
      padding-block-end: env(safe-area-inset-bottom);
    }
    .sheet[open] {
      animation: up var(--dur) var(--ease);
    }
  }
  @keyframes up {
    from {
      transform: translateY(24px);
      opacity: 0;
    }
  }
</style>
