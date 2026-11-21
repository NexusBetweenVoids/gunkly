<script lang="ts">
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { toast } from '../lib/toast.svelte';
  import type { Channel } from '../lib/types';
  import Alert from './Alert.svelte';
  import Button from './Button.svelte';
  import Confirm from './Confirm.svelte';
  import Dialog from './Dialog.svelte';
  import Field from './Field.svelte';
  import Select from './Select.svelte';

  // `base` is /account/channels or /households/{id}/channels.
  let { base, title, id: sid }: { base: `/${string}`; title: string; id: string } = $props();
  let items = $state<Channel[]>([]);
  let err = $state('');
  let result = $state('');
  let busy = $state(false);
  let editing = $state<Channel | 'new' | null>(null);
  let label = $state('');
  let url = $state('');
  let deleting = $state<Channel | null>(null);
  let kinds = $state<string[] | null>(null); // null until loaded
  let kind = $state('');

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
    err = '';
    if (c === 'new' && kinds === null) {
      api<{ enabledNotificationKinds: string[] }>('GET', '/notification-kinds')
        .then((r) => (kinds = r.enabledNotificationKinds))
        .catch(() => {}); // the free URL field still works
    }
  }

  /** Picking a service sets the URL scheme and keeps whatever follows it. */
  function pick(): void {
    if (kind) url = `${kind.replace('-selfhosted', '')}://${url.replace(/^[a-z0-9+.-]+:\/\//i, '')}`;
  }

  const fail = (x: unknown) => failure(x).message;

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      if (editing === 'new') await api('POST', base, { url: url.trim(), label: label.trim() });
      else await api('PATCH', `${base}/${editing!.id}`, { label: label.trim(), ...(url.trim() ? { url: url.trim() } : {}) });
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

<section class="card" aria-labelledby="ch-{sid}">
  <h2 id="ch-{sid}">{title}</h2>
  <p class="hint">{t('channels.privacy')}</p>
  <div aria-live="polite">
    <Alert message={editing ? '' : err} />
    {#if result}<p role="status">{result}</p>{/if}
  </div>
  {#if items.length}
    <ul class="list">
      {#each items as c (c.id)}
        <li>
          <span><strong>{c.label || c.kind}</strong> <span class="badge">{c.kind}</span><br /><span class="muted mono">{c.hint}</span></span>
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
          options={[{ value: '', label: t('channels.serviceOther') }, ...kinds.map((k) => ({ value: k, label: k }))]}
        />
      {:else if kinds}
        <p class="hint">{t('channels.noServices')}</p>
      {/if}
    {/if}
    <Field
      label={editing === 'new' ? t('channels.url') : t('channels.newUrl')}
      hint={editing === 'new' ? t('channels.urlHint') : t('channels.urlKeep')}
      bind:value={url}
      required={editing === 'new'}
      autocomplete="off"
      autocapitalize="none"
      spellcheck="false"
      placeholder="ntfy://ntfy.sh/my-topic"
    />
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
