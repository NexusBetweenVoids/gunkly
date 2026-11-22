<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { locale, t } from '../lib/i18n';
  import { solveChallenge, type Solution } from '../lib/pow';
  import { navigate } from '../lib/router';
  import { adopt, app } from '../lib/session.svelte';
  import type { SessionView } from '../lib/types';

  let displayName = $state('');
  let username = $state('');
  let email = $state('');
  let password = $state('');
  let busy = $state(false);
  let percent = $state(-1); // -1: not solving
  let err = $state({ code: '', message: '' });
  const pwCodes = ['password_too_short', 'password_too_long', 'password_invalid'];

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      let pow: Solution | undefined;
      if (app.instance.requirePow) {
        percent = 0;
        pow = await solveChallenge((f) => (percent = Math.floor(f * 100)));
      }
      const s = await api<SessionView>('POST', '/signup', { username, email, displayName, password, locale, pow });
      await adopt(s);
      navigate('/', true);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
      percent = -1;
    }
  }
</script>

<section class="card narrow">
  <h1>{t('signIn.createAccount')}</h1>
  {#if !app.instance.openSignup}
    <p class="muted">{t('signup.closed')}</p>
    <p><a href="/signin">{t('signIn.title')}</a></p>
  {:else}
    <p class="muted">{t('signup.intro')}</p>
    <form class="stack" onsubmit={submit}>
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
      <Field
        label={app.instance.emailEnabled ? t('field.email') : t('field.emailOptional')}
        hint={app.instance.emailEnabled ? t('field.emailHint') : ''}
        type="email"
        bind:value={email}
        autocomplete="email"
        required={app.instance.emailEnabled}
      />
      <Field
        label={t('field.password')}
        type="password"
        bind:value={password}
        autocomplete="new-password"
        required
        error={pwCodes.includes(err.code) ? err.message : ''}
      />
      {#if !pwCodes.includes(err.code)}<Alert message={err.message} />{/if}
      <p class="muted" role="status" aria-live="polite">
        {#if percent >= 0}{t('signup.working')} {t('signup.progress', { percent })}{/if}
      </p>
      <Button type="submit" {busy}>{t('signup.submit')}</Button>
    </form>
    <p class="muted">{t('invite.haveAccount')} <a href="/signin">{t('signIn.title')}</a></p>
  {/if}
</section>
