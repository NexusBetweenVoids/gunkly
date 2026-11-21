<script lang="ts">
  import type { Component } from 'svelte';
  import { t } from '../lib/i18n';
  import Alert from './Alert.svelte';

  // Loads a screen's code on first use (dynamic import = its own chunk).
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  type Screen = Component<any>;
  let { load, props = {} }: { load: () => Promise<{ default: Screen }>; props?: Record<string, unknown> } = $props();
  // svelte-ignore state_referenced_locally -- a screen mounts once per route, so the loader never changes
  const loading = load();
</script>

{#await loading}
  <p role="status" class="muted">{t('common.loading')}</p>
{:then m}
  {@const Screen = m.default}
  <Screen {...props} />
{:catch}
  <Alert message={t('err.network')} />
{/await}
