<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import RecoveryCodes from '../components/RecoveryCodes.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { announceAccountChange, app, refreshMe } from '../lib/session.svelte';

  // The token only lives in this page: take it out of the address bar (and history).
  const token = new URLSearchParams(location.search).get('token') ?? '';
  if (token) history.replaceState(null, '', location.pathname);

  let phase = $state<'working' | 'done' | 'invalid' | 'error'>(token ? 'working' : 'invalid');
  let codes = $state<string[]>([]);
  let err = $state({ code: '', message: '' });

  async function verify(): Promise<void> {
    try {
      const r = await api<{ verified: boolean; recoveryCodes?: string[] }>('POST', '/account/email/verify', { token });
      codes = r.recoveryCodes ?? [];
      phase = 'done';
      announceAccountChange(); // other open tabs drop their "confirm your email" banner
      if (app.user) await refreshMe().catch(() => {});
    } catch (x) {
      err = failure(x);
      phase = err.code === 'invalid_token' ? 'invalid' : 'error';
    }
  }
  if (token) void verify();
</script>

<section class="card narrow stack">
  <h1>{t('verifyEmail.title')}</h1>
  {#if phase === 'working'}
    <p role="status" class="muted">{t('verifyEmail.working')}</p>
  {:else if phase === 'invalid'}
    <p role="alert">{t('verifyEmail.invalid')}</p>
  {:else if phase === 'error'}
    <Alert message={err.message} />
  {:else}
    <p role="status">{t('verifyEmail.done')}</p>
    {#if codes.length}
      <RecoveryCodes {codes} intro={t('verifyEmail.codesIntro')} onsaved={() => (codes = [])} />
    {:else}
      <p><a href={app.user ? '/' : '/signin'}>{app.user ? t('verifyEmail.continue') : t('signIn.title')}</a></p>
    {/if}
  {/if}
</section>
