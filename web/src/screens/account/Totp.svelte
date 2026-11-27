<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import RecoveryCodes from '../../components/RecoveryCodes.svelte';
  import QrCode from '../../components/QrCode.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app, refreshMe } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';
  import MethodRow from './MethodRow.svelte';

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
</script>

<MethodRow
  id="a-totp"
  name={t('totp.title')}
  hint={t('totp.intro')}
  state={app.user?.totpEnabled ? t('ways.on') : t('ways.off')}
  tone={app.user?.totpEnabled ? 'ok' : ''}
  open={!!setup || codes.length > 0}
>
  {#snippet notes()}
    {#if app.user?.emailOtpEnabled && !app.user?.totpEnabled}<p class="hint">{t('totp.replacesEmail')}</p>{/if}
    {#if app.user?.totpEnabled && app.instance.hostedMode}<p class="hint">{t('totp.mandatory')}</p>{/if}
    {#if !setup}<div aria-live="polite"><Alert message={err.message} /></div>{/if}
  {/snippet}
  {#snippet action()}
    {#if app.user?.totpEnabled}
      {#if !app.instance.hostedMode && !codes.length}
        <Button variant="quiet" onclick={() => (confirmOff = true)}>{t('totp.disable')}</Button>
      {/if}
    {:else if !setup}
      <Button variant="quiet" {busy} onclick={start}>{t('totp.setup')}</Button>
    {/if}
  {/snippet}
  {#if codes.length}
    <RecoveryCodes {codes} intro={t('totp.codesIntro')} onsaved={() => (codes = [])} />
  {:else if setup}
    <form class="stack" onsubmit={confirm}>
      <p>{t('totp.setupIntro')}</p>
      <div class="stack-sm">
        <QrCode text={setup.uri} label={t('totp.qrLabel')} />
        <p class="hint">{t('totp.scan')}</p>
      </div>
      <div class="stack-sm">
        <p class="label" id="totp-secret">{t('totp.secret')}</p>
        <p class="mono" aria-labelledby="totp-secret">{setup.secret.replace(/(.{4})(?=.)/g, '$1 ')}</p>
        <p class="label" id="totp-uri">{t('totp.uri')}</p>
        <p class="mono" aria-labelledby="totp-uri">{setup.uri}</p>
        <div class="row">
          <Button variant="quiet" onclick={() => copyText(setup!.secret)}>{t('totp.copySecret')}</Button>
          <Button variant="quiet" onclick={() => copyText(setup!.uri)}>{t('totp.copyUri')}</Button>
        </div>
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
      <div class="actions">
        <Button type="submit" {busy}>{t('totp.enable')}</Button>
        <Button variant="quiet" onclick={() => (setup = null)}>{t('common.cancel')}</Button>
      </div>
    </form>
  {/if}
</MethodRow>

<Confirm
  open={confirmOff}
  title={t('totp.disableTitle')}
  message={t('totp.disableWarn')}
  confirmLabel={t('totp.disable')}
  {busy}
  onconfirm={disable}
  oncancel={() => (confirmOff = false)}
/>
