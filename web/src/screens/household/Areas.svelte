<script lang="ts">
  import { tick } from 'svelte';
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Icon from '../../components/Icon.svelte';
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

  async function run(fn: () => Promise<unknown>): Promise<boolean> {
    err = '';
    try {
      await fn();
      renaming = deleting = null;
      await load();
      toast(t('common.saved'));
      return true;
    } catch (x) {
      err = failure(x).message;
      return false;
    }
  }

  // Rename in place: the row becomes a field; Escape or Cancel puts the name back.
  async function startRename(a: Area): Promise<void> {
    renaming = a;
    rename = a.name;
    await tick();
    document.getElementById(`area-rename-${a.id}`)?.focus();
  }
  async function stopRename(a: Area): Promise<void> {
    renaming = null;
    await tick();
    document.getElementById(`area-edit-${a.id}`)?.focus();
  }
</script>

<section class="card" aria-labelledby="h-areas">
  <h2 id="h-areas">{t('areas.title')}</h2>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if areas.length}
    <ul class="areas">
      {#each areas as a (a.id)}
        <li>
          {#if renaming?.id === a.id}
            <form
              class="inline"
              onsubmit={(e) => {
                e.preventDefault();
                const r = renaming!;
                void run(() => api('PATCH', `${base}/${r.id}`, { name: rename.trim() })).then((ok) => {
                  if (ok) void stopRename(r);
                });
              }}
            >
              <label class="visually-hidden" for="area-rename-{a.id}">{t('areas.name')}</label>
              <input
                id="area-rename-{a.id}"
                bind:value={rename}
                required
                maxlength={100}
                onkeydown={(e) => {
                  if (e.key === 'Escape') {
                    e.preventDefault();
                    void stopRename(a);
                  }
                }}
              />
              <Button type="submit">{t('common.save')}</Button>
              <Button variant="quiet" onclick={() => stopRename(a)}>{t('common.cancel')}</Button>
            </form>
          {:else}
            <span class="name">{a.name}</span>
            <button type="button" class="icon" id="area-edit-{a.id}" aria-label={t('areas.renameFor', { name: a.name })} onclick={() => startRename(a)}><Icon name="edit" size={18} /></button>
            <button type="button" class="icon" aria-label={t('areas.deleteFor', { name: a.name })} onclick={() => (deleting = a)}><Icon name="trash" size={18} /></button>
          {/if}
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted">{t('areas.none')}</p>
    {#if owner}<div><Button variant="quiet" onclick={() => run(() => api('PATCH', `/households/${id}`, { starterAreas: true }))}>{t('areas.addStarter')}</Button></div>{/if}
  {/if}
  <form class="inline add" class:first={!areas.length} onsubmit={(e) => { e.preventDefault(); void run(() => api('POST', base, { name: name.trim() }).then(() => (name = ''))); }}>
    <label class="visually-hidden" for="area-new">{t('areas.new')}</label>
    <input id="area-new" bind:value={name} required maxlength={100} placeholder={t('areas.newPlaceholder')} />
    <Button type="submit">{t('areas.add')}</Button>
  </form>
</section>

<Confirm
  open={!!deleting}
  title={t('chore.deleteTitle', { name: deleting?.name ?? '' })}
  message={t('areas.deleteWarn')}
  confirmLabel={t('common.delete')}
  onconfirm={() => run(() => api('DELETE', `${base}/${deleting!.id}`))}
  oncancel={() => (deleting = null)}
/>

<style>
  .areas {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .areas li {
    display: flex;
    align-items: center;
    gap: 4px;
    min-block-size: var(--tap);
    border-block-start: 1px solid var(--border);
  }
  .areas li:first-child {
    border-block-start: 0;
  }
  .name {
    flex: 1;
    min-inline-size: 0;
  }
  .icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    inline-size: var(--tap);
    block-size: var(--tap);
    flex: none;
    padding: 0;
    border: 0;
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-2);
    cursor: pointer;
  }
  .icon:hover {
    background: var(--surface-2);
    color: var(--text);
  }
  .inline {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    padding-block: 6px;
  }
  .inline input {
    flex: 1;
    min-inline-size: 0;
  }
  .add {
    margin-block-start: 6px;
    padding-block: 12px 0;
    border-block-start: 1px solid var(--border);
  }
  .add.first {
    border-block-start: 0;
  }
</style>
