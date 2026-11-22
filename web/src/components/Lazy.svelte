<script lang="ts">
  import type { Component } from 'svelte';
  import { loadMessages, t, type Area } from '../lib/i18n';
  import Alert from './Alert.svelte';

  // Loads a screen's code and its message areas (ns) on first use (dynamic import = its own chunk).
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  type Screen = Component<any>;
  let { load, ns = [], props = {} }: { load: () => Promise<{ default: Screen }>; ns?: Area[]; props?: Record<string, unknown> } = $props();
  let Loaded = $state.raw<Screen | null>(null);
  let failed = $state(false);
  // svelte-ignore state_referenced_locally -- a screen mounts once per route, so the loader never changes
  Promise.all([load(), loadMessages(...ns)]).then(([m]) => (Loaded = m.default), () => (failed = true));
</script>

{#if Loaded}
  <Loaded {...props} />
{:else if failed}
  <Alert message={t('err.network')} />
{:else}
  <p role="status" class="muted">{t('common.loading')}</p>
{/if}
