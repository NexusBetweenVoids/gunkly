<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t, type Key } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import { toast } from '../../lib/toast.svelte';
  import ConfigField from './ConfigField.svelte';
  import { changed, loadConfig, saveConfig, type Config } from './config.svelte';

  let cfg = $state<Config | null>(null);
  let enabled = $state(false);
  let url = $state('');
  let model = $state('');
  let key = $state('');
  let err = $state('');
  let busy = $state(false);
  let testing = $state(false);
  let result = $state<{ ok: boolean; ms: number; error?: string } | null>(null);

  const FAIL: Record<string, Key> = {
    unreachable: 'aiAdmin.fail.unreachable', auth: 'aiAdmin.fail.auth', not_found: 'aiAdmin.fail.notFound',
    rate_limited: 'aiAdmin.fail.rateLimited', timeout: 'aiAdmin.fail.timeout', bad_response: 'aiAdmin.fail.badResponse',
    server_error: 'aiAdmin.fail.server',
  };

  function adopt(c: Config): void {
    cfg = c;
    enabled = c['ai.enabled']?.value === 'true';
    url = c['ai.url']?.value ?? '';
    model = c['ai.model']?.value ?? '';
    key = '';
  }

  async function load(): Promise<void> {
    try {
      adopt(await loadConfig());
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function refreshInstance(): Promise<void> {
    try {
      app.instance = await api('GET', '/instance');
    } catch {
      // the flag catches up on the next page load
    }
  }

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!cfg) return;
    err = '';
    result = null;
    const values = changed(cfg, { 'ai.enabled': String(enabled), 'ai.url': url.trim(), 'ai.model': model.trim() }, { 'ai.key': key.trim() });
    if (!Object.keys(values).length) return;
    busy = true;
    try {
      adopt(await saveConfig(values));
      toast(t('common.saved'));
      await refreshInstance();
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function removeKey(): Promise<void> {
    err = '';
    try {
      adopt(await saveConfig({ 'ai.key': null }));
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x).message;
    }
  }

  async function test(): Promise<void> {
    err = '';
    testing = true;
    result = null;
    try {
      result = await api('POST', '/admin/ai/test');
    } catch (x) {
      err = failure(x).message;
    } finally {
      testing = false;
    }
  }

  const ready = $derived(!!cfg && cfg['ai.enabled']?.value === 'true' && !!cfg['ai.url']?.value && !!cfg['ai.model']?.value);
</script>

<section class="card" aria-labelledby="ad-ai">
  <h2 id="ad-ai">{t('aiAdmin.title')}</h2>
  <p>{t('aiAdmin.intro')}</p>
  <ul class="hint list">
    <li>{t('aiAdmin.paid')}</li>
    <li>{t('aiAdmin.local')}</li>
  </ul>
  <p class="hint">{t('aiAdmin.privacy')}</p>
  <div aria-live="polite"><Alert message={err} /></div>
  {#if cfg}
    <form class="stack" onsubmit={save}>
      {#if cfg['ai.enabled']?.locked}
        <p class="hint">{t('config.locked', { env: cfg['ai.enabled'].env })}: {cfg['ai.enabled'].value === 'true' ? t('aiAdmin.on') : t('aiAdmin.off')}</p>
      {:else}
        <label class="check"><input type="checkbox" bind:checked={enabled} />{t('aiAdmin.enable')}</label>
      {/if}
      <ConfigField field={cfg['ai.url']} label={t('aiAdmin.url')} hint={t('aiAdmin.urlHint')} bind:value={url} placeholder="https://api.openai.com/v1" />
      <ConfigField field={cfg['ai.model']} label={t('aiAdmin.model')} hint={t('aiAdmin.modelHint')} bind:value={model} placeholder="gpt-4o-mini" />
      <ConfigField field={cfg['ai.key']} label={t('aiAdmin.key')} hint={t('aiAdmin.keyHint')} bind:value={key} secret onremove={removeKey} />
      <p class="row">
        <Button type="submit" disabled={busy}>{t('common.save')}</Button>
        <Button variant="quiet" disabled={!ready || testing} onclick={test}>{testing ? t('aiAdmin.testing') : t('aiAdmin.test')}</Button>
      </p>
      {#if result}
        <p role="status" class={result.ok ? 'ok' : 'error-text'}>
          {result.ok ? t('aiAdmin.testOk', { ms: result.ms }) : t(FAIL[result.error ?? ''] ?? 'aiAdmin.fail.server')}
        </p>
      {/if}
      <p class="hint">{t('aiAdmin.limitHint')}</p>
    </form>
  {/if}
</section>

<style>
  .list {
    margin: 0;
    padding-inline-start: 1.2em;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
</style>
