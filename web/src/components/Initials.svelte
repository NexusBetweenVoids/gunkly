<script lang="ts">
  import { initials } from '../lib/dates';

  let { names }: { names: string[] } = $props();
</script>

{#if names.length}
  <span class="avatars" title={names.join(', ')}>
    {#each names.slice(0, 3) as n (n)}<span class="avatar" aria-hidden="true">{initials(n)}</span>{/each}
    {#if names.length > 3}<span class="avatar" aria-hidden="true">+{names.length - 3}</span>{/if}
    <span class="visually-hidden">{names.join(', ')}</span>
  </span>
{/if}
