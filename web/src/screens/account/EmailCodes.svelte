<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import RecoveryCodes from '../../components/RecoveryCodes.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app, refreshMe } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  let codes = $state<string[]>([]);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let confirmOff = $state(false);
  const on = $derived(app.user?.emailOtpEnabled ?? false);

  async function enable(): Promise<void> {
    busy = true;
    err = { code: '', message: '' };
    try {
      codes = (await withStepUp(() => api<{ recoveryCodes: string[] }>('POST', '/account/email-otp'))).recoveryCodes;
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
      await withStepUp(() => api<void>('DELETE', '/account/email-otp'));
      await refreshMe();
      toast(t('emailcodes.disabled'));
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
      confirmOff = false;
    }
  }
</script>

{#if app.instance.emailEnabled}
  <section class="card" aria-labelledby="a-emailcodes">
    <h2 id="a-emailcodes">{t('emailcodes.title')}</h2>
    {#if codes.length}
      <RecoveryCodes {codes} intro={t('totp.codesIntro')} onsaved={() => (codes = [])} />
    {:else}
      <div class="stack">
        <p><span class="badge">{on ? t('totp.on') : t('totp.off')}</span></p>
        <p class="muted">{t('emailcodes.intro')} {t('emailcodes.mailbox')}</p>
        {#if !on && app.user?.totpEnabled}<p class="muted">{t('emailcodes.enableReplaces')}</p>{/if}
        {#if !app.user?.emailVerified && !on}<p class="muted">{t('emailcodes.needVerified')}</p>{/if}
        {#if on && app.instance.hostedMode}<p class="muted">{t('emailcodes.mandatory')}</p>{/if}
        <Alert message={err.message} />
        <div>
          {#if on}
            {#if !app.instance.hostedMode}
              <Button variant="danger" onclick={() => (confirmOff = true)}>{t('totp.disable')}</Button>
            {/if}
          {:else}
            <Button {busy} disabled={!app.user?.emailVerified} onclick={enable}>{t('emailcodes.enable')}</Button>
          {/if}
        </div>
      </div>
    {/if}
  </section>

  <Confirm
    open={confirmOff}
    title={t('emailcodes.disableTitle')}
    message={t('totp.disableWarn')}
    confirmLabel={t('totp.disable')}
    {busy}
    onconfirm={disable}
    oncancel={() => (confirmOff = false)}
  />
{/if}
