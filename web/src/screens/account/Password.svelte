<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app, refreshMe } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';
  import MethodRow from './MethodRow.svelte';

  let open = $state(false);
  let pw = $state('');
  let again = $state('');
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let mismatch = $state('');
  const pwCodes = ['password_too_short', 'password_too_long', 'password_invalid'];

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = { code: '', message: '' };
    mismatch = pw === again ? '' : t('password.mismatch');
    if (mismatch) return;
    busy = true;
    try {
      await withStepUp(() => api<void>('POST', '/account/password', { newPassword: pw }));
      pw = again = '';
      open = false;
      toast(t('password.changed'));
      if (!app.user?.hasPassword) await refreshMe();
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<MethodRow
  id="a-pw"
  name={t('field.password')}
  hint={app.user?.hasPassword ? t('ways.passwordHint') : t('ways.passwordNone')}
  state={app.user?.hasPassword ? t('ways.on') : t('ways.notSet')}
  tone={app.user?.hasPassword ? 'ok' : ''}
  {open}
>
  {#snippet action()}
    <Button variant="quiet" aria-expanded={open} onclick={() => (open = !open)}
      >{app.user?.hasPassword ? t('ways.change') : t('ways.set')}</Button
    >
  {/snippet}
  <form class="stack" onsubmit={submit}>
    <Field
      label={t('password.new')}
      type="password"
      bind:value={pw}
      autocomplete="new-password"
      required
      error={pwCodes.includes(err.code) ? err.message : ''}
    />
    <Field label={t('password.confirm')} type="password" bind:value={again} autocomplete="new-password" required error={mismatch} />
    {#if !pwCodes.includes(err.code)}<Alert message={err.message} />{/if}
    <div class="actions">
      <Button type="submit" {busy}>{t('password.change')}</Button>
      <Button variant="quiet" onclick={() => (open = false)}>{t('common.cancel')}</Button>
    </div>
  </form>
</MethodRow>
