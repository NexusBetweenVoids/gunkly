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
  const passkeyReady = $derived(!!app.instance.passkeysAvailable && typeof window !== 'undefined' && !!window.PublicKeyCredential);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });

  async function submit(e?: SubmitEvent, resend = false): Promise<void> {
    e?.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      const body: Record<string, string> = { login, password };
      // Posting the password again without a code makes the server email a new one.
      if (needSecond && !resend) body[recovery ? 'recoveryCode' : method === 'email' ? 'emailCode' : 'totp'] = code.trim();
      const s = await api<SessionView>('POST', '/session', body);
      if (s.twoFactorRequired) {
        needSecond = true;
        method = s.method ?? 'totp';
        passkeyOffered = !!s.passkey && passkeyReady;
        if (resend) toast(t('twofa.resent'));
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
    {#if needSecond}
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
      />
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
    {#if !needSecond || method !== 'passkey' || recovery}
      <Button type="submit" {busy} variant={needSecond && passkeyOffered && !recovery ? 'quiet' : 'primary'}>{needSecond ? t('signIn.verify') : t('signIn.title')}</Button>
    {/if}
    {#if needSecond}
      <Button
        variant="quiet"
        onclick={() => {
          needSecond = false;
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
    <div class="row">
      {#if app.instance.emailEnabled}<a href="/forgot-password">{t('signIn.forgot')}</a>{/if}
      {#if app.instance.openSignup}<a href="/signup">{t('signIn.createAccount')}</a>{/if}
    </div>
  {/if}
  <LegalLinks />
</section>

<style>
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
