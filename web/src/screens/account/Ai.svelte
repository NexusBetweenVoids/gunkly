<script lang="ts">
  import { t, type Key } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import AssistantConnect from './AssistantConnect.svelte';
  import ConnectedApps from './ConnectedApps.svelte';

  // Settings, AI: what connecting an assistant does, how to set it up, then the guided setup.
  const off = $derived(app.instance.mcpEnabled === false);
  const examples: Key[] = ['ai.example1', 'ai.example2', 'ai.example3'];
  const steps: Key[] = ['ai.step1', 'ai.step2', 'ai.step3', 'ai.step4'];
</script>

<section class="card hero" aria-labelledby="ai-title">
  <h2 id="ai-title">{t('ai.title')}</h2>
  <p class="lead">{t('ai.lead')}</p>
  <ul class="examples" aria-label={t('ai.examplesLabel')}>
    {#each examples as e (e)}<li>{t(e)}</li>{/each}
  </ul>
  <p class="muted">{t('ai.how')}</p>
  {#if off}
    <p class="muted" data-testid="ai-off">{t('mcp.disabled')}</p>
    {#if app.user?.isAdmin}<p><a href="/admin/connections">{t('ai.adminTurnOn')}</a></p>{/if}
  {:else}
    <h3>{t('ai.setupTitle')}</h3>
    <ol class="steps stack-sm">
      {#each steps as s (s)}<li>{t(s)}</li>{/each}
    </ol>
  {/if}
</section>

{#if !off}
  <AssistantConnect households={app.households} oncreated={() => {}} />
  <ConnectedApps />
  <p class="hint">{t('ai.keysHint')} <a href="/settings/integrations">{t('account.group.integrations')}</a></p>
{/if}

<style>
  .lead {
    font-size: 1.0625rem;
  }
  .examples {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .examples li {
    padding: 6px 12px;
    border-radius: 999px;
    background: var(--surface-2);
    border: 1px solid var(--border);
    font-size: 0.9375rem;
  }
</style>
