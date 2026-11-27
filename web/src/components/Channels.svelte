<script lang="ts">
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { docsUrl, fromLink, SERVICES, serviceName } from '../lib/services';
  import { toast } from '../lib/toast.svelte';
  import type { Channel } from '../lib/types';
  import Alert from './Alert.svelte';
  import Button from './Button.svelte';
  import Confirm from './Confirm.svelte';
  import Dialog from './Dialog.svelte';
  import Field from './Field.svelte';
  import Select from './Select.svelte';

  // `base` is /account/channels or /households/{id}/channels.
  let {
    base,
    title,
    id: sid,
    intro = '',
    bare = false,
    items = $bindable([]),
  }: { base: `/${string}`; title: string; id: string; intro?: string; bare?: boolean; items?: Channel[] } = $props();
  let err = $state('');
  let result = $state('');
  let busy = $state(false);
  let editing = $state<Channel | 'new' | null>(null);
  let label = $state('');
  let url = $state('');
  let deleting = $state<Channel | null>(null);
  let kinds = $state<string[] | null>(null); // null until loaded
  let kind = $state('');
  // Guided entry: the service's own fields instead of a raw service URL.
  let values = $state<Record<string, string>>({});
  let rawUrl = $state(false);
  const svc = $derived(kind ? SERVICES[kind] : undefined);
  const guided = $derived(editing === 'new' && !!svc?.fields && !rawUrl);
  // Services with guided fields first, then the rest by name.
  const ordered = $derived(
    [...(kinds ?? [])].sort((a, b) => Number(!SERVICES[a]?.fields) - Number(!SERVICES[b]?.fields) || serviceName(a).localeCompare(serviceName(b))),
  );

  async function load(): Promise<void> {
    try {
      items = await api<Channel[]>('GET', base);
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  function open(c: Channel | 'new'): void {
    editing = c;
    label = c === 'new' ? '' : c.label;
    url = '';
    kind = '';
    values = {};
    rawUrl = false;
    err = '';
    if (c === 'new' && kinds === null) {
      api<{ enabledNotificationKinds: string[] }>('GET', '/notification-kinds')
        .then((r) => (kinds = r.enabledNotificationKinds))
        .catch(() => {}); // the free URL field still works
    }
  }

  /** Picking a service starts its fields afresh. */
  function pick(): void {
    values = Object.fromEntries((SERVICES[kind]?.fields ?? []).map((f) => [f.id, '']));
    rawUrl = false;
  }

  /** The service URL to send: built from the fields, or typed (a pasted webhook link is converted). */
  function target(): string | null {
    if (guided) return svc!.build!(values);
    const typed = url.trim();
    return typed ? (fromLink(typed) ?? typed) : '';
  }

  const fail = (x: unknown) => failure(x).message;

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = '';
    const to = target();
    if (to === null) return void (err = t('svc.invalid', { name: svc!.name }));
    busy = true;
    try {
      if (editing === 'new') await api('POST', base, { url: to, label: label.trim() || (svc ? svc.name : '') });
      else await api('PATCH', `${base}/${editing!.id}`, { label: label.trim(), ...(to ? { url: to } : {}) });
      editing = null;
      toast(t('common.saved'));
      await load();
    } catch (x) {
      err = fail(x);
    } finally {
      busy = false;
    }
  }

  async function test(c: Channel): Promise<void> {
    result = '';
    err = '';
    try {
      const r = await api<{ sent: boolean; error?: string }>('POST', `${base}/${c.id}/test`);
      result = r.sent ? t('channels.testOk', { label: c.label }) : t('channels.testFailed', { label: c.label, error: r.error ?? '' });
    } catch (x) {
      err = failure(x).message;
    }
  }

  async function remove(): Promise<void> {
    try {
      await api('DELETE', `${base}/${deleting!.id}`);
      deleting = null;
      await load();
    } catch (x) {
      err = failure(x).message;
      deleting = null;
    }
  }
</script>

<section class:card={!bare} aria-labelledby="ch-{sid}">
  <!-- Bare (inside a settings row that already names it): the heading only labels the region. -->
  <h2 id="ch-{sid}" class:visually-hidden={bare}>{title}</h2>
  {#if intro}<p class="muted">{intro}</p>{/if}
  <p class="hint">{t('channels.privacy')}</p>
  <div aria-live="polite">
    <Alert message={editing ? '' : err} />
    {#if result}<p role="status">{result}</p>{/if}
  </div>
  {#if items.length}
    <ul class="list">
      {#each items as c (c.id)}
        <li>
          <span><strong>{c.label || serviceName(c.kind)}</strong> <span class="badge">{serviceName(c.kind)}</span><br /><span class="muted mono">{c.hint}</span></span>
          <span class="row">
            <Button variant="quiet" aria-label={t('channels.testFor', { label: c.label || c.kind })} onclick={() => test(c)}>{t('channels.test')}</Button>
            <Button variant="quiet" aria-label={t('channels.editFor', { label: c.label || c.kind })} onclick={() => open(c)}>{t('channels.edit')}</Button>
            <Button variant="danger" aria-label={t('channels.deleteFor', { label: c.label || c.kind })} onclick={() => (deleting = c)}>{t('common.delete')}</Button>
          </span>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted">{t('channels.none')}</p>
  {/if}
  <div class="mt-md"><Button onclick={() => open('new')}>{t('channels.add')}</Button></div>
</section>

<Dialog open={!!editing} title={editing === 'new' ? t('channels.add') : t('channels.edit')} onclose={() => (editing = null)}>
  <form class="stack" onsubmit={save}>
    <Field label={t('channels.label')} bind:value={label} maxlength={100} />
    {#if editing === 'new'}
      {#if kinds?.length}
        <Select
          label={t('channels.service')}
          bind:value={kind}
          onchange={pick}
          options={[{ value: '', label: t('channels.serviceOther') }, ...ordered.map((k) => ({ value: k, label: serviceName(k) }))]}
        />
      {:else if kinds}
        <p class="hint">{t('channels.noServices')}</p>
      {/if}
    {/if}
    {#if editing === 'new' && svc}
      <div class="howto" data-testid="service-help">
        {#if svc.steps}
          <ol class="steps stack-sm">
            {#each svc.steps as step (step)}<li>{t(step)}</li>{/each}
          </ol>
        {/if}
        <p class="hint">
          <a href={docsUrl(svc)} target="_blank" rel="noopener noreferrer">{t('svc.guide', { name: svc.name })}</a>
        </p>
      </div>
    {/if}
    {#if guided}
      {#each svc!.fields! as f (f.id)}
        <Field
          label={t(f.label)}
          hint={f.hint ? t(f.hint) : undefined}
          bind:value={values[f.id]}
          required
          autocomplete="off"
          autocapitalize="none"
          spellcheck="false"
          placeholder={f.placeholder}
        />
      {/each}
      <p class="hint"><button type="button" class="linkish" onclick={() => (rawUrl = true)}>{t('svc.useUrl')}</button></p>
    {:else}
      <Field
        label={editing === 'new' ? t('channels.url') : t('channels.newUrl')}
        hint={editing === 'new' ? t('channels.urlHint') : t('channels.urlKeep')}
        bind:value={url}
        required={editing === 'new'}
        autocomplete="off"
        autocapitalize="none"
        spellcheck="false"
        placeholder={svc?.format ?? 'ntfy://ntfy.sh/my-topic'}
      />
    {/if}
    <p class="hint">{t('channels.privacy')}</p>
    <Alert message={err} />
    <div class="row">
      <Button type="submit" {busy}>{t('common.save')}</Button>
      <Button variant="quiet" onclick={() => (editing = null)}>{t('common.cancel')}</Button>
    </div>
  </form>
</Dialog>
<Confirm
  open={!!deleting}
  title={t('channels.deleteTitle', { label: deleting?.label || deleting?.kind || '' })}
  message={t('channels.deleteWarn')}
  confirmLabel={t('common.delete')}
  onconfirm={remove}
  oncancel={() => (deleting = null)}
/>

<style>
  .howto {
    padding: 12px 14px;
    border-radius: 8px;
    background: var(--surface-2);
    border: 1px solid var(--border);
  }
  .howto .hint {
    margin-block-end: 0;
  }
  .linkish {
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-decoration: underline;
    cursor: pointer;
  }
</style>
