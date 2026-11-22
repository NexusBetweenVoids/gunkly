<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';

  let email = $state('');
  let busy = $state(false);
  let sent = $state(false);
  let err = $state({ code: '', message: '' });

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    sent = false;
    err = { code: '', message: '' };
    try {
      await withStepUp(() => api<unknown>('PATCH', '/account/email', { email }));
      email = '';
      sent = true;
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

{#if app.instance.emailEnabled}
  <section class="card" aria-labelledby="a-email">
    <h2 id="a-email">{t('emailSection.title')}</h2>
    <div class="stack">
      {#if app.user?.email}
        <p>
          <span class="mono">{app.user.email}</span>
          <span class="badge">{app.user.emailVerified ? t('emailSection.verified') : t('emailSection.unverified')}</span>
        </p>
        {#if !app.user.emailVerified && !app.instance.hostedMode}<p class="muted">{t('emailSection.unverifiedNote')}</p>{/if}
      {:else}
        <p class="muted">{t('emailSection.none')}</p>
      {/if}
      <form class="stack" onsubmit={submit}>
        <Field
          label={t('emailSection.new')}
          hint={t('emailSection.hint')}
          type="email"
          bind:value={email}
          autocomplete="email"
          required
          error={err.code === 'invalid' ? err.message : ''}
        />
        {#if err.code !== 'invalid'}<Alert message={err.message} />{/if}
        {#if sent}<p role="status">{t('emailSection.sent')}</p>{/if}
        <div><Button type="submit" {busy}>{app.user?.email ? t('emailSection.change') : t('emailSection.add')}</Button></div>
      </form>
    </div>
  </section>
{/if}
