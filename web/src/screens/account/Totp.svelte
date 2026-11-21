<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app, refreshMe } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  let setup = $state<{ secret: string; uri: string } | null>(null);
  let code = $state('');
  let codes = $state<string[]>([]);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let confirmOff = $state(false);

  async function start(): Promise<void> {
    busy = true;
    err = { code: '', message: '' };
    try {
      setup = await withStepUp(() => api<{ secret: string; uri: string }>('POST', '/account/totp/setup'));
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }

  async function confirm(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      codes = (await api<{ recoveryCodes: string[] }>('POST', '/account/totp/confirm', { code: code.trim() })).recoveryCodes;
      setup = null;
      code = '';
      await refreshMe();
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }

  async function disable(): Promise<void> {
    busy = true;
    try {
      await withStepUp(() => api<void>('DELETE', '/account/totp'));
      await refreshMe();
      toast(t('totp.disabled'));
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
      confirmOff = false;
    }
  }

  function download(): void {
    const url = URL.createObjectURL(new Blob([codes.join('\n') + '\n'], { type: 'text/plain' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = t('totp.filename');
    a.click();
    URL.revokeObjectURL(url);
  }
</script>

<section class="card" aria-labelledby="a-totp">
  <h2 id="a-totp">{t('totp.title')}</h2>
  {#if codes.length}
    <div class="stack">
      <p>{t('totp.codesIntro')}</p>
      <ul class="list mono" aria-label={t('totp.codesLabel')}>
        {#each codes as c (c)}<li>{c}</li>{/each}
      </ul>
      <div class="row">
        <Button variant="quiet" onclick={() => copyText(codes.join('\n'))}>{t('common.copy')}</Button>
        <Button variant="quiet" onclick={download}>{t('totp.download')}</Button>
        <Button onclick={() => (codes = [])}>{t('totp.saved')}</Button>
      </div>
    </div>
  {:else if setup}
    <form class="stack" onsubmit={confirm}>
      <p>{t('totp.setupIntro')}</p>
      <div class="stack-sm">
        <p class="label" id="totp-secret">{t('totp.secret')}</p>
        <p class="mono" aria-labelledby="totp-secret">{setup.secret}</p>
        <p class="label" id="totp-uri">{t('totp.uri')}</p>
        <p class="mono" aria-labelledby="totp-uri">{setup.uri}</p>
        <div><Button variant="quiet" onclick={() => copyText(setup!.secret)}>{t('totp.copySecret')}</Button></div>
      </div>
      <Field
        label={t('totp.confirmCode')}
        bind:value={code}
        inputmode="numeric"
        autocomplete="one-time-code"
        required
        error={err.code === 'invalid_code' ? err.message : ''}
      />
      {#if err.code !== 'invalid_code'}<Alert message={err.message} />{/if}
      <div class="row">
        <Button type="submit" {busy}>{t('totp.enable')}</Button>
        <Button variant="quiet" onclick={() => (setup = null)}>{t('common.cancel')}</Button>
      </div>
    </form>
  {:else}
    <div class="stack">
      <p>
        <span class="badge">{app.user?.totpEnabled ? t('totp.on') : t('totp.off')}</span>
      </p>
      <p class="muted">{t('totp.intro')}</p>
      <Alert message={err.message} />
      <div>
        {#if app.user?.totpEnabled}
          <Button variant="danger" onclick={() => (confirmOff = true)}>{t('totp.disable')}</Button>
        {:else}
          <Button {busy} onclick={start}>{t('totp.setup')}</Button>
        {/if}
      </div>
    </div>
  {/if}
</section>

<Confirm
  open={confirmOff}
  title={t('totp.disableTitle')}
  message={t('totp.disableWarn')}
  confirmLabel={t('totp.disable')}
  {busy}
  onconfirm={disable}
  oncancel={() => (confirmOff = false)}
/>
