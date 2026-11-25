<script lang="ts" module>
  export type FieldSpec = {
    key: string;
    label: string;
    hint?: string;
    secret?: boolean;
    kind?: 'text' | 'bool' | 'number' | 'select';
    options?: { value: string; label: string }[];
    placeholder?: string;
  };
</script>

<script lang="ts">
  // A card of admin configuration fields (GET/PATCH /admin/config): environment-locked fields are
  // read-only, secrets are write-only, Save sends only what changed.
  import type { Snippet } from 'svelte';
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Select from '../../components/Select.svelte';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { toast } from '../../lib/toast.svelte';
  import ConfigField from './ConfigField.svelte';
  import { changed, loadConfig, saveConfig, type Config } from './config.svelte';

  let {
    id,
    title,
    fields,
    intro,
    after,
    onsaved,
  }: { id: string; title: string; fields: FieldSpec[]; intro?: Snippet; after?: Snippet<[Config]>; onsaved?: (c: Config) => void } = $props();

  let cfg = $state<Config | null>(null);
  let draft = $state<Record<string, string>>({});
  let secrets = $state<Record<string, string>>({});
  let err = $state('');
  let busy = $state(false);
  let gen = $state(0); // remounts secret fields after a save

  function adopt(c: Config): void {
    cfg = c;
    const d: Record<string, string> = {};
    const sc: Record<string, string> = {};
    for (const f of fields) {
      if (f.secret) sc[f.key] = '';
      else d[f.key] = c[f.key]?.value ?? '';
    }
    draft = d;
    secrets = sc;
    gen++;
  }

  async function load(): Promise<void> {
    try {
      adopt(await loadConfig());
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!cfg) return;
    err = '';
    const values = changed(cfg, draft, secrets);
    if (!Object.keys(values).length) return;
    busy = true;
    try {
      adopt(await saveConfig(values));
      toast(t('common.saved'));
      onsaved?.(cfg!);
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function remove(key: string): Promise<void> {
    err = '';
    try {
      adopt(await saveConfig({ [key]: null }));
      toast(t('common.saved'));
      onsaved?.(cfg!);
    } catch (x) {
      err = failure(x).message;
    }
  }

  const allLocked = $derived(!!cfg && fields.every((f) => cfg![f.key]?.locked));
</script>

<section class="card" aria-labelledby="cfg-{id}">
  <h2 id="cfg-{id}">{title}</h2>
  {@render intro?.()}
  <div aria-live="polite"><Alert message={err} /></div>
  {#if cfg}
    <form class="stack" onsubmit={save}>
      {#each fields as f (f.key)}
        {@const field = cfg[f.key]}
        {#if f.kind === 'bool'}
          {#if field?.locked}
            <p class="hint">{f.label}: {field.value === 'true' ? t('config.yes') : t('config.no')} — {t('config.locked', { env: field.env })}</p>
          {:else}
            <label class="check"><input type="checkbox" checked={draft[f.key] === 'true'} onchange={(e) => (draft[f.key] = String(e.currentTarget.checked))} />{f.label}</label>
            {#if f.hint}<p class="hint">{f.hint}</p>{/if}
          {/if}
        {:else if f.kind === 'select' && !field?.locked}
          <Select label={f.label} options={f.options ?? []} bind:value={draft[f.key]} />
        {:else if f.secret}
          {#key gen}
            <ConfigField {field} label={f.label} hint={f.hint} secret bind:value={secrets[f.key]} onremove={() => remove(f.key)} />
          {/key}
        {:else}
          <ConfigField {field} label={f.label} hint={f.hint} bind:value={draft[f.key]} placeholder={f.placeholder} />
        {/if}
      {/each}
      {#if !allLocked}<div><Button type="submit" {busy}>{t('common.save')}</Button></div>{/if}
    </form>
    {@render after?.(cfg)}
  {:else if !err}
    <p role="status" class="muted">{t('common.loading')}</p>
  {/if}
</section>
