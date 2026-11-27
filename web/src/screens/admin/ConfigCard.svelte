<script lang="ts" module>
  export type FieldSpec = {
    key: string;
    label: string;
    hint?: string;
    secret?: boolean;
    kind?: 'text' | 'bool' | 'number' | 'select';
    options?: { value: string; label: string }[];
    placeholder?: string;
    /** The card's status badge already says this (an on/off switch): not repeated when locked. */
    status?: boolean;
  };

  /** "GUNKLY_SMTP_*" for several variables that share a prefix, else the names. */
  export function envPattern(envs: string[]): string {
    if (envs.length === 1) return envs[0]!;
    let p = envs[0] ?? '';
    for (const e of envs) while (!e.startsWith(p)) p = p.slice(0, -1);
    p = p.slice(0, p.lastIndexOf('_') + 1);
    return p.length > 'GUNKLY_'.length ? `${p}*` : envs.join(', ');
  }
</script>

<script lang="ts">
  // A card of admin configuration fields (GET/PATCH /admin/config): environment-locked fields are
  // read-only, secrets are write-only, Save sends only what changed.
  import type { Snippet } from 'svelte';
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Icon from '../../components/Icon.svelte';
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
    badge,
    intro,
    after,
    onsaved,
  }: { id: string; title: string; fields: FieldSpec[]; badge?: Snippet; intro?: Snippet; after?: Snippet<[Config]>; onsaved?: (c: Config) => void } = $props();

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

  // Fields the environment sets: a read-only list with one note, instead of inputs that look editable.
  const locked = $derived(cfg ? fields.filter((f) => cfg![f.key]?.locked && !f.status) : []);
  const open = $derived(cfg ? fields.filter((f) => !cfg![f.key]?.locked) : []);
  function shown(f: FieldSpec): { text: string; unset: boolean } {
    const v = cfg?.[f.key];
    if (f.secret) return v?.set ? { text: t('config.secretSet'), unset: false } : { text: t('config.notSet'), unset: true };
    const raw = v?.value ?? '';
    if (f.kind === 'bool') return { text: raw === 'true' ? t('admin.ssoOn') : t('admin.ssoOff'), unset: false };
    if (!raw) return { text: t('config.notSet'), unset: true };
    return { text: f.options?.find((o) => o.value === raw)?.label ?? raw, unset: false };
  }
</script>

<section class="card stack" aria-labelledby="cfg-{id}">
  <div class="head">
    <h2 id="cfg-{id}">{title}</h2>
    {@render badge?.()}
  </div>
  {@render intro?.()}
  <div class="live" aria-live="polite"><Alert message={err} /></div>
  {#if cfg}
    {#if locked.length}
      <div class="locked" data-testid="cfg-{id}-locked">
        <p class="note"><Icon name="lock" size={16} />{t('config.lockedNote', { env: envPattern(locked.map((f) => cfg![f.key]!.env)) })}</p>
        <dl>
          {#each locked as f (f.key)}
            {@const v = shown(f)}
            <dt>{f.label}</dt>
            <dd class:muted={v.unset} class:mono={!v.unset && !f.secret && !f.options && f.kind !== 'bool'}>{v.text}</dd>
          {/each}
        </dl>
      </div>
    {/if}
    {#if open.length}
      <form class="stack" onsubmit={save}>
        {#each open as f (f.key)}
          {#if f.kind === 'bool'}
            <div class="stack-sm">
              <label class="check"><input type="checkbox" checked={draft[f.key] === 'true'} onchange={(e) => (draft[f.key] = String(e.currentTarget.checked))} />{f.label}</label>
              {#if f.hint}<p class="hint">{f.hint}</p>{/if}
            </div>
          {:else if f.kind === 'select'}
            <Select label={f.label} options={f.options ?? []} bind:value={draft[f.key]} hint={f.hint} />
          {:else if f.secret}
            {#key gen}
              <ConfigField field={cfg[f.key]} label={f.label} hint={f.hint} secret bind:value={secrets[f.key]} onremove={() => remove(f.key)} />
            {/key}
          {:else}
            <ConfigField field={cfg[f.key]} label={f.label} hint={f.hint} bind:value={draft[f.key]} placeholder={f.placeholder} />
          {/if}
        {/each}
        <div><Button type="submit" {busy}>{t('common.save')}</Button></div>
      </form>
    {/if}
    {@render after?.(cfg)}
  {:else if !err}
    <p role="status" class="muted">{t('common.loading')}</p>
  {/if}
</section>

<style>
  /* An empty live region takes no slot in the card's spacing. */
  .live:empty {
    display: none;
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 12px;
  }
  .head h2 {
    flex: 1;
    margin: 0;
  }
  .locked {
    display: grid;
    gap: 10px;
  }
  .note {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    font-size: 0.875rem;
    color: var(--text-2);
  }
  .note :global(svg) {
    flex: none;
  }
  dl {
    display: grid;
    grid-template-columns: minmax(7rem, max-content) minmax(0, 1fr);
    gap: 8px 16px;
    margin: 0;
    padding-block: 12px;
    padding-inline: 14px;
    border-radius: var(--radius);
    background: var(--surface-2);
    font-size: 0.9375rem;
  }
  dt {
    color: var(--text-2);
  }
  dd {
    margin: 0;
    min-inline-size: 0;
    overflow-wrap: anywhere;
  }
  dd.mono {
    user-select: text;
  }
</style>
