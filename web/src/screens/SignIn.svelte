<script lang="ts">
  import Logo from '../components/Logo.svelte';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import SecondFactor from '../components/SecondFactor.svelte';
  import { api } from '../lib/api';
  import { safeNext } from '../lib/safenext';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { navigate } from '../lib/router';
  import { adopt, app } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { SessionView } from '../lib/types';

  const next = safeNext(new URLSearchParams(location.search).get('next'));
  let login = $state('');
  let password = $state('');
  let code = $state('');
  let recovery = $state(false);
  let needSecond = $state(false);
  let method = $state<'totp' | 'email'>('totp');
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
        if (resend) toast(t('twofa.resent'));
      } else {
        await adopt(s);
        password = '';
        navigate(next, true);
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
      <p class="muted">{method === 'email' && !recovery ? t('signIn.emailCodeSent') : t('signIn.twoFactor')}</p>
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
    {#if err.code !== 'invalid_credentials' && err.code !== 'invalid_code'}<Alert message={err.message} />{/if}
    <Button type="submit" {busy}>{needSecond ? t('signIn.verify') : t('signIn.title')}</Button>
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
  {#if !needSecond}
    <div class="row">
      {#if app.instance.emailEnabled}<a href="/forgot-password">{t('signIn.forgot')}</a>{/if}
      {#if app.instance.openSignup}<a href="/signup">{t('signIn.createAccount')}</a>{/if}
    </div>
  {/if}
</section>
