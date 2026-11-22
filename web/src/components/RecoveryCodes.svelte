<script lang="ts">
  import { copyText } from '../lib/clip';
  import { t } from '../lib/i18n';
  import Button from './Button.svelte';

  // Shows recovery codes once, with copy and download, until the user confirms.
  let { codes, intro, onsaved }: { codes: string[]; intro: string; onsaved: () => void } = $props();

  function download(): void {
    const url = URL.createObjectURL(new Blob([codes.join('\n') + '\n'], { type: 'text/plain' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = t('totp.filename');
    a.click();
    URL.revokeObjectURL(url);
  }
</script>

<div class="stack">
  <p>{intro}</p>
  <ul class="list mono" aria-label={t('totp.codesLabel')}>
    {#each codes as c (c)}<li>{c}</li>{/each}
  </ul>
  <div class="row">
    <Button variant="quiet" onclick={() => copyText(codes.join('\n'))}>{t('common.copy')}</Button>
    <Button variant="quiet" onclick={download}>{t('totp.download')}</Button>
    <Button onclick={onsaved}>{t('totp.saved')}</Button>
  </div>
</div>
