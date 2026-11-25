<script lang="ts">
  // One admin configuration field: editable text, a secret (write-only: "saved" + replace/remove), or
  // locked because the environment sets it.
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { t } from '../../lib/i18n';
  import type { ConfigField } from './config.svelte';

  let {
    field,
    label,
    hint = '',
    value = $bindable(''),
    secret = false,
    placeholder = '',
    onremove,
  }: { field: ConfigField | undefined; label: string; hint?: string; value?: string; secret?: boolean; placeholder?: string; onremove?: () => void } = $props();
  let replacing = $state(false);
</script>

{#if field?.locked}
  <Field label={label} value={secret ? '••••••••' : (field.value ?? '')} readonly hint={t('config.locked', { env: field.env })} />
{:else if secret && field?.set && !replacing}
  <div class="field">
    <span class="label">{label}</span>
    <p class="saved">
      <span class="muted">{t('config.secretSaved')}</span>
      <Button variant="quiet" onclick={() => (replacing = true)}>{t('config.replace')}</Button>
      {#if onremove}<Button variant="quiet" onclick={onremove}>{t('config.remove')}</Button>{/if}
    </p>
    {#if hint}<p class="hint">{hint}</p>{/if}
  </div>
{:else}
  <Field {label} {hint} bind:value type={secret ? 'password' : 'text'} autocomplete="off" spellcheck="false" {placeholder} />
{/if}

<style>
  .saved {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin: 0;
  }
</style>
