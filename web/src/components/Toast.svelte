<script lang="ts">
  import { dismiss, toasts } from '../lib/toast.svelte';
</script>

<div class="toasts" role="status" aria-live="polite">
  {#each toasts as x (x.id)}
    <p class="toast">
      {x.text}
      {#if x.action}
        {@const run = x.action.run}
        <button type="button" class="toast-action" onclick={() => { dismiss(x.id); run(); }}>{x.action.label}</button>
      {/if}
    </p>
  {/each}
</div>
