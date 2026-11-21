<script lang="ts">
  import Field from './Field.svelte';

  type Opt = { value: string; label: string };
  let {
    label,
    value = $bindable(''),
    options,
    hint = '',
    onchange,
  }: { label: string; value: string; options: Opt[]; hint?: string; onchange?: () => void } = $props();
</script>

<Field {label} {hint}>
  {#snippet children({ id, describedby })}
    <select {id} aria-describedby={describedby} bind:value {onchange}>
      {#each options as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
    </select>
  {/snippet}
</Field>
