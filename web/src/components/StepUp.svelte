<script lang="ts">
  import { appPath } from '../lib/base';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { app } from '../lib/session.svelte';
  import { resolveStepUp, stepUp } from '../lib/stepup.svelte';
  import Alert from './Alert.svelte';
  import Button from './Button.svelte';
  import Dialog from './Dialog.svelte';
  import Field from './Field.svelte';
  import SecondFactor from './SecondFactor.svelte';

  let password = $state('');
  let code = $state('');
  let recovery = $state(false);
  let emailAsked = $state(false); // email-code users get the code after the password is checked
  let busy = $state(false);
  let err = $state({ code: '', message: '' });

  const email = $derived(app.user?.twoFactor === 'email');
  const sso = $derived(!!app.user?.ssoLinked && !!app.instance.sso);
  const noPassword = $derived(app.user?.hasPassword === false); // created by single sign-on, no password yet
  const passkeys = $derived(!!app.user?.passkeys && !!app.instance.passkeysAvailable);
  // A passkey is a second factor too: without an app or email codes, the password goes with a recovery code.
  const passkeyOnly = $derived(!!app.user?.passkeys && !app.user?.totpEnabled && !email);

  async function submit(e?: SubmitEvent, resend = false): Promise<void> {
    e?.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      const body: Record<string, string> = { password };
      if (passkeyOnly) body.recoveryCode = code.trim();
      else if (app.user?.totpEnabled) body[recovery ? 'recoveryCode' : 'totp'] = code.trim();
      // Without a code the server emails one (and answers two_factor_required).
      else if (email && emailAsked && !resend) body[recovery ? 'recoveryCode' : 'emailCode'] = code.trim();
      await api<void>('POST', '/session/step-up', body);
      password = code = '';
      emailAsked = false;
      resolveStepUp(true);
    } catch (x) {
      err = failure(x);
      if (email && err.code === 'two_factor_required') {
        emailAsked = true;
        err = { code: '', message: '' };
      }
    } finally {
      busy = false;
    }
  }

  // The provider asks the user to sign in again, then brings the browser back to this page.
  async function viaSSO(): Promise<void> {
    busy = true;
    err = { code: '', message: '' };
    try {
      const r = await api<{ url: string }>('POST', '/session/step-up/sso', { next: appPath() });
      location.assign(r.url);
    } catch (x) {
      err = failure(x);
      busy = false;
    }
  }

  async function viaPasskey(): Promise<void> {
    busy = true;
    err = { code: '', message: '' };
    try {
      const { assertion } = await import('../lib/passkey');
      const passkey = await assertion('/session/step-up/passkey/options');
      await api<void>('POST', '/session/step-up', { passkey });
      password = code = '';
      resolveStepUp(true);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog open={stepUp.open} title={t('stepUp.title')} onclose={() => resolveStepUp(false)}>
  <form class="stack" onsubmit={submit}>
    <p class="muted">{t('stepUp.body')}</p>
    {#if passkeys}
      <Button {busy} onclick={viaPasskey}>{t('stepUp.passkey')}</Button>
    {/if}
    {#if sso}
      <Button {busy} onclick={viaSSO}>{app.instance.sso?.name ? t('stepUp.ssoNamed', { name: app.instance.sso.name }) : t('stepUp.sso')}</Button>
    {/if}
    {#if (passkeys || sso) && !noPassword}<p class="muted">{t('stepUp.orPassword')}</p>{/if}
    {#if !noPassword}
    <Field
      label={t('field.password')}
      type="password"
      bind:value={password}
      autocomplete="current-password"
      required
      error={err.code === 'invalid_credentials' ? err.message : ''}
    />
    {#if passkeyOnly}
      <Field
        label={t('twofa.recoveryCode')}
        hint={t('twofa.recoveryHint')}
        bind:value={code}
        autocomplete="off"
        autocapitalize="none"
        spellcheck="false"
        required
        error={err.code === 'invalid_code' ? err.message : ''}
      />
    {:else if app.user?.totpEnabled}
      <SecondFactor bind:code bind:recovery error={err.code === 'invalid_code' ? err.message : ''} />
    {:else if email && emailAsked}
      <p class="muted" role="status">{t('stepUp.emailCodeSent')}</p>
      <SecondFactor
        bind:code
        bind:recovery
        method="email"
        onresend={() => submit(undefined, true)}
        error={err.code === 'invalid_code' ? err.message : ''}
      />
    {/if}
    {/if}
    {#if err.code !== 'invalid_credentials' && err.code !== 'invalid_code'}<Alert message={err.message} />{/if}
    <div class="row">
      {#if !noPassword}<Button type="submit" {busy}>{t('stepUp.confirm')}</Button>{/if}
      <Button variant="quiet" onclick={() => resolveStepUp(false)}>{t('common.cancel')}</Button>
    </div>
  </form>
</Dialog>
