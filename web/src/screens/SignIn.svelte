<script lang="ts">
  import LegalLinks from '../components/LegalLinks.svelte';
  import Logo from '../components/Logo.svelte';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import SecondFactor from '../components/SecondFactor.svelte';
  import { api } from '../lib/api';
  import { safeNext } from '../lib/safenext';
  import { failure } from '../lib/errors';
  import { has, t, type Key } from '../lib/i18n';
  import { navigate } from '../lib/router.svelte';
  import { adopt, app } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { SessionView } from '../lib/types';

  const params = new URLSearchParams(location.search);
  const next = safeNext(params.get('next'));
  // Where a single sign-on attempt ended (the callback redirects here with ?sso=<code>).
  const ssoCode = params.get('sso') ?? '';
  const ssoMessage = ssoCode ? t(has(`err.sso_${ssoCode}`) ? (`err.sso_${ssoCode}` as Key) : 'err.sso_failed') : '';
  let login = $state('');
  let password = $state('');
  let code = $state('');
  let recovery = $state(false);
  let needSecond = $state(false);
  let method = $state<'totp' | 'email' | 'passkey'>('totp');
  let passkeyOffered = $state(false); // the account has a passkey it can use as second factor
  let emailSent = $state(false); // the emailed code went out (with a passkey offered, only on request)
  // Passkey first: the email code is only sent when the user asks for it.
  const choosing = $derived(needSecond && passkeyOffered && method === 'email' && !emailSent && !recovery);
  const passkeyReady = $derived(!!app.instance.passkeysAvailable && typeof window !== 'undefined' && !!window.PublicKeyCredential);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });

  // sendEmail: post the password again without a code, asking the server to email one (a new one
  // on a repeat). Without a passkey on offer the server sends it on the first step by itself.
  async function submit(e?: SubmitEvent, sendEmail = false): Promise<void> {
    e?.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      const body: Record<string, string | boolean> = { login, password };
      if (sendEmail) body.sendEmailCode = true;
      else if (needSecond && !choosing) body[recovery ? 'recoveryCode' : method === 'email' ? 'emailCode' : 'totp'] = code.trim();
      const s = await api<SessionView>('POST', '/session', body);
      if (s.twoFactorRequired) {
        const again = emailSent;
        needSecond = true;
        method = s.method ?? 'totp';
        passkeyOffered = !!s.passkey && passkeyReady;
        emailSent = !!s.emailSent;
        if (sendEmail && again) toast(t('twofa.resent'));
        // The server offered a passkey this browser cannot use: go straight to the emailed code.
        if (method === 'email' && !emailSent && !passkeyOffered && !sendEmail) {
          busy = false;
          return submit(undefined, true);
        }
      } else {
        await done(s);
      }
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }

  async function done(s: SessionView): Promise<void> {
    await adopt(s);
    password = '';
    if (next.startsWith('/oauth/')) location.assign(next); // server-rendered consent page, not a SPA route
    else navigate(next, true);
  }

  // A passkey signs in on its own (password and second factor in one), or answers the second step.
  async function withPasskey(second: boolean): Promise<void> {
    busy = true;
    err = { code: '', message: '' };
    try {
      const { assertion } = await import('../lib/passkey');
      if (second) {
        const passkey = await assertion('/session/passkey/options', { login, password });
        await done(await api<SessionView>('POST', '/session', { login, password, passkey }));
      } else {
        await done(await api<SessionView>('POST', '/session/passkey/login', await assertion('/session/passkey/login/options')));
      }
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<section class="card narrow">
  <p class="logo-hero"><Logo size={72} /></p>
  <h1>{t('signIn.title')}</h1>
  <form class="stack" onsubmit={submit}>
    {#if choosing}
      <p class="muted">{t('signIn.passkeyPrompt')}</p>
      <Button {busy} onclick={() => withPasskey(true)}>{t('twofa.usePasskey')}</Button>
      <div class="stack-sm alternatives">
        <p class="or">{t('signIn.or')}</p>
        <Button variant="quiet" {busy} onclick={() => submit(undefined, true)}>{t('twofa.emailInstead')}</Button>
        <Button variant="quiet" onclick={() => (recovery = true)}>{t('twofa.useRecovery')}</Button>
      </div>
    {:else if needSecond}
      {#if passkeyOffered && !recovery}
        <p class="muted">{t('signIn.passkeyPrompt')}</p>
        <Button {busy} onclick={() => withPasskey(true)}>{t('twofa.usePasskey')}</Button>
      {/if}
      {#if method !== 'passkey' || recovery}
        <p class="muted">{method === 'email' && !recovery ? t('signIn.emailCodeSent') : t('signIn.twoFactor')}</p>
      {/if}
      <SecondFactor
        bind:code
        bind:recovery
        {method}
        onresend={() => submit(undefined, true)}
        error={err.code === 'invalid_code' ? err.message : ''}
      >
        {#snippet action()}
          <Button type="submit" {busy} variant={passkeyOffered && !recovery ? 'quiet' : 'primary'}>{t('signIn.verify')}</Button>
        {/snippet}
      </SecondFactor>
    {:else}
      <Field
        label={t('signIn.username')}
        bind:value={login}
        autocomplete="username"
        autocapitalize="none"
        spellcheck="false"
        required
      />
      <Field
        label={t('field.password')}
        type="password"
        bind:value={password}
        autocomplete="current-password"
        required
        error={err.code === 'invalid_credentials' ? err.message : ''}
      />
    {/if}
    {#if err.code !== 'invalid_credentials' && err.code !== 'invalid_code'}<Alert message={err.message || (needSecond ? '' : ssoMessage)} />{/if}
    {#if !needSecond}
      <Button type="submit" {busy}>{t('signIn.title')}</Button>
    {/if}
    {#if needSecond}
      <Button
        variant="quiet"
        onclick={() => {
          needSecond = false;
          emailSent = false;
          recovery = false;
          code = '';
          err = { code: '', message: '' };
        }}>{t('common.back')}</Button
      >
    {/if}
  </form>
  {#if !needSecond && (passkeyReady || app.instance.sso)}
    <div class="stack-sm alternatives">
      <p class="or">{t('signIn.or')}</p>
      {#if passkeyReady}
        <Button variant="quiet" {busy} onclick={() => withPasskey(false)}>{t('signIn.passkey')}</Button>
      {/if}
      {#if app.instance.sso}
        <a class="btn btn-quiet" href="/api/v1/auth/oidc/start?next={encodeURIComponent(next)}"
          >{app.instance.sso.name ? t('signIn.ssoNamed', { name: app.instance.sso.name }) : t('signIn.sso')}</a
        >
      {/if}
    </div>
  {/if}
  {#if !needSecond}
    {#if app.instance.emailEnabled}<p class="forgot"><a href="/forgot-password">{t('signIn.forgot')}</a></p>{/if}
    {#if app.instance.openSignup}
      <div class="new-here">
        <p>{t('signIn.newHere')}</p>
        <a class="btn btn-outline" href="/signup">{t('signIn.createAccount')}</a>
      </div>
    {/if}
  {/if}
  <LegalLinks />
</section>

<style>
  .forgot {
    margin: 0;
  }
  .new-here {
    display: grid;
    gap: 10px;
    margin-block-start: calc(var(--space) * 1.5);
    padding-block-start: calc(var(--space) * 1.25);
    border-block-start: 1px solid var(--border);
    text-align: center;
  }
  .new-here p {
    margin: 0;
    color: var(--text-2);
  }
  .btn-outline {
    background: transparent;
    color: var(--accent);
    box-shadow: inset 0 0 0 2px var(--accent);
  }
  .alternatives {
    margin-block: var(--space);
  }
  .or {
    display: flex;
    align-items: center;
    gap: 12px;
    margin: 0;
    color: var(--text-2);
    font-size: 0.875rem;
  }
  .or::before,
  .or::after {
    content: '';
    flex: 1;
    border-block-start: 1px solid var(--border);
  }
</style>
