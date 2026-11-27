<script lang="ts">
  import { href } from '../lib/base';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { navigate } from '../lib/router.svelte';
  import { app, clearSession } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';

  // The token only lives in this page: take it out of the address bar (and history).
  const token = new URLSearchParams(location.search).get('token') ?? '';
  if (token) history.replaceState(null, '', location.pathname);

  let password = $state('');
  let busy = $state(false);
  let invalid = $state(!token);
  let err = $state({ code: '', message: '' });
  const pwCodes = ['password_too_short', 'password_too_long', 'password_invalid'];

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      await api<void>('POST', '/password-reset/confirm', { token, password });
      password = '';
      clearSession(); // every session was signed out by the reset
      toast(t('reset.done'));
      navigate('/signin', true);
    } catch (x) {
      err = failure(x);
      invalid = err.code === 'invalid_token';
    } finally {
      busy = false;
    }
  }
</script>

<section class="card narrow auth">
  <h1>{t('reset.title')}</h1>
  {#if invalid}
    <p role="alert">{t('reset.invalid')}</p>
    <p><a href={href('/forgot-password')}>{t('reset.askAgain')}</a></p>
  {:else}
    <form class="stack" onsubmit={submit}>
      <Field
        label={t('password.new')}
        type="password"
        bind:value={password}
        autocomplete="new-password"
        hint={app.instance.minPasswordLength ? t('field.passwordHint', { n: app.instance.minPasswordLength }) : ''}
        required
        error={pwCodes.includes(err.code) ? err.message : ''}
      />
      {#if !pwCodes.includes(err.code)}<Alert message={err.message} />{/if}
      <Button type="submit" {busy}>{t('reset.submit')}</Button>
    </form>
  {/if}
  <p><a href={href('/signin')}>{t('forgot.back')}</a></p>
</section>
