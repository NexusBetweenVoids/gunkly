<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLInputAttributes } from 'svelte/elements';

  type Props = Omit<HTMLInputAttributes, 'children'> & {
    label: string;
    hint?: string;
    error?: string;
    children?: Snippet<[{ id: string; describedby: string | undefined; invalid: boolean }]>;
  };
  let { label, hint = '', error = '', value = $bindable(''), children, ...rest }: Props = $props();

  const uid = $props.id();
  const id = `f-${uid}`;
  const describedby = $derived([hint && `${id}-hint`, error && `${id}-err`].filter(Boolean).join(' ') || undefined);
</script>

<div class="field">
  <label for={id}>{label}</label>
  {#if children}
    {@render children({ id, describedby, invalid: !!error })}
  {:else}
    <input {id} bind:value aria-describedby={describedby} aria-invalid={error ? 'true' : undefined} {...rest} />
  {/if}
  {#if hint}<p class="hint" id="{id}-hint">{hint}</p>{/if}
  {#if error}<p class="error-text" id="{id}-err">{error}</p>{/if}
</div>
