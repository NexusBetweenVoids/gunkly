<script lang="ts">
  import { href } from '../lib/base';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Confirm from '../components/Confirm.svelte';
  import DateDialog from '../components/DateDialog.svelte';
  import Icon from '../components/Icon.svelte';
  import Initials from '../components/Initials.svelte';
  import Select from '../components/Select.svelte';
  import { api } from '../lib/api';
  import { clock, dueText, fmt, fmtTime } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { formatDate, t, type Key } from '../lib/i18n';
  import { navigate } from '../lib/router.svelte';
  import { describeRRule } from '../lib/rrule';
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
  let changingTurn = $state(false);
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
  function setTurn(uid: string): void {
    changingTurn = false;
    if (!uid || uid === c?.turnUserId) return;
    void act(async () => {
      const r = await api<Chore>('PATCH', `${base}/chores/${id}`, { turnUserId: uid });
      toast(t('chore.turnSet', { name: who(uid) }));
      return r;
    });
  }
  const mute = () =>
    act(() => api<void>(c!.muted ? 'DELETE' : 'PUT', `${base}/chores/${id}/mute`).then(() => ({ ...c!, muted: !c!.muted })), c!.muted ? 'chore.muteOff' : 'chore.muteOn');
  const remText = (r: Reminder) =>
    t(r.kind === 'before' ? 'reminder.t.before' : r.kind === 'on' ? 'reminder.t.on' : 'reminder.t.overdue', { count: r.days, time: clock(r.minute) });
  const repeatText = (x: Chore) =>
    x.kind === 'oneoff' ? t('repeat.none') : x.schedule.type === 'rrule' ? describeRRule(x.schedule.rrule ?? '') : t('repeat.every', { count: x.schedule.interval!.every, unit: t(UNIT[x.schedule.interval!.unit], { count: x.schedule.interval!.every }) });
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
      {#if c.rotation && c.turnUserId}
        <dt>{t('chore.turn')}</dt>
        <dd>
          {who(c.turnUserId)}
          {#if canEdit && !c.archivedAt}
            <Button variant="quiet" {busy} aria-expanded={changingTurn} onclick={() => (changingTurn = !changingTurn)}>{t('chore.turnChange')}</Button>
            {#if changingTurn}
              <Select label={t('chore.turnWhose')} bind:value={() => c!.turnUserId ?? '', setTurn} options={c.assignees.map((u) => ({ value: u, label: who(u) }))} />
            {/if}
          {/if}
        </dd>
      {/if}
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
      <h2 id="c-act" class="visually-hidden">{t('chore.actions')}</h2>
      {#snippet remove()}
        <Button variant="quiet" class="btn-icon remove" aria-label={t('common.delete')} title={t('common.delete')} onclick={() => (dlg = 'delete')}><Icon name="trash" /><span class="cap" aria-hidden="true">{t('common.delete')}</span></Button>
      {/snippet}
      <div class="actions">
        {#if !c.archivedAt}
          {#if !paused}<Button onclick={done} {busy}>{t('common.done')}</Button>{/if}
          <div class="icons">
            <Button variant="quiet" class="btn-icon" aria-label={t('chore.snooze')} title={t('chore.snooze')} onclick={() => (dlg = 'snooze')}><Icon name="snooze" /><span class="cap" aria-hidden="true">{t('chore.snooze')}</span></Button>
            {#if c.kind === 'recurring'}<Button variant="quiet" class="btn-icon" aria-label={t('chore.skip')} title={t('chore.skip')} {busy} onclick={() => act(post('skip'), 'chore.skipped')}><Icon name="skip" /><span class="cap" aria-hidden="true">{t('chore.short.skip')}</span></Button>{/if}
            {#if paused}
              <Button class="btn-icon" aria-label={t('chore.resume')} title={t('chore.resume')} {busy} onclick={() => act(post('resume'), 'chore.resumed')}><Icon name="resume" /><span class="cap" aria-hidden="true">{t('chore.resume')}</span></Button>
            {:else}
              <Button variant="quiet" class="btn-icon" aria-label={t('chore.pause')} title={t('chore.pause')} onclick={() => (dlg = 'pause')}><Icon name="pause" /><span class="cap" aria-hidden="true">{t('chore.pause')}</span></Button>
            {/if}
            <Button variant="quiet" class="btn-icon" aria-label={c.muted ? t('chore.unmute') : t('chore.mute')} title={c.muted ? t('chore.unmute') : t('chore.mute')} {busy} onclick={mute}><Icon name={c.muted ? 'bell' : 'bellOff'} /><span class="cap" aria-hidden="true">{c.muted ? t('chore.short.unmute') : t('chore.short.mute')}</span></Button>
            <a class="btn btn-quiet btn-icon" href={href(`/chores/${id}/edit`)} aria-label={t('chore.edit')} title={t('chore.edit')}><Icon name="edit" /><span class="cap" aria-hidden="true">{t('chore.short.edit')}</span></a>
            <Button variant="quiet" class="btn-icon" aria-label={t('chore.archive')} title={t('chore.archive')} {busy} onclick={() => act(() => api<void>('POST', `${base}/chores/${id}/archive`).then(load), 'chore.archivedMsg')}><Icon name="archive" /><span class="cap" aria-hidden="true">{t('chore.archive')}</span></Button>
            {@render remove()}
          </div>
        {:else}
          <Button {busy} onclick={() => act(post('unarchive'), 'chore.unarchivedMsg')}>{t('chore.unarchive')}</Button>
          <div class="icons">{@render remove()}</div>
        {/if}
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
              {fmtTime(h.completedAt)} &middot; {who(h.userId)}{#if h.source === 'import'} &middot; {t('chore.imported')}{:else if h.source === 'oauth'} &middot; {h.via || t('chore.app')}{/if}{#if h.note}<br /><span class="muted">{h.note}</span>{/if}
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
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  /* Icons stay together; on narrow screens they drop below Done as one row. */
  .icons {
    display: flex;
    flex: 1 1 auto;
    flex-wrap: wrap;
  }
  .icons :global(.btn-icon) {
    min-inline-size: 40px;
    inline-size: 40px;
  }
  .icons :global(.btn-quiet) {
    background: transparent;
    color: var(--text-2);
  }
  @media (hover: hover) {
    .icons :global(.btn-quiet:hover) {
      opacity: 1;
      background: var(--surface-2);
      color: var(--text);
    }
  }
  /* Touch screens have no tooltips: show a short caption under each icon. */
  .cap {
    display: none;
  }
  @media (hover: none) {
    .icons {
      flex-wrap: nowrap;
      inline-size: 100%;
    }
    .icons :global(.btn-icon) {
      flex: 1 1 0;
      flex-direction: column;
      gap: 2px;
      min-inline-size: 0;
      inline-size: auto;
      min-block-size: 52px;
      padding-block: 6px;
    }
    .cap {
      display: block;
      font-size: 0.6875rem;
      font-weight: 500;
      line-height: 1.1;
      white-space: nowrap;
    }
    .icons :global(.btn-icon.remove) {
      margin-inline-start: 0;
    }
  }
  /* Narrow phones: captions need more room than one row gives, so use two rows of four. */
  @media (hover: none) and (max-width: 28rem) {
    .icons {
      display: grid;
      grid-template-columns: repeat(4, 1fr);
      gap: 4px;
    }
    .icons :global(.btn-icon.remove) {
      grid-column-end: -1;
    }
  }
  .icons :global(.remove) {
    margin-inline-start: auto;
    color: var(--danger);
  }
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
