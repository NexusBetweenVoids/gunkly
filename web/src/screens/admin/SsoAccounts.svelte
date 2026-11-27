<script lang="ts">
  // The single sign-on settings kept in the instance settings (PATCH /admin/settings): whether a first
  // SSO sign-in creates an account, and the provider name on the sign-in button. Part of the SSO card.
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  type S = { ssoConfigured: boolean; ssoCreateAccounts: boolean; ssoName: string; ssoNameDefault: string };
  let s = $state<S | null>(null);
  let busy = $state(false);
  let err = $state('');

  api<S>('GET', '/admin/settings')
    .then((v) => (s = v))
    .catch((x) => (err = failure(x).message));

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!s) return;
    busy = true;
    err = '';
    try {
      s = await withStepUp(() => api<S>('PATCH', '/admin/settings', { ssoCreateAccounts: s!.ssoCreateAccounts, ssoName: s!.ssoName }));
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

{#if s?.ssoConfigured}
  <form class="stack accounts" onsubmit={save} aria-labelledby="sso-accounts">
    <h3 id="sso-accounts">{t('admin.ssoAccounts')}</h3>
    <div class="stack-sm">
      <label class="check"><input type="checkbox" bind:checked={s.ssoCreateAccounts} aria-describedby="sso-hint" />{t('admin.ssoCreateAccounts')}</label>
      <p class="hint" id="sso-hint">{t('admin.ssoCreateAccountsHint')}</p>
    </div>
    <Field
      label={t('admin.ssoName')}
      hint={s.ssoNameDefault ? t('admin.ssoNameHintDefault', { name: s.ssoNameDefault }) : t('admin.ssoNameHint')}
      bind:value={s.ssoName}
      maxlength={50}
      placeholder={s.ssoNameDefault || t('admin.ssoNamePlaceholder')}
    />
    <Alert message={err} />
    <div><Button type="submit" {busy}>{t('common.save')}</Button></div>
  </form>
{:else}
  <Alert message={err} />
{/if}

<style>
  .accounts {
    padding-block-start: var(--space);
    border-block-start: 1px solid var(--border);
  }
</style>
