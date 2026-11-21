<script lang="ts">
  import type { Snippet } from 'svelte';

  let { open, title, onclose, children }: { open: boolean; title: string; onclose: () => void; children: Snippet } =
    $props();
  let el = $state<HTMLDialogElement>();
  const uid = $props.id();
  const tid = `d-${uid}`;

  $effect(() => {
    if (!el) return;
    if (open && !el.open) el.showModal();
    else if (!open && el.open) el.close();
  });
</script>

<dialog bind:this={el} aria-labelledby={tid} {onclose}>
  {#if open}
    <h2 id={tid}>{title}</h2>
    {@render children()}
  {/if}
</dialog>
