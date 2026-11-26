<script lang="ts">
  // Terms of use and privacy policy of the hosted service. The server only sends their addresses in
  // hosted mode (GUNKLY_TERMS_URL / GUNKLY_PRIVACY_URL), so self-hosted instances never show this.
  import { t } from '../lib/i18n';
  import { app } from '../lib/session.svelte';

  let { agree = false }: { agree?: boolean } = $props();
  const terms = $derived(app.instance.termsUrl);
  const privacy = $derived(app.instance.privacyUrl);
</script>

{#if terms || privacy}
  <p class="hint legal">
    {#if agree}{t('legal.agree')}{/if}
    {#if terms}<a href={terms} target="_blank" rel="noopener noreferrer">{t('legal.terms')}</a>{/if}
    {#if terms && privacy}<span aria-hidden="true">·</span>{/if}
    {#if privacy}<a href={privacy} target="_blank" rel="noopener noreferrer">{t('legal.privacy')}</a>{/if}
  </p>
{/if}

<style>
  .legal {
    display: flex;
    flex-wrap: wrap;
    gap: 0 0.5ch;
    align-items: baseline;
  }
</style>
