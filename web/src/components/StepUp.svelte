<script lang="ts">
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { app } from '../lib/session.svelte';
  import { resolveStepUp, stepUp } from '../lib/stepup.svelte';
  import Alert from './Alert.svelte';
  import Button from './Button.svelte';
  import Dialog from './Dialog.svelte';
  import Field from './Field.svelte';
  import SecondFactor from './SecondFactor.svelte';

  let password = $state('');
  let code = $state('');
  let recovery = $state(false);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      const body: Record<string, string> = { password };
      if (app.user?.totpEnabled) body[recovery ? 'recoveryCode' : 'totp'] = code.trim();
      await api<void>('POST', '/session/step-up', body);
      password = code = '';
      resolveStepUp(true);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog open={stepUp.open} title={t('stepUp.title')} onclose={() => resolveStepUp(false)}>
  <form class="stack" onsubmit={submit}>
    <p class="muted">{t('stepUp.body')}</p>
    <Field
      label={t('field.password')}
      type="password"
      bind:value={password}
      autocomplete="current-password"
      required
      error={err.code === 'invalid_credentials' ? err.message : ''}
    />
    {#if app.user?.totpEnabled}
      <SecondFactor bind:code bind:recovery error={err.code === 'invalid_code' ? err.message : ''} />
    {/if}
    {#if err.code !== 'invalid_credentials' && err.code !== 'invalid_code'}<Alert message={err.message} />{/if}
    <div class="row">
      <Button type="submit" {busy}>{t('stepUp.confirm')}</Button>
      <Button variant="quiet" onclick={() => resolveStepUp(false)}>{t('common.cancel')}</Button>
    </div>
  </form>
</Dialog>
