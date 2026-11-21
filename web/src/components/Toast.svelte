<script lang="ts">
  import { toasts } from '../lib/toast.svelte';
</script>

<div class="toasts" role="status" aria-live="polite">
  {#each toasts as x (x.id)}<p class="toast">{x.text}</p>{/each}
</div>
