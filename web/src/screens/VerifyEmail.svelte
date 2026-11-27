<script lang="ts">
  import { href } from '../lib/base';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import RecoveryCodes from '../components/RecoveryCodes.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { navigate } from '../lib/router.svelte';
  import { adopt, announceAccountChange, app, refreshMe } from '../lib/session.svelte';
  import type { SessionView } from '../lib/types';

  // The token only lives in this page: take it out of the address bar (and history).
  const token = new URLSearchParams(location.search).get('token') ?? '';
  if (token) history.replaceState(null, '', location.pathname);

  // 'offer': a brand-new account's link opened on a signed-out device (often the phone's mail app):
  // confirming can sign in here too, once, after the person sees which account it is.
  let phase = $state<'working' | 'offer' | 'done' | 'invalid' | 'error'>(token ? 'working' : 'invalid');
  let continueAs = $state('');
  let codes = $state<string[]>([]);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });

  async function start(): Promise<void> {
    try {
      const p = await api<{ continueAs?: string }>('POST', '/account/email/verify/preview', { token });
      if (p.continueAs && !app.user) {
        continueAs = p.continueAs;
        phase = 'offer';
        return;
      }
    } catch (x) {
      err = failure(x);
      phase = err.code === 'invalid_token' ? 'invalid' : 'error';
      return;
    }
    await verify(false);
  }

  async function verify(signIn: boolean): Promise<void> {
    busy = true;
    try {
      const r = await api<{ verified: boolean; recoveryCodes?: string[]; session?: SessionView }>('POST', '/account/email/verify', { token, signIn });
      codes = r.recoveryCodes ?? [];
      if (r.session) await adopt(r.session);
      phase = 'done';
      announceAccountChange(); // other open tabs drop their "confirm your email" banner
      if (app.user) await refreshMe().catch(() => {});
      if (r.session && !codes.length) next();
    } catch (x) {
      err = failure(x);
      phase = err.code === 'invalid_token' ? 'invalid' : 'error';
    } finally {
      busy = false;
    }
  }
  if (token) void start();

  // Signed in: on to the welcome flow when there is no household yet, else Home.
  const next = () => navigate(app.households.length ? '/' : '/welcome', true);
</script>

<section class="card narrow stack">
  <h1>{t('verifyEmail.title')}</h1>
  {#if phase === 'working'}
    <p role="status" class="muted">{t('verifyEmail.working')}</p>
  {:else if phase === 'offer'}
    <p>{t('verifyEmail.offer', { name: continueAs })}</p>
    <Button {busy} onclick={() => verify(true)}>{t('verifyEmail.continueAs', { name: continueAs })}</Button>
    <p class="hint">{t('verifyEmail.offerHint')}</p>
    <p><button type="button" class="linkish" disabled={busy} onclick={() => verify(false)}>{t('verifyEmail.onlyConfirm')}</button></p>
  {:else if phase === 'invalid'}
    <p role="alert">{t('verifyEmail.invalid')}</p>
  {:else if phase === 'error'}
    <Alert message={err.message} />
  {:else}
    <p role="status">{t('verifyEmail.done')}</p>
    {#if codes.length}
      <RecoveryCodes {codes} intro={t('verifyEmail.codesIntro')} onsaved={() => (app.user ? next() : (codes = []))} />
    {:else if app.user}
      <div><Button onclick={next}>{t('verifyEmail.continue')}</Button></div>
    {:else}
      <p><a href={href('/signin')}>{t('signIn.title')}</a></p>
    {/if}
  {/if}
</section>

<style>
  .linkish {
    padding: 0;
    border: 0;
    background: none;
    color: var(--accent);
    text-decoration: underline;
    cursor: pointer;
    min-block-size: var(--tap);
  }
</style>
