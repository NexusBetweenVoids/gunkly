<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api, ApiError, download } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t, type Key } from '../../lib/i18n';
  import { navigate } from '../../lib/router';
  import { refreshHouseholds, selectHousehold } from '../../lib/session.svelte';
  import { toast } from '../../lib/toast.svelte';

  type Summary = {
    household: { id: string; name: string };
    areasCreated: number;
    areasMerged: number;
    choresCreated: number;
    completionsImported: number;
    unassigned: number;
  };

  let { id }: { id: string } = $props();
  const MAX_BYTES = 5 * 1024 * 1024;
  const REASONS: Record<string, Key> = {
    not_json: 'import.err.not_json',
    format: 'import.err.format',
    version: 'import.err.version',
    required: 'import.err.required',
    invalid: 'import.err.invalid',
    too_long: 'import.err.too_long',
    limit_chores: 'import.err.limit_chores',
    limit_areas: 'import.err.limit_areas',
    limit_reminders: 'import.err.limit_reminders',
    limit_completions: 'import.err.limit_completions',
  };

  let file = $state<File | null>(null);
  let mode = $state<'new' | 'merge'>('new');
  let busy = $state(false);
  let exporting = $state(false);
  let err = $state('');
  let done = $state<{ mode: 'new' | 'merge'; summary: Summary } | null>(null);

  function message(x: unknown): string {
    if (x instanceof ApiError && x.code === 'import_invalid') {
      const m = t(REASONS[x.reason] ?? 'err.invalid');
      return x.path ? t('import.errorAt', { message: m, path: x.path }) : m;
    }
    return failure(x).message;
  }

  async function exportIt(): Promise<void> {
    err = '';
    exporting = true;
    try {
      await download(`/households/${id}/export`, 'gunkly-household-export.json');
      toast(t('export.downloaded'));
    } catch (x) {
      err = message(x);
    } finally {
      exporting = false;
    }
  }

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = '';
    done = null;
    if (!file) {
      err = t('transfer.chooseFile');
      return;
    }
    if (file.size > MAX_BYTES) {
      err = t('transfer.tooLarge');
      return;
    }
    busy = true;
    try {
      let doc: unknown;
      try {
        doc = JSON.parse(await file.text());
      } catch {
        throw new ApiError(400, 'import_invalid', 0, 'not_json');
      }
      const path = mode === 'new' ? '/households/import' : `/households/${id}/import`;
      done = { mode, summary: await api<Summary>('POST', path, doc) };
      if (mode === 'new') await refreshHouseholds();
    } catch (x) {
      err = message(x);
    } finally {
      busy = false;
    }
  }

  function open(hid: string): void {
    selectHousehold(hid);
    navigate(`/households/${hid}`);
  }
</script>

<section class="card" aria-labelledby="h-transfer">
  <h2 id="h-transfer">{t('transfer.title')}</h2>
  <p class="muted">{t('transfer.intro')}</p>
  <div class="mt-md">
    <Button variant="quiet" onclick={exportIt} busy={exporting}>{t('transfer.export')}</Button>
  </div>

  <form class="stack mt-md" onsubmit={submit}>
    <h3>{t('transfer.importTitle')}</h3>
    <Field label={t('transfer.file')} hint={t('transfer.fileHint')}>
      {#snippet children({ id: fid, describedby })}
        <input
          id={fid}
          type="file"
          accept="application/json,.json"
          aria-describedby={describedby}
          onchange={(e) => (file = e.currentTarget.files?.[0] ?? null)}
        />
      {/snippet}
    </Field>
    <fieldset class="plain-fieldset stack-sm">
      <legend class="label">{t('transfer.mode')}</legend>
      <label class="check"><input type="radio" bind:group={mode} value="new" />{t('transfer.modeNew')}</label>
      <label class="check"><input type="radio" bind:group={mode} value="merge" />{t('transfer.modeMerge')}</label>
      {#if mode === 'merge'}<p class="hint">{t('transfer.modeMergeHint')}</p>{/if}
    </fieldset>
    <div aria-live="polite"><Alert message={err} /></div>
    {#if done}
      <div class="stack-sm" role="status">
        <p>{t(done.mode === 'new' ? 'transfer.doneNew' : 'transfer.doneMerge', { name: done.summary.household.name })}</p>
        <ul>
          <li>{t('transfer.sumAreas', { created: done.summary.areasCreated, merged: done.summary.areasMerged })}</li>
          <li>{t('transfer.sumChores', { count: done.summary.choresCreated })}</li>
          <li>{t('transfer.sumCompletions', { count: done.summary.completionsImported })}</li>
          {#if done.summary.unassigned > 0}
            <li>{t('transfer.sumUnassigned', { count: done.summary.unassigned })}</li>
          {/if}
        </ul>
        {#if done.mode === 'new'}
          <div><Button variant="quiet" onclick={() => open(done!.summary.household.id)}>{t('transfer.open')}</Button></div>
        {/if}
      </div>
    {/if}
    <div><Button type="submit" {busy}>{t('transfer.import')}</Button></div>
  </form>
</section>
