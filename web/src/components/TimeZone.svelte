<script lang="ts">
  import { t } from '../lib/i18n';
  import { zones } from '../lib/zones';
  import Field from './Field.svelte';

  let { value = $bindable(''), error = '' }: { value: string; error?: string } = $props();

  const uid = $props.id();
  const listId = `tz-${uid}`;
</script>

<Field
  label={t('field.timezone')}
  hint={t('field.timezoneHint')}
  bind:value
  {error}
  list={listId}
  autocomplete="off"
  autocapitalize="none"
  spellcheck="false"
  required
/>
<datalist id={listId}>
  {#each zones as z (z)}<option value={z}></option>{/each}
</datalist>
