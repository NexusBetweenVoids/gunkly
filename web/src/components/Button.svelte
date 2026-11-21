<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';

  type Props = HTMLButtonAttributes & {
    variant?: 'primary' | 'quiet' | 'danger';
    busy?: boolean;
    children: Snippet;
  };
  let { variant = 'primary', busy = false, type = 'button', children, ...rest }: Props = $props();
</script>

<button
  {type}
  class="btn"
  class:btn-quiet={variant === 'quiet'}
  class:btn-danger={variant === 'danger'}
  aria-busy={busy || undefined}
  {...rest}
  disabled={busy || rest.disabled}
>
  {@render children()}
</button>
