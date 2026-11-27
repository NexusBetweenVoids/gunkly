<script lang="ts">
  import { href } from '../lib/base';
  import Logo from '../components/Logo.svelte';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import LegalLinks from '../components/LegalLinks.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { navigate } from '../lib/router.svelte';
  import { adopt, app, refreshHouseholds, selectHousehold } from '../lib/session.svelte';
  import type { SessionView } from '../lib/types';

  let { token }: { token: string } = $props();
  let username = $state('');
  let email = $state('');
  let displayName = $state('');
  let password = $state('');
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  const pwCodes = ['password_too_short', 'password_too_long', 'password_invalid'];
  const message = () => (err.code === 'not_found' ? t('invite.invalid') : err.message);

  // Which household the link joins and who sent it, so people can tell it is the right link.
  // A revoked, expired or used-up link answers not_found: say so at once instead of a form.
  let preview = $state<{ householdName: string; inviterName?: string } | null>(null);
  let dead = $state(false);
  $effect(() => {
    preview = null;
    dead = false;
    api<{ householdName: string; inviterName?: string }>('GET', `/invites/${encodeURIComponent(token)}`)
      .then((p) => (preview = p))
      .catch((x) => (dead = failure(x).code === 'not_found')); // other failures: the generic page still works
  });
  const lead = $derived(preview?.inviterName ? t('invite.invitedBy', { name: preview.inviterName }) + ' ' : '');

  async function accept(): Promise<void> {
    busy = true;
    err = { code: '', message: '' };
    try {
      const j = await api<{ householdId: string }>('POST', `/invites/${encodeURIComponent(token)}/accept`);
      await refreshHouseholds(j.householdId);
      selectHousehold(j.householdId);
      navigate('/', true);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }

  async function register(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      const s = await api<SessionView>('POST', `/invites/${encodeURIComponent(token)}/register`, {
        username,
        email,
        displayName,
        password,
      });
      await adopt(s);
      navigate('/', true);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<section class="card narrow stack auth">
  <p class="logo-hero"><Logo size={72} /></p>
  <h1>{preview ? t('invite.titleNamed', { household: preview.householdName }) : t('invite.title')}</h1>
  {#if dead}
    <p role="alert">{t('invite.invalid')}</p>
    {#if app.user}
      <a class="btn" href={href('/')}>{t('verifyEmail.continue')}</a>
    {:else}
      <a class="btn" href={href('/signin')}>{t('verifyEmail.toSignIn')}</a>
    {/if}
  {:else if app.user}
    <p>{lead}{t('invite.acceptBody', { name: app.user.displayName })}</p>
    <Alert message={message()} />
    <Button {busy} onclick={accept}>{t('invite.accept')}</Button>
  {:else}
    <p class="muted">{lead}{t('invite.registerBody')}</p>
    <form class="stack" onsubmit={register}>
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
        hint={app.instance.minPasswordLength ? t('field.passwordHint', { n: app.instance.minPasswordLength }) : ''}
        required
        error={pwCodes.includes(err.code) ? err.message : ''}
      />
      {#if !pwCodes.includes(err.code)}<Alert message={message()} />{/if}
      <LegalLinks agree />
      <Button type="submit" {busy}>{t('invite.register')}</Button>
    </form>
    <p class="muted">
      {t('invite.haveAccount')}
      <a href={href(`/signin?next=${encodeURIComponent('/invite/' + token)}`)}>{t('signIn.title')}</a>
    </p>
  {/if}
</section>
