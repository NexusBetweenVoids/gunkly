<script lang="ts">
  let { message, id }: { message: string; id?: string } = $props();
</script>

{#if message}<p class="alert" role="alert" {id}>{message}</p>{/if}
