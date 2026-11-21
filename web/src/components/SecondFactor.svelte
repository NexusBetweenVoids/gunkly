<script lang="ts">
  import { t } from '../lib/i18n';
  import Button from './Button.svelte';
  import Field from './Field.svelte';

  let {
    code = $bindable(''),
    recovery = $bindable(false),
    error = '',
  }: { code: string; recovery: boolean; error?: string } = $props();
</script>

{#if recovery}
  <Field
    label={t('twofa.recoveryCode')}
    hint={t('twofa.recoveryHint')}
    bind:value={code}
    {error}
    autocomplete="off"
    autocapitalize="none"
    spellcheck="false"
    required
  />
{:else}
  <Field
    label={t('twofa.code')}
    hint={t('twofa.codeHint')}
    bind:value={code}
    {error}
    inputmode="numeric"
    autocomplete="one-time-code"
    required
  />
{/if}
<Button
  variant="quiet"
  onclick={() => {
    recovery = !recovery;
    code = '';
  }}
>
  {recovery ? t('twofa.useApp') : t('twofa.useRecovery')}
</Button>
