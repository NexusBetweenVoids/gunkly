<script lang="ts">
  import Logo from '../components/Logo.svelte';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { navigate } from '../lib/router';
  import { adopt } from '../lib/session.svelte';
  import type { SessionView } from '../lib/types';

  let code = $state('');
  let username = $state('');
  let email = $state('');
  let displayName = $state('');
  let password = $state('');
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  const on = (...codes: string[]) => (codes.includes(err.code) ? err.message : '');

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      await adopt(
        await api<SessionView>('POST', '/setup', { code: code.trim(), username, email, displayName, password }),
      );
      navigate('/', true);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<section class="card narrow">
  <p class="logo-hero"><Logo size={72} /></p>
  <h1>{t('setup.title')}</h1>
  <p class="muted">{t('setup.intro')}</p>
  <form class="stack" onsubmit={submit}>
    <Field
      label={t('setup.code')}
      hint={t('setup.codeHint')}
      bind:value={code}
      error={on('invalid_setup_code')}
      autocomplete="off"
      spellcheck="false"
      required
    />
    <Field label={t('field.displayName')} bind:value={displayName} autocomplete="name" required maxlength={100} />
    <Field
      label={t('field.username')}
      hint={t('field.usernameHint')}
      bind:value={username}
      autocomplete="username"
      autocapitalize="none"
      spellcheck="false"
      required
    />
    <Field label={t('field.emailOptional')} type="email" bind:value={email} autocomplete="email" />
    <Field
      label={t('field.password')}
      type="password"
      bind:value={password}
      autocomplete="new-password"
      required
      error={on('password_too_short', 'password_too_long', 'password_invalid')}
    />
    {#if !['invalid_setup_code', 'password_too_short', 'password_too_long', 'password_invalid'].includes(err.code)}
      <Alert message={err.message} />
    {/if}
    <Button type="submit" {busy}>{t('setup.submit')}</Button>
  </form>
</section>
