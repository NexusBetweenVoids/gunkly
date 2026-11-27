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
  import { href } from '../../lib/base';
  import MethodRow from './MethodRow.svelte';

  let codes = $state<string[]>([]);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let confirmOff = $state(false);
  const on = $derived(app.user?.emailOtpEnabled ?? false);
  const verified = $derived(!!app.user?.email && !!app.user.emailVerified);

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
  <MethodRow
    id="a-emailcodes"
    name={t('emailcodes.title')}
    hint={t('emailcodes.intro')}
    state={on ? t('ways.on') : verified ? t('ways.off') : t('ways.unavailable')}
    tone={on ? 'ok' : ''}
    open={codes.length > 0}
  >
    {#snippet notes()}
      {#if !on && !verified}
        <p class="hint" data-testid="emailcodes-reason">{t('emailcodes.needVerified')}</p>
      {:else if !on}
        <p class="hint">{t('emailcodes.mailbox')}</p>
        {#if app.user?.totpEnabled}<p class="hint">{t('emailcodes.enableReplaces')}</p>{/if}
      {/if}
      {#if on && app.instance.hostedMode}<p class="hint">{t('emailcodes.mandatory')}</p>{/if}
      <div aria-live="polite"><Alert message={err.message} /></div>
    {/snippet}
    {#snippet action()}
      {#if on}
        {#if !app.instance.hostedMode && !codes.length}
          <Button variant="quiet" onclick={() => (confirmOff = true)}>{t('ways.turnOff')}</Button>
        {/if}
      {:else if verified}
        <Button variant="quiet" {busy} onclick={enable}>{t('ways.turnOn')}</Button>
      {:else}
        <a class="btn btn-quiet" href={href('/settings/profile')}>{app.user?.email ? t('ways.confirmEmail') : t('ways.addEmail')}</a>
      {/if}
    {/snippet}
    <RecoveryCodes {codes} intro={t('totp.codesIntro')} onsaved={() => (codes = [])} />
  </MethodRow>

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
