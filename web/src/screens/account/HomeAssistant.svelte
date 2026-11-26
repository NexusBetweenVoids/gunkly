<script lang="ts" module>
  // My Home Assistant links: they open the user's own Home Assistant (my.home-assistant.io
  // asks for its address once). hacs_repository opens the repository in the Marketplace (HA 2026.11+)
  // or HACS (older), offering to add it when it is not listed yet; config_flow_start
  // opens "Add integration" for Gunkly (offered next to a new key, with the address to paste).
  // Nothing about this server is sent along.
  export const HA_REPO = 'https://github.com/NexusBetweenVoids/gunkly-homeassistant';
  export const HA_HACS = 'https://my.home-assistant.io/redirect/hacs_repository/?owner=NexusBetweenVoids&repository=gunkly-homeassistant&category=integration';
  export const HA_ADD = 'https://my.home-assistant.io/redirect/config_flow_start/?domain=gunkly';
</script>

<script lang="ts">
  import Button from '../../components/Button.svelte';
  import { t } from '../../lib/i18n';
  import type { Household } from '../../lib/types';

  // Settings, Integrations: a guided setup for the Home Assistant integration.
  let { households, oncreate }: { households: Household[]; oncreate: () => void } = $props();
</script>

{#snippet logo()}
  <svg class="ha-logo" aria-hidden="true" viewBox="0 0 24 24" width="22" height="22">
    <path
      fill="currentColor"
      d="M22.939 10.627 13.061.749a1.505 1.505 0 0 0-2.121 0l-9.879 9.878C.478 11.21 0 12.363 0 13.187v9c0 .826.675 1.5 1.5 1.5h9.227l-4.063-4.062a2.034 2.034 0 0 1-.664.113c-1.13 0-2.05-.92-2.05-2.05s.92-2.05 2.05-2.05 2.05.92 2.05 2.05c0 .233-.041.456-.113.665l3.163 3.163V9.928a2.05 2.05 0 0 1-1.15-1.84c0-1.13.92-2.05 2.05-2.05s2.05.92 2.05 2.05a2.05 2.05 0 0 1-1.15 1.84v8.127l3.146-3.146A2.051 2.051 0 0 1 18 12.239c1.13 0 2.05.92 2.05 2.05s-.92 2.05-2.05 2.05c-.25 0-.488-.047-.709-.13L12.9 20.602v3.088h9.6c.825 0 1.5-.675 1.5-1.5v-9c0-.825-.477-1.977-1.061-2.561z"
    />
  </svg>
{/snippet}

<section class="card" aria-labelledby="a-ha">
  <h2 id="a-ha" class="with-logo">{@render logo()}{t('ha.title')}</h2>
  <p>{t('ha.intro')}</p>
  <ol class="steps stack">
    <li>
      <p>{t('ha.step1')}</p>
      <p><a class="btn ha-btn" href={HA_HACS} target="_blank" rel="noopener noreferrer">{@render logo()}{t('ha.install')}</a></p>
      <p class="hint">{t('ha.step1Hint')} <a href={HA_REPO} target="_blank" rel="noopener noreferrer">{t('ha.guide')}</a></p>
    </li>
    <li><p>{t('ha.step2')}</p></li>
    <li>
      <p>{t('ha.step3')}</p>
      {#if households.length === 0}
        <p class="hint">{t('tokens.needHouseholdFirst')}</p>
      {:else}
        <Button variant="quiet" onclick={oncreate}>{t('ha.createKey')}</Button>
      {/if}
    </li>
  </ol>
  <p class="hint">{t('ha.myHint')}</p>
</section>

<style>
  .with-logo {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .with-logo .ha-logo {
    color: #18bcf2;
  }
  .steps p {
    margin-block: 0 6px;
  }
  .ha-btn {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    background: #18bcf2;
    border-color: #18bcf2;
    color: #04222f;
  }
  .ha-btn:hover {
    background: #0fa6d8;
    border-color: #0fa6d8;
  }
</style>
