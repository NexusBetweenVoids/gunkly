<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Field from '../../components/Field.svelte';
  import { api, ApiError } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { fmtTime } from '../../lib/dates';
  import { failure } from '../../lib/errors';
  import { t, type Key } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  type Hook = {
    id: string;
    host: string;
    description: string;
    events: string[];
    active: boolean;
    autoDisabled: boolean;
    failures: number;
    lastSuccessAt?: string;
    secret?: string;
  };
  type Attempt = { id: string; type: string; status: string; attempts: number; httpStatus?: number; durationMs: number; error?: string; createdAt: string };

  const EVENTS: { id: string; label: Key }[] = [
    { id: 'chore.completed', label: 'webhook.event.completed' },
    { id: 'chore.completion_reverted', label: 'webhook.event.reverted' },
    { id: 'chore.overdue', label: 'webhook.event.overdue' },
    { id: 'chore.created', label: 'webhook.event.created' },
    { id: 'chore.updated', label: 'webhook.event.updated' },
    { id: 'chore.deleted', label: 'webhook.event.deleted' },
    { id: 'chore.snoozed', label: 'webhook.event.snoozed' },
    { id: 'chore.skipped', label: 'webhook.event.skipped' },
  ];
  const eventLabel = (id: string): string => t(EVENTS.find((e) => e.id === id)?.label ?? 'webhook.event.ping');
  const STATUS: Record<string, Key> = { pending: 'delivery.pending', sent: 'delivery.sent', failed: 'delivery.failed' };
  const ICON: Record<string, string> = { pending: '⏳', sent: '✓', failed: '✗' };
  // Code, not prose: verifies a delivery. The full version is in docs/webhooks.md.
  const SNIPPET = `ts=\${header#t=}; ts=\${ts%%,*}
printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$secret"`;

  let { id }: { id: string } = $props();
  const base = $derived(`/households/${id}/webhooks`);
  let hooks = $state<Hook[]>([]);
  let loaded = $state(false);
  let err = $state('');
  let url = $state('');
  let description = $state('');
  let picked = $state<string[]>(EVENTS.map((e) => e.id));
  let busy = $state(false);
  let formErr = $state('');
  let secret = $state<{ host: string; value: string } | null>(null);
  let logOf = $state('');
  let log = $state<Attempt[]>([]);
  let deleting = $state<Hook | null>(null);
  let rotating = $state<Hook | null>(null);

  async function load(): Promise<void> {
    try {
      hooks = await api<Hook[]>('GET', base);
      err = '';
    } catch (x) {
      err = failure(x).message;
    }
    loaded = true;
  }
  function opened(e: Event): void {
    if ((e.currentTarget as HTMLDetailsElement).open && !loaded) void load();
  }

  const problem = (x: unknown): string =>
    x instanceof ApiError && x.code === 'invalid_webhook_url' ? t('webhook.invalidUrl') : failure(x).message;

  async function add(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    formErr = '';
    if (!picked.length) {
      formErr = t('webhook.needEvent');
      return;
    }
    busy = true;
    try {
      const made = await withStepUp(() => api<Hook>('POST', base, { url: url.trim(), description: description.trim(), events: picked }));
      secret = { host: made.host, value: made.secret ?? '' };
      url = description = '';
      await load();
    } catch (x) {
      formErr = problem(x);
    } finally {
      busy = false;
    }
  }

  async function run(fn: () => Promise<unknown>): Promise<void> {
    err = '';
    try {
      await fn();
      await load();
      if (logOf) await showLog(logOf);
    } catch (x) {
      err = problem(x);
    }
  }
  const toggle = (h: Hook) => run(() => withStepUp(() => api('PATCH', `${base}/${h.id}`, { active: !h.active })));
  const test = (h: Hook) =>
    run(async () => {
      const r = await api<Attempt>('POST', `${base}/${h.id}/test`);
      toast(r.status === 'sent' ? t('webhook.testSent', { code: r.httpStatus ?? 0 }) : t('webhook.testFailed', { reason: r.error ?? '' }));
      logOf = h.id;
    });
  async function rotate(h: Hook): Promise<void> {
    rotating = null;
    err = '';
    try {
      const r = await withStepUp(() => api<Hook>('POST', `${base}/${h.id}/rotate-secret`));
      secret = { host: h.host, value: r.secret ?? '' };
    } catch (x) {
      err = failure(x).message;
    }
  }
  async function remove(h: Hook): Promise<void> {
    deleting = null;
    if (logOf === h.id) logOf = '';
    await run(() => api('DELETE', `${base}/${h.id}`));
  }
  async function showLog(hid: string): Promise<void> {
    logOf = hid;
    try {
      log = (await api<{ items: Attempt[] }>('GET', `${base}/${hid}/deliveries`)).items;
    } catch (x) {
      err = failure(x).message;
    }
  }
  const pick = (ev: string, on: boolean) => (picked = on ? [...picked, ev] : picked.filter((p) => p !== ev));
</script>

<details class="card advanced" ontoggle={opened}>
  <summary>{t('webhook.title')}</summary>
  <p class="hint">{t('webhook.intro')}</p>
  <div aria-live="polite"><Alert message={err} /></div>

  {#if secret}
    <div class="stack-sm" role="status">
      <p class="label">{t('webhook.secretFor', { host: secret.host })}</p>
      <p class="mono">{secret.value}</p>
      <p class="hint">{t('webhook.secretOnce')}</p>
      <div><Button variant="quiet" onclick={() => copyText(secret!.value)}>{t('common.copy')}</Button></div>
      <details>
        <summary>{t('webhook.verify')}</summary>
        <p class="hint">{t('webhook.verifyHint')}</p>
        <pre class="mono">{SNIPPET}</pre>
      </details>
      <div><Button variant="quiet" onclick={() => (secret = null)}>{t('webhook.secretDone')}</Button></div>
    </div>
  {/if}

  {#if !loaded}
    <p role="status" class="muted">{t('common.loading')}</p>
  {:else if hooks.length}
    <ul class="list">
      {#each hooks as h (h.id)}
        <li>
          <span>
            <strong>{h.description || h.host}</strong>
            {#if !h.active}<span class="badge">{t('webhook.off')}</span>{/if}
            <br /><span class="muted">{h.host} &middot; {h.events.map(eventLabel).join(', ')}</span>
            {#if h.lastSuccessAt}<br /><span class="muted">{t('webhook.lastOk', { date: fmtTime(h.lastSuccessAt) })}</span>{/if}
            {#if h.autoDisabled && !h.active}<br /><span class="error-text">{t('webhook.autoOff')}</span>{/if}
          </span>
          <span class="row">
            <Button variant="quiet" disabled={!h.active} onclick={() => test(h)}>{t('webhook.test')}</Button>
            <Button variant="quiet" onclick={() => (logOf === h.id ? (logOf = '') : showLog(h.id))} aria-expanded={logOf === h.id}>{t('webhook.log')}</Button>
            <Button variant="quiet" onclick={() => toggle(h)}>{h.active ? t('webhook.disable') : t('webhook.enable')}</Button>
            <Button variant="quiet" onclick={() => (rotating = h)}>{t('webhook.rotate')}</Button>
            <Button variant="danger" onclick={() => (deleting = h)}>{t('common.delete')}</Button>
          </span>
        </li>
        {#if logOf === h.id}
          <li>
            {#if log.length}
              <ul class="list">
                {#each log as a (a.id)}
                  <li>
                    <span>
                      <strong>{a.type === 'ping' ? t('webhook.event.ping') : eventLabel(a.type)}</strong>
                      <span class="muted"> &middot; {fmtTime(a.createdAt)}{a.httpStatus ? ` · HTTP ${a.httpStatus}` : ''} &middot; {a.durationMs} ms</span>
                      {#if a.error}<br /><span class="error-text">{a.error}</span>{/if}
                    </span>
                    <span class="badge"><span aria-hidden="true">{ICON[a.status] ?? ''}</span> {t(STATUS[a.status] ?? 'delivery.pending')}</span>
                  </li>
                {/each}
              </ul>
            {:else}
              <span class="muted">{t('webhook.logNone')}</span>
            {/if}
          </li>
        {/if}
      {/each}
    </ul>
  {:else}
    <p class="muted">{t('webhook.none')}</p>
  {/if}

  <h3>{t('webhook.add')}</h3>
  <form class="stack" onsubmit={add}>
    <Field label={t('webhook.url')} hint={t('webhook.urlHint')} type="url" bind:value={url} required maxlength={2048} autocomplete="off" />
    <Field label={t('webhook.description')} bind:value={description} maxlength={200} />
    <fieldset class="stack-sm">
      <legend class="label">{t('webhook.events')}</legend>
      {#each EVENTS as ev (ev.id)}
        <label class="check"><input type="checkbox" checked={picked.includes(ev.id)} onchange={(e) => pick(ev.id, e.currentTarget.checked)} />{t(ev.label)}</label>
      {/each}
    </fieldset>
    <Alert message={formErr} />
    <div><Button type="submit" {busy}>{t('webhook.create')}</Button></div>
  </form>
</details>

<Confirm
  open={!!rotating}
  title={t('webhook.rotateTitle')}
  message={t('webhook.rotateWarn')}
  confirmLabel={t('webhook.rotate')}
  onconfirm={() => rotating && rotate(rotating)}
  oncancel={() => (rotating = null)}
/>
<Confirm
  open={!!deleting}
  title={t('webhook.deleteTitle')}
  message={t('webhook.deleteWarn')}
  confirmLabel={t('common.delete')}
  onconfirm={() => deleting && remove(deleting)}
  oncancel={() => (deleting = null)}
/>
