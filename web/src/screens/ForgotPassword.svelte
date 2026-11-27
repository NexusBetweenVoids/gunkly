<script lang="ts">
  import { href } from '../lib/base';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';

  let login = $state('');
  let busy = $state(false);
  let sent = $state(false);
  let err = $state({ code: '', message: '' });

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      await api<unknown>('POST', '/password-reset/request', { login });
      sent = true; // the same answer whether or not the account exists
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<section class="card narrow auth">
  <h1>{t('forgot.title')}</h1>
  {#if sent}
    <p role="status">{t('forgot.sent')}</p>
  {:else}
    <p class="muted">{t('forgot.intro')}</p>
    <form class="stack" onsubmit={submit}>
      <Field
        label={t('signIn.username')}
        bind:value={login}
        autocomplete="username"
        autocapitalize="none"
        spellcheck="false"
        required
      />
      <Alert message={err.message} />
      <Button type="submit" {busy}>{t('forgot.submit')}</Button>
    </form>
  {/if}
  <p><a href={href('/signin')}>{t('forgot.back')}</a></p>
</section>
