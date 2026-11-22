<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Confirm from '../components/Confirm.svelte';
  import DateDialog from '../components/DateDialog.svelte';
  import Initials from '../components/Initials.svelte';
  import { api } from '../lib/api';
  import { clock, dueText, fmt, fmtTime } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { formatDate, t, type Key } from '../lib/i18n';
  import { navigate } from '../lib/router.svelte';
  import { describe, fromRRule } from '../lib/rrule';
  import { app, currentHousehold } from '../lib/session.svelte';
  import { STATUS, UNIT } from '../lib/names';
  import { toast } from '../lib/toast.svelte';
  import type { Area, Chore, Completion, Member, Page, Reminder } from '../lib/types';

  let { id }: { id: string } = $props();
  const base = `/households/${app.hhId}`;
  const canEdit = currentHousehold()?.role !== 'guest';
  const PRESET: Record<string, Key> = { gentle: 'reminder.gentle', nag: 'reminder.nag', none: 'reminder.none', custom: 'reminder.customOption' };

  let c = $state<Chore | null>(null);
  const areasOn = !!currentHousehold()?.areasEnabled;
  let areas = $state<Area[]>([]);
  let members = $state<Member[]>([]);
  let rem = $state<{ effectivePreset: string; reminders: Reminder[] } | null>(null);
  let hist = $state<Completion[]>([]);
  let next = $state<string | null>(null);
  let err = $state('');
  let busy = $state(false);
  let dlg = $state<'' | 'snooze' | 'pause' | 'delete'>('');

  const who = (uid: string | null) => members.find((m) => m.userId === uid)?.displayName ?? t('chore.someone');
  const paused = $derived(!!c && (c.pausedIndefinitely || (!!c.pausedUntil && c.status === 'paused')));

  async function load(): Promise<void> {
    try {
      [c, areas, members] = await Promise.all([
        api<Chore>('GET', `${base}/chores/${id}`),
        areasOn ? api<Area[]>('GET', `${base}/areas`) : Promise.resolve([] as Area[]),
        api<Member[]>('GET', `${base}/members`),
      ]);
      rem = await api('GET', `${base}/chores/${id}/reminders`);
      hist = [];
      await more();
    } catch (x) {
      err = failure(x).message;
    }
  }
  async function more(first = true): Promise<void> {
    const p = await api<Page<Completion>>('GET', `${base}/chores/${id}/completions?limit=10${next && !first ? `&after=${encodeURIComponent(next)}` : ''}`);
    hist = first ? p.items : [...hist, ...p.items];
    next = p.next || null;
  }
  void load();

  /** Run an action; adopt the chore it returns (when any) and reload history. */
  async function act(fn: () => Promise<Chore | void>, msg?: Key): Promise<void> {
    busy = true;
    err = '';
    try {
      const r = await fn();
      if (r) c = r;
      dlg = '';
      if (msg) toast(t(msg));
      await Promise.all([more(), api<{ effectivePreset: string; reminders: Reminder[] }>('GET', `${base}/chores/${id}/reminders`).then((v) => (rem = v))]);
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
  const post = (p: string, body?: unknown) => () => api<Chore>('POST', `${base}/chores/${id}/${p}`, body);

  async function done(): Promise<void> {
    await act(async () => {
      const r = await api<{ completion: Completion; chore: Chore }>('POST', `${base}/chores/${id}/complete`);
      toast(t('home.completed', { name: r.chore.name }), {
        label: t('common.undo'),
        run: () => void act(post(`completions/${r.completion.id}/revert`), 'chore.undone'),
      });
      return r.chore;
    });
  }
  const mute = () =>
    act(() => api<void>(c!.muted ? 'DELETE' : 'PUT', `${base}/chores/${id}/mute`).then(() => ({ ...c!, muted: !c!.muted })), c!.muted ? 'chore.muteOff' : 'chore.muteOn');
  const remText = (r: Reminder) =>
    t(r.kind === 'before' ? 'reminder.t.before' : r.kind === 'on' ? 'reminder.t.on' : 'reminder.t.overdue', { count: r.days, time: clock(r.minute) });
  const repeatText = (x: Chore) =>
    x.kind === 'oneoff' ? t('repeat.none') : x.schedule.type === 'rrule' ? describe(fromRRule(x.schedule.rrule ?? '')) : t('repeat.every', { count: x.schedule.interval!.every, unit: t(UNIT[x.schedule.interval!.unit], { count: x.schedule.interval!.every }) });
</script>

{#if c}
  <h1>{c.name}</h1>
  <div aria-live="polite"><Alert message={err} /></div>
  <section class="card stack" aria-labelledby="c-info">
    <h2 id="c-info" class="visually-hidden">{t('chore.details')}</h2>
    <p>
      <span class="badge">{t(STATUS[c.status])}</span>
      {#if c.status !== 'paused' && c.status !== 'archived' && c.status !== 'none'}<strong class:late={c.status === 'overdue'}>{dueText(c.dueDate)}</strong> <span class="muted">({fmt(c.dueDate, { dateStyle: 'full' })})</span>{/if}
      {#if paused}<br />{c.pausedIndefinitely ? t('chore.pausedForever') : t('chore.pausedUntil', { date: formatDate(new Date(c.pausedUntil!), { dateStyle: 'medium' }) })}{/if}
    </p>
    <dl class="facts">
      {#if areasOn}<dt>{t('chore.area')}</dt><dd>{areas.find((a) => a.id === c!.areaId)?.name ?? t('chore.noArea')}</dd>{/if}
      <dt>{t('chore.repeat')}</dt><dd>{repeatText(c)}</dd>
      <dt>{t('chore.assignees')}</dt>
      <dd>{#if c.assignees.length}<Initials names={c.assignees.map(who)} />{:else}{t('chore.anyone')}{/if}</dd>
      {#if rem}
        <dt>{t('chore.reminders')}</dt>
        <dd>
          {rem.effectivePreset ? t(PRESET[rem.effectivePreset] ?? 'reminder.none') : t('reminder.none')}{c.muted ? ` (${t('chore.muted')})` : ''}
          {#if rem.reminders.length}<ul>{#each rem.reminders as r, i (i)}<li>{remText(r)}</li>{/each}</ul>{/if}
        </dd>
      {/if}
    </dl>
    {#if c.notes}<p class="notes">{c.notes}</p>{/if}
    {#if c.link}<p><a href={c.link} target="_blank" rel="noopener noreferrer">{c.link}</a></p>{/if}
  </section>

  {#if canEdit}
    <section class="card mt-md" aria-labelledby="c-act">
      <h2 id="c-act">{t('chore.actions')}</h2>
      <div class="row">
        {#if !c.archivedAt}
          {#if !paused}<Button onclick={done} {busy}>{t('common.done')}</Button>{/if}
          <Button variant="quiet" onclick={() => (dlg = 'snooze')}>{t('chore.snooze')}</Button>
          {#if c.kind === 'recurring'}<Button variant="quiet" {busy} onclick={() => act(post('skip'), 'chore.skipped')}>{t('chore.skip')}</Button>{/if}
          {#if paused}
            <Button variant="quiet" {busy} onclick={() => act(post('resume'), 'chore.resumed')}>{t('chore.resume')}</Button>
          {:else}
            <Button variant="quiet" onclick={() => (dlg = 'pause')}>{t('chore.pause')}</Button>
          {/if}
          <a class="btn btn-quiet" href="/chores/{id}/edit">{t('chore.edit')}</a>
          <Button variant="quiet" {busy} onclick={mute}>{c.muted ? t('chore.unmute') : t('chore.mute')}</Button>
          <Button variant="quiet" {busy} onclick={() => act(() => api<void>('POST', `${base}/chores/${id}/archive`).then(load), 'chore.archivedMsg')}>{t('chore.archive')}</Button>
        {:else}
          <Button {busy} onclick={() => act(post('unarchive'), 'chore.unarchivedMsg')}>{t('chore.unarchive')}</Button>
        {/if}
        <Button variant="danger" onclick={() => (dlg = 'delete')}>{t('common.delete')}</Button>
      </div>
    </section>
  {/if}

  <section class="card mt-md" aria-labelledby="c-hist">
    <h2 id="c-hist">{t('chore.history')}</h2>
    {#if hist.length}
      <ul class="list">
        {#each hist as h (h.id)}
          <li>
            <span class:struck={!!h.revertedAt}>
              {fmtTime(h.completedAt)} &middot; {who(h.userId)}{#if h.source === 'import'} &middot; {t('chore.imported')}{/if}{#if h.note}<br /><span class="muted">{h.note}</span>{/if}
            </span>
            {#if h.revertedAt}
              <span class="badge">{t('chore.reverted')}</span>
            {:else if canEdit}
              <Button variant="quiet" {busy} aria-label={t('chore.revertFor', { date: fmtTime(h.completedAt) })} onclick={() => act(post(`completions/${h.id}/revert`), 'chore.undone')}>{t('common.undo')}</Button>
            {/if}
          </li>
        {/each}
      </ul>
      {#if next}<Button variant="quiet" onclick={() => more(false).catch((x) => (err = failure(x).message))}>{t('common.more')}</Button>{/if}
    {:else}
      <p class="muted">{t('chore.noHistory')}</p>
    {/if}
  </section>

  <DateDialog
    open={dlg === 'snooze'}
    title={t('chore.snoozeTitle')}
    label={t('chore.snoozeUntil')}
    confirmLabel={t('chore.snooze')}
    {busy}
    onconfirm={(d) => act(post('snooze', { until: d }), 'chore.snoozed')}
    oncancel={() => (dlg = '')}
  />
  <DateDialog
    open={dlg === 'pause'}
    title={t('chore.pauseTitle')}
    label={t('chore.pauseUntil')}
    minOffset={1}
    confirmLabel={t('chore.pauseDate')}
    alt={{ label: t('chore.pauseForever'), run: () => void act(post('pause', {}), 'chore.paused2') }}
    {busy}
    onconfirm={(d) => act(post('pause', { until: d }), 'chore.paused2')}
    oncancel={() => (dlg = '')}
  />
  <Confirm
    open={dlg === 'delete'}
    title={t('chore.deleteTitle', { name: c.name })}
    message={t('chore.deleteWarn')}
    confirmLabel={t('common.delete')}
    {busy}
    onconfirm={() => act(() => api<void>('DELETE', `${base}/chores/${id}`).then(() => navigate('/')))}
    oncancel={() => (dlg = '')}
  />
{:else if err}
  <Alert message={err} />
{:else}
  <p role="status" class="muted">{t('common.loading')}</p>
{/if}

<style>
  .facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 6px 16px;
    margin: 0;
  }
  dt {
    font-weight: 600;
  }
  dd {
    margin: 0;
  }
  dd ul {
    margin-block: 4px 0;
    padding-inline-start: 20px;
  }
  .notes {
    white-space: pre-wrap;
    margin: 0;
  }
  .late {
    color: var(--danger);
  }
  .struck {
    text-decoration: line-through;
  }
</style>
