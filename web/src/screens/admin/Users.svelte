<script lang="ts">
  import { tick } from 'svelte';
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { failure } from '../../lib/errors';
  import { formatDate, t } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  type U = { id: string; username: string; email?: string; displayName: string; isAdmin: boolean; totpEnabled: boolean; lastActiveAt: string };
  let users = $state<U[]>([]);
  let err = $state('');
  let username = $state('');
  let email = $state('');
  let displayName = $state('');
  let password = $state('');
  let isAdmin = $state(false);
  let creating = $state(false);
  let busy = $state(false);
  let cerr = $state({ code: '', message: '' });
  let resetFor = $state<U | null>(null);
  let newPassword = $state('');
  const pwCodes = ['password_too_short', 'password_too_long', 'password_invalid'];

  async function openCreate(): Promise<void> {
    creating = true;
    await tick();
    document.querySelector<HTMLInputElement>('#create-user input')?.focus();
  }

  async function load(): Promise<void> {
    try {
      users = await api<U[]>('GET', '/admin/users');
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function create(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    cerr = { code: '', message: '' };
    try {
      await withStepUp(() => api('POST', '/admin/users', { username, email, displayName, password, isAdmin }));
      username = email = displayName = password = '';
      isAdmin = false;
      creating = false;
      toast(t('admin.created'));
      await load();
    } catch (x) {
      cerr = failure(x);
    } finally {
      busy = false;
    }
  }

  async function reset(): Promise<void> {
    if (!resetFor) return;
    busy = true;
    try {
      const r = await withStepUp(() => api<{ password: string }>('POST', `/admin/users/${resetFor!.id}/reset-password`));
      newPassword = r.password;
    } catch (x) {
      err = failure(x).message;
      resetFor = null;
    } finally {
      busy = false;
    }
  }
</script>

<section class="card" aria-labelledby="ad-users">
  <h2 id="ad-users">{t('admin.users')}</h2>
  <Alert message={err} />
  <ul class="list">
    {#each users as u (u.id)}
      <li>
        <span>
          <strong>{u.displayName}</strong>
          {#if u.isAdmin}<span class="badge">{t('nav.admin')}</span>{/if}
          <br /><span class="muted meta">{u.username}{u.email ? ` · ${u.email}` : ''} · {t('admin.lastActive', { date: formatDate(new Date(u.lastActiveAt), { dateStyle: 'medium' }) })}</span>
        </span>
        {#if !u.isAdmin}<Button variant="quiet" onclick={() => (resetFor = u)}>{t('admin.reset')}</Button>{/if}
      </li>
    {/each}
  </ul>

  {#if creating}
    <h3 class="mt-lg">{t('admin.createUser')}</h3>
    <form class="stack" id="create-user" onsubmit={create}>
      <Field label={t('field.displayName')} bind:value={displayName} required maxlength={100} />
      <Field label={t('field.username')} hint={t('field.usernameHint')} bind:value={username} autocapitalize="none" spellcheck="false" required />
      <Field label={t('field.emailOptional')} type="email" bind:value={email} />
      <Field label={t('admin.tempPassword')} type="password" bind:value={password} autocomplete="new-password" required error={pwCodes.includes(cerr.code) ? cerr.message : ''} />
      <label class="check"><input type="checkbox" bind:checked={isAdmin} />{t('admin.makeAdmin')}</label>
      {#if !pwCodes.includes(cerr.code)}<Alert message={cerr.message} />{/if}
      <div class="actions">
        <Button type="submit" {busy}>{t('admin.create')}</Button>
        <Button variant="quiet" onclick={() => ((creating = false), (cerr = { code: '', message: '' }))}>{t('common.cancel')}</Button>
      </div>
    </form>
  {:else}
    <div class="actions mt-md"><Button variant="quiet" onclick={openCreate}>{t('admin.create')}</Button></div>
  {/if}
</section>

<Confirm
  open={!!resetFor && !newPassword}
  title={t('admin.resetTitle', { name: resetFor?.displayName ?? '' })}
  message={t('admin.resetWarn')}
  confirmLabel={t('admin.reset')}
  {busy}
  onconfirm={reset}
  oncancel={() => (resetFor = null)}
/>
<Dialog
  open={!!newPassword}
  title={t('password.new')}
  onclose={() => {
    newPassword = '';
    resetFor = null;
  }}
>
  <div class="stack">
    <p>{t('admin.newPasswordBody')}</p>
    <p class="mono">{newPassword}</p>
    <div class="row">
      <Button variant="quiet" onclick={() => copyText(newPassword)}>{t('common.copy')}</Button>
      <Button
        onclick={() => {
          newPassword = '';
          resetFor = null;
        }}>{t('common.done')}</Button
      >
    </div>
  </div>
</Dialog>

<style>
  /* Same size as the other settings lists (name, then a smaller muted line). */
  .meta {
    font-size: 0.875rem;
  }
</style>
