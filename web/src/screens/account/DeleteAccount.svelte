<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { navigate } from '../../lib/router.svelte';
  import { app, clearSession } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';

  type Candidate = { userId: string; displayName: string; role: string };
  type Preview = {
    deleteHouseholds: { id: string; name: string }[];
    needOwner: { id: string; name: string; candidates: Candidate[] }[];
  };
  let preview = $state<Preview | null>(null);
  let owners = $state<Record<string, string>>({});
  let confirm = $state('');
  let busy = $state(false);
  let err = $state('');

  async function start(): Promise<void> {
    busy = true;
    err = '';
    try {
      const p = await api<Preview>('GET', '/account/deletion-preview');
      // Default to the longest-standing member; the user may change it.
      owners = Object.fromEntries(p.needOwner.map((h) => [h.id, h.candidates[0]?.userId ?? '']));
      preview = p;
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function remove(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      await withStepUp(() => api('DELETE', '/account', { confirmUsername: confirm, newOwners: owners }));
      clearSession();
      navigate('/signin');
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

<section class="card" aria-labelledby="a-delete">
  <h2 id="a-delete">{t('delete.title')}</h2>
  <p class="muted">{t('delete.intro')}</p>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if !preview}
    <div class="mt-md"><Button variant="danger" onclick={start} {busy}>{t('delete.start')}</Button></div>
  {:else}
    <form class="stack" onsubmit={remove}>
      {#if preview.deleteHouseholds.length}
        <p>{t('delete.households')}</p>
        <ul>
          {#each preview.deleteHouseholds as h (h.id)}<li>{h.name}</li>{/each}
        </ul>
      {/if}
      {#each preview.needOwner as h (h.id)}
        <Field label={t('delete.newOwner', { name: h.name })}>
          {#snippet children({ id })}
            <select {id} bind:value={owners[h.id]} required>
              {#each h.candidates as c (c.userId)}<option value={c.userId}>{c.displayName}</option>{/each}
            </select>
          {/snippet}
        </Field>
      {/each}
      <Field label={t('delete.confirmLabel', { username: app.user?.username ?? '' })} bind:value={confirm} autocomplete="off" required />
      <div class="row">
        <Button type="submit" variant="danger" {busy}>{t('delete.confirm')}</Button>
        <Button variant="quiet" onclick={() => (preview = null)}>{t('common.cancel')}</Button>
      </div>
    </form>
  {/if}
</section>
