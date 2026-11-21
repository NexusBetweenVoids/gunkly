<script lang="ts">
  import { addDays, today } from '../lib/dates';
  import { t } from '../lib/i18n';
  import Button from './Button.svelte';
  import Dialog from './Dialog.svelte';
  import Field from './Field.svelte';

  let {
    open,
    title,
    label,
    minOffset = 0,
    confirmLabel,
    alt,
    busy = false,
    onconfirm,
    oncancel,
  }: {
    open: boolean;
    title: string;
    label: string;
    minOffset?: number;
    confirmLabel: string;
    alt?: { label: string; run: () => void };
    busy?: boolean;
    onconfirm: (date: string) => void;
    oncancel: () => void;
  } = $props();
  const min = $derived(addDays(today(), minOffset));
  let date = $state('');
  $effect(() => {
    if (open) date = addDays(today(), Math.max(minOffset, 1));
  });
</script>

<Dialog {open} {title} onclose={oncancel}>
  <form class="stack" onsubmit={(e) => { e.preventDefault(); onconfirm(date); }}>
    <Field {label} type="date" bind:value={date} {min} required />
    <div class="row">
      <Button type="submit" {busy}>{confirmLabel}</Button>
      {#if alt}<Button variant="quiet" onclick={alt.run}>{alt.label}</Button>{/if}
      <Button variant="quiet" onclick={oncancel}>{t('common.cancel')}</Button>
    </div>
  </form>
</Dialog>
