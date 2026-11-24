<script lang="ts">
  import { t } from '../lib/i18n';
  import Button from './Button.svelte';
  import Field from './Field.svelte';

  // The second step: a code from the authenticator app or from email (with
  // "email me a new code"), or a recovery code. The parent decides which body
  // field the code goes into from `method` and `recovery`.
  let {
    code = $bindable(''),
    recovery = $bindable(false),
    error = '',
    method = 'totp',
    onresend,
  }: { code: string; recovery: boolean; error?: string; method?: 'totp' | 'email' | 'passkey'; onresend?: () => void } = $props();
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
{:else if method !== 'passkey'}
  <Field
    label={method === 'email' ? t('twofa.emailCode') : t('twofa.code')}
    hint={method === 'email' ? t('twofa.emailCodeHint') : t('twofa.codeHint')}
    bind:value={code}
    {error}
    inputmode="numeric"
    autocomplete="one-time-code"
    required
  />
  {#if method === 'email' && onresend}
    <Button variant="quiet" onclick={onresend}>{t('twofa.resend')}</Button>
  {/if}
{/if}
<Button
  variant="quiet"
  onclick={() => {
    recovery = !recovery;
    code = '';
  }}
>
  {recovery ? (method === 'email' ? t('twofa.useEmail') : method === 'passkey' ? t('twofa.usePasskey') : t('twofa.useApp')) : t('twofa.useRecovery')}
</Button>
