<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { toast } from '../../lib/toast.svelte';
  import type { Area } from '../../lib/types';

  let { id, owner }: { id: string; owner: boolean } = $props();
  const base = $derived(`/households/${id}/areas`);
  let areas = $state<Area[]>([]);
  let err = $state('');
  let name = $state('');
  let renaming = $state<Area | null>(null);
  let rename = $state('');
  let deleting = $state<Area | null>(null);

  async function load(): Promise<void> {
    try {
      areas = await api<Area[]>('GET', base);
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function run(fn: () => Promise<unknown>): Promise<void> {
    err = '';
    try {
      await fn();
      renaming = deleting = null;
      await load();
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x).message;
    }
  }
</script>

<section class="card" aria-labelledby="h-areas">
  <h2 id="h-areas">{t('areas.title')}</h2>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if areas.length}
    <ul class="list">
      {#each areas as a (a.id)}
        <li>
          <span>{a.name}</span>
          <span class="row">
            <Button variant="quiet" aria-label={t('areas.renameFor', { name: a.name })} onclick={() => ((renaming = a), (rename = a.name))}>{t('areas.rename')}</Button>
            <Button variant="danger" aria-label={t('areas.deleteFor', { name: a.name })} onclick={() => (deleting = a)}>{t('common.delete')}</Button>
          </span>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted">{t('areas.none')}</p>
    {#if owner}<div><Button variant="quiet" onclick={() => run(() => api('PATCH', `/households/${id}`, { starterAreas: true }))}>{t('areas.addStarter')}</Button></div>{/if}
  {/if}
  <form class="row mt-md" onsubmit={(e) => { e.preventDefault(); void run(() => api('POST', base, { name: name.trim() }).then(() => (name = ''))); }}>
    <Field label={t('areas.new')} bind:value={name} required maxlength={100} />
    <Button type="submit">{t('areas.add')}</Button>
  </form>
</section>

<Dialog open={!!renaming} title={t('areas.renameTitle')} onclose={() => (renaming = null)}>
  <form class="stack" onsubmit={(e) => { e.preventDefault(); void run(() => api('PATCH', `${base}/${renaming!.id}`, { name: rename.trim() })); }}>
    <Field label={t('areas.name')} bind:value={rename} required maxlength={100} />
    <div class="row">
      <Button type="submit">{t('common.save')}</Button>
      <Button variant="quiet" onclick={() => (renaming = null)}>{t('common.cancel')}</Button>
    </div>
  </form>
</Dialog>
<Confirm
  open={!!deleting}
  title={t('chore.deleteTitle', { name: deleting?.name ?? '' })}
  message={t('areas.deleteWarn')}
  confirmLabel={t('common.delete')}
  onconfirm={() => run(() => api('DELETE', `${base}/${deleting!.id}`))}
  oncancel={() => (deleting = null)}
/>
