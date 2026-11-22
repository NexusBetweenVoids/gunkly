<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import ReminderEditor from '../components/ReminderEditor.svelte';
  import RuleBuilder from '../components/RuleBuilder.svelte';
  import Select from '../components/Select.svelte';
  import { api } from '../lib/api';
  import { today } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { t, type Key } from '../lib/i18n';
  import { nameOf } from '../lib/names';
  import { DEFAULT_MINUTE, derive, hm, PRESET_REMINDERS, toMinute } from '../lib/reminders';
  import { navigate } from '../lib/router.svelte';
  import { fromRRule, newRule, toRRule } from '../lib/rrule';
  import { app, currentHousehold } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { Area, Chore, Member, Reminder, Template } from '../lib/types';

  let { id }: { id?: string } = $props();

  type Unit = 'day' | 'week' | 'month';
  const PRESETS: Record<string, [number, Unit]> = {
    daily: [1, 'day'], weekly: [1, 'week'], '2weeks': [2, 'week'], monthly: [1, 'month'],
    '3months': [3, 'month'], '6months': [6, 'month'], yearly: [12, 'month'],
  };
  const PRESET_LABELS: Record<string, Key> = {
    none: 'repeat.none', daily: 'repeat.daily', weekly: 'repeat.weekly', '2weeks': 'repeat.2weeks',
    monthly: 'repeat.monthly', '3months': 'repeat.3months', '6months': 'repeat.6months', yearly: 'repeat.yearly',
    interval: 'repeat.interval', rule: 'repeat.rule',
  };
  const hh = app.hhId;
  const LAST_KEY = `gunkly.lastAssignees.${hh}`;
  const base = `/households/${hh}`;

  // With areas off the editor has no area field and never sends areaId, so a chore keeps its area.
  const areasOn = !!currentHousehold()?.areasEnabled;
  let areas = $state<Area[]>([]);
  let members = $state<Member[]>([]);
  let templates = $state<Template[]>([]);
  let loaded = $state(false);
  let name = $state('');
  let areaId = $state('');
  let newArea = $state('');
  let assignees = $state<string[]>([]);
  let firstDue = $state(today());
  let repeat = $state('weekly');
  let n = $state(1);
  let unit = $state<Unit>('week');
  let rule = $state(newRule());
  let anchor = $state<'from_completion' | 'from_scheduled'>('from_completion');
  let reminder = $state('');
  let customRem = $state<Reminder[]>([]);
  // The simple "Remind me at" time: sent only when the user changed it or it was read from stored rows.
  let remTime = $state(hm(DEFAULT_MINUTE));
  let remTouched = $state(false);
  let remStored = $state(false);
  let notes = $state('');
  let link = $state('');
  let more = $state(false);
  let busy = $state(false);
  let err = $state('');
  let linkErr = $state('');

  const validLink = (s: string) => {
    if (!s) return true;
    try {
      const u = new URL(s);
      return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.host;
    } catch {
      return false;
    }
  };

  // Per-viewer convenience: the assignees of the last chore created here; storage may be unavailable.
  function rememberedAssignees(): string[] {
    try {
      const v: unknown = JSON.parse(localStorage.getItem(LAST_KEY) ?? '[]');
      return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
    } catch {
      return [];
    }
  }

  function setRepeat(v: string): void {
    repeat = v;
    anchor = v === 'rule' ? 'from_scheduled' : 'from_completion';
  }

  function applyChore(c: Chore): void {
    name = c.name;
    areaId = c.areaId ?? '';
    assignees = c.assignees;
    firstDue = c.schedule.firstDue;
    notes = c.notes;
    link = c.link;
    reminder = c.reminderPreset;
    anchor = c.schedule.anchor ?? 'from_completion';
    if (c.kind === 'oneoff') repeat = 'none';
    else if (c.schedule.type === 'rrule') {
      repeat = 'rule';
      rule = fromRRule(c.schedule.rrule ?? '');
    } else {
      const iv = c.schedule.interval!;
      [n, unit] = [iv.every, iv.unit];
      repeat = Object.keys(PRESETS).find((k) => PRESETS[k][0] === iv.every && PRESETS[k][1] === iv.unit) ?? 'interval';
    }
    more = repeat === 'rule' || repeat === 'interval' || !!notes || !!link ||
      anchor !== (repeat === 'rule' ? 'from_scheduled' : 'from_completion');
  }

  async function load(): Promise<void> {
    try {
      const [a, m, tp, c] = await Promise.all([
        areasOn ? api<Area[]>('GET', `${base}/areas`) : Promise.resolve([] as Area[]),
        api<Member[]>('GET', `${base}/members`),
        id ? Promise.resolve([] as Template[]) : api<Template[]>('GET', '/templates'),
        id ? api<Chore>('GET', `${base}/chores/${id}`) : Promise.resolve(null),
      ]);
      [areas, members, templates] = [a, m.filter((x) => x.role !== 'guest'), tp];
      if (!c) assignees = rememberedAssignees().filter((u) => members.some((x) => x.userId === u));
      if (c) {
        applyChore(c);
        if (c.reminderPreset === 'custom') {
          customRem = (await api<{ reminders: Reminder[] }>('GET', `${base}/chores/${id}/reminders`)).reminders;
          const d = derive(customRem);
          remTime = d.minute === null ? '' : hm(d.minute);
          remStored = d.minute !== null;
          if (d.preset === 'custom') more = true;
          else reminder = d.preset;
        }
      }
    } catch (x) {
      err = failure(x).message;
    }
    loaded = true;
  }
  void load();

  function useTemplate(key: string): void {
    const tp = templates.find((x) => x.key === key);
    if (!tp) return;
    name = nameOf('template', tp.key);
    if (areasOn) {
      const an = nameOf('area', tp.area);
      const hit = areas.find((a) => a.name.toLowerCase() === an.toLowerCase());
      [areaId, newArea] = hit ? [hit.id, ''] : ['__new', an];
    }
    const p = Object.keys(PRESETS).find((k) => PRESETS[k][0] === tp.interval.every && PRESETS[k][1] === tp.interval.unit);
    if (p) setRepeat(p);
    else {
      [n, unit] = [tp.interval.every, tp.interval.unit];
      setRepeat('interval');
      more = true;
    }
  }

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = linkErr = '';
    if (!validLink(link.trim())) {
      linkErr = t('chore.linkInvalid');
      more = true;
      return;
    }
    busy = true;
    try {
      let area: string | null = areaId || null;
      if (areasOn && areaId === '__new') area = newArea.trim() ? (await api<Area>('POST', `${base}/areas`, { name: newArea.trim() })).id : null;
      const recurring = repeat !== 'none';
      const [every, u] = PRESETS[repeat] ?? [n, unit];
      const custom = reminder === 'custom';
      const sendTime = !custom && showTime && !!remTime && (remTouched || remStored);
      const body = {
        name: name.trim(), notes, link: link.trim(), ...(areasOn ? { areaId: area } : {}), assignees,
        kind: recurring ? 'recurring' : 'oneoff',
        schedule: repeat === 'rule'
          ? { type: 'rrule', rrule: toRRule(rule), anchor, firstDue }
          : { type: 'interval', interval: { every, unit: u }, anchor, firstDue },
        reminderPreset: custom ? (id ? null : '') : reminder,
        ...(sendTime ? { reminderTime: remTime } : {}),
      };
      const c = id ? await api<Chore>('PUT', `${base}/chores/${id}`, body) : await api<Chore>('POST', `${base}/chores`, body);
      if (custom) {
        const reminders = customRem.map((r) => ({ ...r, days: r.kind === 'on' ? 0 : Math.max(1, r.days) }));
        await api('PUT', `${base}/chores/${c.id}/reminders`, { reminders });
      }
      if (!id) {
        try {
          localStorage.setItem(LAST_KEY, JSON.stringify(assignees));
        } catch {
          // storage unavailable: the next new chore starts as anyone
        }
      }
      toast(t('common.saved'));
      navigate(`/chores/${c.id}`);
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  const effective = $derived(reminder || currentHousehold()?.defaultReminderPreset || '');
  const showTime = $derived(reminder === 'custom' ? customRem.length > 0 : effective === 'gentle' || effective === 'nag');
  const mixed = $derived(reminder === 'custom' && new Set(customRem.map((r) => r.minute)).size > 1);

  function setRemTime(v: string): void {
    if (!v) return;
    remTime = v;
    remTouched = true;
    if (reminder === 'custom') customRem = customRem.map((r) => ({ ...r, minute: toMinute(v) }));
  }

  function setReminder(v: string): void {
    const from = effective;
    reminder = v;
    if (v !== 'custom') return;
    const chosen = remTouched || remStored;
    customRem = (PRESET_REMINDERS[from] ?? []).map((r) => ({ ...r, minute: chosen ? toMinute(remTime) : r.minute }));
  }

  const toggle = (uid: string) => (assignees = assignees.includes(uid) ? assignees.filter((x) => x !== uid) : [...assignees, uid]);
  const repeatKeys = $derived(Object.keys(PRESET_LABELS).filter((k) => more || (k !== 'interval' && k !== 'rule') || repeat === k));
</script>

<section class="card narrow">
  <h1>{id ? t('chore.edit') : t('chore.add')}</h1>
  {#if !loaded}
    <p role="status" class="muted">{t('common.loading')}</p>
  {:else}
    <form class="stack" onsubmit={submit}>
      {#if templates.length}
        <Select
          label={t('chore.template')}
          options={[{ value: '', label: t('chore.templateNone') }, ...templates.map((x) => ({ value: x.key, label: nameOf('template', x.key) }))]}
          bind:value={() => '', useTemplate}
        />
      {/if}
      <Field label={t('chore.name')} bind:value={name} required maxlength={200} />
      {#if areasOn}
      <Select
        label={t('chore.area')}
        bind:value={areaId}
        options={[
          { value: '', label: t('chore.noArea') },
          ...areas.map((a) => ({ value: a.id, label: a.name })),
          { value: '__new', label: t('chore.newArea') },
        ]}
      />
      {#if areaId === '__new'}<Field label={t('chore.newAreaName')} bind:value={newArea} maxlength={100} />{/if}
      {/if}

      <fieldset class="stack-sm">
        <legend class="label">{t('chore.assignees')}</legend>
        {#each members as m (m.userId)}
          <label class="check"><input type="checkbox" checked={assignees.includes(m.userId)} onchange={() => toggle(m.userId)} />{m.displayName}</label>
        {/each}
        <p class="hint">{assignees.length ? t('chore.assigneesSome') : t('chore.anyone')}</p>
      </fieldset>

      <div class="row">
        <Field label={t('chore.firstDue')} type="date" bind:value={firstDue} required />
        {#if showTime && mixed}
          <div class="field">
            <span class="label">{t('chore.remindAt')}</span>
            <p class="hint">{t('chore.remindAtMixed')}</p>
          </div>
        {:else if showTime}
          <Field label={t('chore.remindAt')} type="time" bind:value={() => remTime, setRemTime} hint={t('chore.remindAtHint')} />
        {/if}
      </div>
      <Select
        label={t('chore.repeat')}
        bind:value={() => repeat, setRepeat}
        options={repeatKeys.map((k) => ({ value: k, label: t(PRESET_LABELS[k]) }))}
      />
      <Select
        label={t('chore.reminders')}
        bind:value={() => reminder, setReminder}
        options={[
          { value: '', label: t('reminder.default') },
          { value: 'gentle', label: t('reminder.gentle') },
          { value: 'nag', label: t('reminder.nag') },
          { value: 'none', label: t('reminder.none') },
          ...(more || reminder === 'custom' ? [{ value: 'custom', label: t('reminder.customOption') }] : []),
        ]}
        hint={t('reminder.presetHint')}
      />

      <button type="button" class="btn btn-quiet" aria-expanded={more} onclick={() => (more = !more)}>{t('chore.more')}</button>
      {#if more}
        <div class="stack">
          {#if repeat === 'interval'}
            <div class="row">
              <Field label={t('chore.every')} type="number" min="1" max="1000" bind:value={() => String(n), (v) => (n = Number(v) || 1)} />
              <Select
                label={t('chore.unit')}
                bind:value={() => unit, (v) => (unit = v as Unit)}
                options={[{ value: 'day', label: t('unit.day') }, { value: 'week', label: t('unit.week') }, { value: 'month', label: t('unit.month') }]}
              />
            </div>
          {:else if repeat === 'rule'}
            <RuleBuilder bind:rule />
          {/if}
          {#if repeat !== 'none'}
            <Select
              label={t('chore.anchor')}
              bind:value={() => anchor, (v) => (anchor = v as typeof anchor)}
              hint={t('chore.anchorHint')}
              options={[{ value: 'from_completion', label: t('chore.anchorCompletion') }, { value: 'from_scheduled', label: t('chore.anchorScheduled') }]}
            />
          {/if}
          {#if reminder === 'custom'}<ReminderEditor bind:items={customRem} />{/if}
          <Field label={t('chore.notes')} hint={t('chore.notesHint')}>
            {#snippet children({ id: fid, describedby })}
              <textarea id={fid} aria-describedby={describedby} bind:value={notes} rows="3" maxlength="10000"></textarea>
            {/snippet}
          </Field>
          <Field label={t('chore.link')} type="url" bind:value={link} placeholder="https://" hint={t('chore.linkHint')} error={linkErr} maxlength={2000} />
        </div>
      {/if}

      <div aria-live="polite"><Alert message={err} /></div>
      <div class="row">
        <Button type="submit" {busy}>{t('common.save')}</Button>
        <a class="btn btn-quiet" href={id ? `/chores/${id}` : '/'}>{t('common.cancel')}</a>
      </div>
    </form>
  {/if}
</section>
