<script lang="ts">
  import { t } from '../lib/i18n';
  import Button from './Button.svelte';
  import Dialog from './Dialog.svelte';

  let {
    open,
    title,
    message,
    confirmLabel,
    busy = false,
    onconfirm,
    oncancel,
  }: {
    open: boolean;
    title: string;
    message: string;
    confirmLabel: string;
    busy?: boolean;
    onconfirm: () => void;
    oncancel: () => void;
  } = $props();
</script>

<Dialog {open} {title} onclose={oncancel}>
  <div class="stack">
    <p>{message}</p>
    <div class="row">
      <Button variant="danger" {busy} onclick={onconfirm}>{confirmLabel}</Button>
      <Button variant="quiet" onclick={oncancel}>{t('common.cancel')}</Button>
    </div>
  </div>
</Dialog>
