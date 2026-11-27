<script lang="ts">
  import { href } from '../lib/base';
  import AiSuggest, { type Suggestion } from '../components/AiSuggest.svelte';
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import ReminderEditor from '../components/ReminderEditor.svelte';
  import RuleBuilder from '../components/RuleBuilder.svelte';
  import Select from '../components/Select.svelte';
  import Sheet from '../components/Sheet.svelte';
  import { api } from '../lib/api';
  import { addDays, fmt, householdToday, initials, parseYmd, ymd } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { t, type Key } from '../lib/i18n';
  import { nameOf, UNIT } from '../lib/names';
  import { DEFAULT_MINUTE, derive, hm, PRESET_REMINDERS, toMinute } from '../lib/reminders';
  import { navigate } from '../lib/router.svelte';
  import { DAYS, describe, fromRRule, newRule, nthKey, representable, toRRule, type Rule } from '../lib/rrule';
  import { app, currentHousehold } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { Area, Chore, Member, Reminder, Template } from '../lib/types';

  let { id }: { id?: string } = $props();

  type Unit = 'day' | 'week' | 'month';
  // The wording of the common intervals ("Every week", "Every 3 months").
  const PRESET: Record<string, Key> = {
    '1day': 'repeat.daily', '1week': 'repeat.weekly', '2week': 'repeat.2weeks', '1month': 'repeat.monthly',
    '3month': 'repeat.3months', '6month': 'repeat.6months', '12month': 'repeat.yearly',
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
  let rotation = $state(false);
  let onlyTurn = $state(false); // advance only when the person whose turn it is completes
  let firstDue = $state(householdToday());
  let repeat = $state<'none' | 'interval' | 'rule'>('interval');
  let n = $state(1);
  let unit = $state<Unit>('week');
  let rule = $state(newRule());
  // A stored rule the builder cannot show exactly (set through the API, e.g. with BYSETPOS): kept as it is
  // unless the user picks another repeat or replaces it.
  let rawRule = $state('');
  // "On": pins a weekly or monthly interval to weekdays, a date, or e.g. the first Saturday. Saved as a
  // calendar rule; with nothing picked it stays a plain interval.
  let onDays = $state<string[]>([]);
  let monthOn = $state<'any' | 'date' | 'nth'>('any');
  let monthDay = $state(1);
  let nth = $state(1);
  let nthDay = $state('SA');
  let anchor = $state<'from_completion' | 'from_scheduled'>('from_completion');
  let reminder = $state('');
  let customRem = $state<Reminder[]>([]);
  // The simple "Remind me at" time: sent only when the user changed it or it was read from stored rows.
  let remTime = $state(hm(DEFAULT_MINUTE));
  let remTouched = $state(false);
  let remStored = $state(false);
  let notes = $state('');
  let link = $state('');
  // Which sheet is open, and whether the notes section is unfolded.
  let sheet = $state<'' | 'area' | 'repeat' | 'reminder' | 'templates' | 'ai'>('');
  let notesOpen = $state(false);
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

  // A calendar rule usually means fixed dates; an interval usually counts from completion.
  function setRepeat(v: typeof repeat): void {
    if (v !== 'rule') rawRule = '';
    if (v === repeat) return;
    repeat = v;
    anchor = v === 'rule' ? 'from_scheduled' : 'from_completion';
  }

  // The "How often" buttons: Once, or an interval of 1 day/week/month (the stepper changes the number).
  function chooseFreq(f: 'none' | Unit): void {
    if (f === 'none') return setRepeat('none');
    setRepeat('interval');
    if (unit !== f) [unit, n, onDays, monthOn] = [f, 1, [], 'any'];
  }

  // The interval as a calendar rule when "On" pins it, else null.
  const pinned = $derived.by((): Rule | null => {
    if (repeat !== 'interval') return null;
    const r = { ...newRule(), interval: n, months: [] };
    if (unit === 'week' && onDays.length) return { ...r, freq: 'WEEKLY', weekdays: onDays };
    if (unit === 'month' && monthOn === 'date') return { ...r, freq: 'MONTHLY', mode: 'day', day: monthDay };
    if (unit === 'month' && monthOn === 'nth') return { ...r, freq: 'MONTHLY', mode: 'nth', nth, weekday: nthDay };
    return null;
  });

  // Weekday code (MO..SU) and whether a date matches the "On" choice.
  const dow = (s: string) => DAYS[(parseYmd(s).getUTCDay() + 6) % 7];
  function matches(s: string, r: Rule): boolean {
    const d = parseYmd(s);
    const last = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 0)).getUTCDate();
    if (r.freq === 'WEEKLY') return r.weekdays.includes(dow(s));
    if (r.mode === 'day') return r.day === -1 ? d.getUTCDate() === last : d.getUTCDate() === r.day;
    if (dow(s) !== r.weekday) return false;
    return r.nth === -1 ? d.getUTCDate() + 7 > last : Math.ceil(d.getUTCDate() / 7) === r.nth;
  }
  // After changing "On", move the first due date to the first matching day from it (or from today).
  function alignFirstDue(): void {
    const r = pinned;
    if (!r) return;
    let s = firstDue && firstDue > today ? firstDue : today;
    for (let i = 0; i < 400 && !matches(s, r); i++) s = addDays(s, 1);
    firstDue = s;
  }
  function toggleDay(d: string): void {
    onDays = onDays.includes(d) ? onDays.filter((x) => x !== d) : [...onDays, d];
    alignFirstDue();
  }
  function setMonthOn(v: typeof monthOn): void {
    monthOn = v;
    if (v === 'date' && firstDue) monthDay = parseYmd(firstDue).getUTCDate();
    alignFirstDue();
  }

  function applyChore(c: Chore): void {
    name = c.name;
    areaId = c.areaId ?? '';
    assignees = c.assignees;
    rotation = c.rotation;
    onlyTurn = c.advanceOn === 'assignee';
    firstDue = c.schedule.firstDue;
    notes = c.notes;
    link = c.link;
    reminder = c.reminderPreset;
    anchor = c.schedule.anchor ?? 'from_completion';
    if (c.schedule.interval) [n, unit] = [c.schedule.interval.every, c.schedule.interval.unit];
    if (c.schedule.type === 'rrule') {
      rule = fromRRule(c.schedule.rrule ?? '');
      rawRule = representable(c.schedule.rrule ?? '') ? '' : (c.schedule.rrule ?? '');
    }
    repeat = c.kind === 'oneoff' ? 'none' : c.schedule.type === 'rrule' ? 'rule' : 'interval';
    if (repeat === 'rule') adoptSimpleRule(c.schedule.rrule ?? '');
    notesOpen = !!notes || !!link;
  }

  // A rule the "On" line can show (weekly on weekdays, monthly on a date or an nth weekday) opens as an
  // interval with "On" set; anything else stays a custom rule.
  function adoptSimpleRule(s: string): void {
    if (!representable(s)) return; // something the builder cannot represent
    if (rule.freq === 'WEEKLY' && rule.weekdays.length) [unit, onDays] = ['week', [...rule.weekdays]];
    else if (rule.freq === 'MONTHLY' && rule.mode === 'day') [unit, monthOn, monthDay] = ['month', 'date', rule.day];
    else if (rule.freq === 'MONTHLY' && rule.mode === 'nth') [unit, monthOn, nth, nthDay] = ['month', 'nth', rule.nth, rule.weekday];
    else return;
    [n, repeat] = [rule.interval, 'interval'];
  }

  // "Edit" on an AI suggestion: fill the form with it, then the person saves as usual.
  function applySuggestion(sg: Suggestion): void {
    applyChore({
      ...sg, link: '', advanceOn: 'any', reminderPreset: reminder,
      schedule: { ...sg.schedule, anchor: sg.schedule.type === 'rrule' ? 'from_scheduled' : 'from_completion' },
    } as unknown as Chore);
    if (sg.reminderTime) setRemTime(sg.reminderTime);
    sheet = '';
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
          if (d.preset !== 'custom') reminder = d.preset;
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
    setRepeat('interval');
    [n, unit] = [tp.interval.every, tp.interval.unit];
    sheet = '';
  }

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = linkErr = '';
    if (!validLink(link.trim())) {
      linkErr = t('chore.linkInvalid');
      notesOpen = true;
      return;
    }
    busy = true;
    try {
      let area: string | null = areaId || null;
      if (areasOn && areaId === '__new') area = newArea.trim() ? (await api<Area>('POST', `${base}/areas`, { name: newArea.trim() })).id : null;
      const recurring = repeat !== 'none';
      const custom = reminder === 'custom';
      const sendTime = !custom && showTime && !!remTime && (remTouched || remStored);
      const body = {
        name: name.trim(), notes, link: link.trim(), ...(areasOn ? { areaId: area } : {}), assignees,
        rotation: takesTurns, advanceOn: onlyTurn ? 'assignee' : 'any',
        kind: recurring ? 'recurring' : 'oneoff',
        schedule: repeat === 'rule' || pinned
          ? { type: 'rrule', rrule: repeat === 'rule' && rawRule ? rawRule : toRRule(pinned ?? rule), anchor, firstDue }
          : { type: 'interval', interval: { every: n, unit }, anchor, firstDue },
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

  const takesTurns = $derived(rotation && assignees.length >= 2 && repeat !== 'none');
  const memberName = (uid: string) => members.find((m) => m.userId === uid)?.displayName ?? '';
  function move(i: number, by: -1 | 1): void {
    const a = [...assignees];
    [a[i], a[i + by]] = [a[i + by], a[i]];
    assignees = a;
  }
  const toggle = (uid: string) => (assignees = assignees.includes(uid) ? assignees.filter((x) => x !== uid) : [...assignees, uid]);

  // Row summaries and the "Next:" preview.
  const day = (s: string) => fmt(s, { weekday: 'short', day: 'numeric', month: 'short' });
  function addPeriod(s: string, every: number, u: Unit): string {
    if (u !== 'month') return addDays(s, every * (u === 'week' ? 7 : 1));
    const d = parseYmd(s);
    const m = d.getUTCMonth() + every;
    const last = new Date(Date.UTC(d.getUTCFullYear(), m + 1, 0)).getUTCDate();
    return ymd(new Date(Date.UTC(d.getUTCFullYear(), m, Math.min(d.getUTCDate(), last))));
  }
  const today = householdToday();
  const repeatText = $derived(
    repeat === 'none' ? t('repeat.none')
      : repeat === 'rule' ? (rawRule ? t('rule.customRaw', { rule: rawRule }) : describe(rule))
        : pinned ? describe(pinned)
        : PRESET[`${n}${unit}`] ? t(PRESET[`${n}${unit}`]) : t('repeat.every', { count: n, unit: t(UNIT[unit], { count: n }) }),
  );
  const repeatSub = $derived(
    !firstDue ? '' : repeat === 'none' ? t('chore.dueOn', { date: day(firstDue) })
      : t(anchor === 'from_completion' ? 'chore.startsFromDone' : 'chore.startsFixed', { date: day(firstDue) }),
  );
  const nextText = $derived(
    !firstDue || repeat === 'none' ? ''
      : repeat === 'rule' || pinned ? t('chore.nextOnly', { date: day(firstDue) })
        : t(anchor === 'from_completion' ? 'chore.nextAbout' : 'chore.next', { date: day(firstDue), next: day(addPeriod(firstDue, n, unit)) }),
  );
  const dayLong = (d: string) => fmt(`2024-01-0${DAYS.indexOf(d) + 1}`, { weekday: 'long' }); // 2024-01-01 is a Monday
  const dayShort = (d: string) => fmt(`2024-01-0${DAYS.indexOf(d) + 1}`, { weekday: 'short' });
  const monthDays = [...Array.from({ length: 31 }, (_, i) => ({ value: String(i + 1), label: String(i + 1) })), { value: '-1', label: t('rule.lastDay.option') }];
  const nths = [1, 2, 3, 4, 5, -1].map((v) => ({ value: String(v), label: t(nthKey(v)) }));
  const REMINDER_LABEL: Record<string, Key> = {
    '': 'reminder.default', gentle: 'reminder.gentle', nag: 'reminder.nag', none: 'reminder.none', custom: 'reminder.customOption',
  };
  const areaText = $derived(areaId === '__new' ? newArea.trim() || t('chore.newArea') : areas.find((a) => a.id === areaId)?.name ?? t('chore.noArea'));
  const notesText = $derived(notes.trim().split('\n')[0] || link.trim() || t('chore.optional'));
  const quick = $derived(templates.slice(0, 4));
  function closeRepeat(): void {
    if (!firstDue) firstDue = today;
    sheet = '';
  }
</script>

<section class="card narrow">
  <h1>{id ? t('chore.edit') : t('chore.add')}</h1>
  {#if !loaded}
    <p role="status" class="muted">{t('common.loading')}</p>
  {:else}
    <form class="stack" id="chore-form" onsubmit={submit}>
      <div class="field">
        <label for="chore-name">{t('chore.name')}</label>
        <div class="namebox" class:with-ai={!id && app.instance.aiEnabled}>
          <input id="chore-name" class="big" bind:value={name} required maxlength="200" placeholder={t('chore.namePlaceholder')} />
          {#if !id && app.instance.aiEnabled}
            <button type="button" class="ai-in" aria-haspopup="dialog" aria-label={t('ai.chip')} title={t('ai.chip')} onclick={() => (sheet = 'ai')}><Icon name="sparkle" /></button>
          {/if}
        </div>
      </div>
      {#if !id && templates.length}
        <div class="chips quick" role="group" aria-label={t('chore.template')}>
          {#each quick as tp (tp.key)}
            <button type="button" class="chip" onclick={() => useTemplate(tp.key)}>{nameOf('template', tp.key)}</button>
          {/each}
          {#if templates.length > quick.length}
            <button type="button" class="chip" aria-haspopup="dialog" onclick={() => (sheet = 'templates')}>{t('chore.templateMore')}</button>
          {/if}
        </div>
      {/if}

      <fieldset class="group who">
        <legend class="label">{t('chore.who')}</legend>
        <div class="pills">
          {#each members as m (m.userId)}
            <label class="pill">
              <input type="checkbox" class="cover" checked={assignees.includes(m.userId)} onchange={() => toggle(m.userId)} />
              <span class="avatar" aria-hidden="true">{initials(m.displayName)}</span>
              <span>{m.displayName}</span>
            </label>
          {/each}
        </div>
        <p class="hint">{assignees.length ? t('chore.someHint') : t('chore.anyoneHint')}</p>
        {#if assignees.length >= 2 && repeat !== 'none'}
          <label class="check"><input type="checkbox" bind:checked={rotation} />{t('chore.takeTurns')}</label>
          {#if rotation}
            <p class="hint">{t('chore.takeTurnsHint')}</p>
            <ol class="order" aria-label={t('chore.turnOrder')}>
              {#each assignees as uid, i (uid)}
                <li>
                  <span>{memberName(uid)}</span>
                  <Button variant="quiet" disabled={i === 0} aria-label={t('chore.moveUp', { name: memberName(uid) })} onclick={() => move(i, -1)}>&uarr;</Button>
                  <Button variant="quiet" disabled={i === assignees.length - 1} aria-label={t('chore.moveDown', { name: memberName(uid) })} onclick={() => move(i, 1)}>&darr;</Button>
                </li>
              {/each}
            </ol>
            <label class="check"><input type="checkbox" bind:checked={onlyTurn} />{t('chore.advanceOnly')}</label>
          {/if}
        {/if}
      </fieldset>

      <div class="group rows">
        {#if areasOn}
          <button type="button" class="rowbtn" aria-haspopup="dialog" onclick={() => (sheet = 'area')}>
            <span class="ico area"><Icon name="tag" /></span>
            <span class="txt"><span class="visually-hidden">{t('chore.area')}:</span><strong>{areaText}</strong></span>
            <Icon name="chevron" />
          </button>
        {/if}
        <button type="button" class="rowbtn" aria-haspopup="dialog" onclick={() => (sheet = 'repeat')}>
          <span class="ico rep"><Icon name="repeat" /></span>
          <span class="txt"><span class="visually-hidden">{t('chore.repeat')}:</span><strong>{repeatText}</strong><span class="sub">{repeatSub}</span></span>
          <Icon name="chevron" />
        </button>
        <button type="button" class="rowbtn" aria-haspopup="dialog" onclick={() => (sheet = 'reminder')}>
          <span class="ico rem"><Icon name="bell" /></span>
          <span class="txt">
            <span class="visually-hidden">{t('chore.reminders')}:</span><strong>{t(REMINDER_LABEL[reminder] ?? 'reminder.default')}</strong>
            {#if showTime && remTime}<span class="sub">{t('chore.remindAtShort', { time: remTime })}</span>{/if}
          </span>
          <Icon name="chevron" />
        </button>
        <button type="button" class="rowbtn" aria-expanded={notesOpen} aria-controls="chore-notes" onclick={() => (notesOpen = !notesOpen)}>
          <span class="ico note"><Icon name="note" /></span>
          <span class="txt"><strong>{t('chore.notesLink')}</strong><span class="sub">{notesText}</span></span>
          <span class="chev" class:open={notesOpen}><Icon name="chevron" /></span>
        </button>
        <div id="chore-notes" class="notes stack" hidden={!notesOpen}>
          <Field label={t('chore.notes')} hint={t('chore.notesHint')}>
            {#snippet children({ id: fid, describedby })}
              <textarea id={fid} aria-describedby={describedby} bind:value={notes} rows="3" maxlength="10000"></textarea>
            {/snippet}
          </Field>
          <Field label={t('chore.link')} type="url" bind:value={link} placeholder="https://" hint={t('chore.linkHint')} error={linkErr} maxlength={2000} />
        </div>
      </div>

      <div aria-live="polite"><Alert message={err} /></div>
      <div class="row">
        <Button type="submit" {busy}>{t('common.save')}</Button>
        <a class="btn btn-quiet" href={href(id ? `/chores/${id}` : '/')}>{t('common.cancel')}</a>
      </div>
    </form>
  {/if}
</section>

<!-- Sheets sit outside the form: Enter in a sheet never saves the chore half-way. -->
<Sheet open={sheet === 'ai'} title={t('ai.describe')} onclose={() => (sheet = '')}>
  <AiSuggest {base} {areas} {members} onEdit={applySuggestion} />
</Sheet>

<Sheet open={sheet === 'templates'} title={t('chore.template')} onclose={() => (sheet = '')}>
  <div class="chips">
    {#each templates as tp (tp.key)}
      <button type="button" class="chip" onclick={() => useTemplate(tp.key)}>{nameOf('template', tp.key)}</button>
    {/each}
  </div>
</Sheet>

<Sheet open={sheet === 'area'} title={t('chore.area')} onclose={() => (sheet = '')}>
  <fieldset class="plain-fieldset stack-sm">
    <legend class="visually-hidden">{t('chore.area')}</legend>
    <label class="opt"><input type="radio" name="area" value="" bind:group={areaId} /><span>{t('chore.noArea')}</span></label>
    {#each areas as a (a.id)}
      <label class="opt"><input type="radio" name="area" value={a.id} bind:group={areaId} /><span>{a.name}</span></label>
    {/each}
    <label class="opt"><input type="radio" name="area" value="__new" bind:group={areaId} /><span>{t('chore.newArea')}</span></label>
  </fieldset>
  {#if areaId === '__new'}<Field label={t('chore.newAreaName')} bind:value={newArea} maxlength={100} />{/if}
</Sheet>

<Sheet open={sheet === 'repeat'} title={t('chore.repeat')} onclose={closeRepeat}>
  <div class="stack-sm">
    <Field label={t('chore.firstDue')} type="date" bind:value={firstDue} required />
    <div class="seg">
      <button type="button" aria-pressed={firstDue === today} onclick={() => (firstDue = today)}>{t('chore.today')}</button>
      <button type="button" aria-pressed={firstDue === addDays(today, 1)} onclick={() => (firstDue = addDays(today, 1))}>{t('chore.tomorrow')}</button>
    </div>
  </div>

  <fieldset class="plain-fieldset stack-sm">
    <legend class="label">{t('chore.howOften')}</legend>
    <div class="seg four">
      {#each [['none', 'chore.once'], ['day', 'chore.daily'], ['week', 'chore.weekly'], ['month', 'chore.monthly']] as [f, k] (f)}
        <label>
          <input type="radio" class="visually-hidden" name="freq" checked={f === 'none' ? repeat === 'none' : repeat === 'interval' && unit === f} onchange={() => chooseFreq(f as 'none' | Unit)} />
          <span>{t(k as Key)}</span>
        </label>
      {/each}
    </div>
    {#if repeat === 'interval'}
      <div class="stepper">
        <label for="every-n">{t('chore.every')}</label>
        <button type="button" class="btn btn-quiet" aria-label={t('chore.fewer')} disabled={n <= 1} onclick={() => (n = Math.max(1, n - 1))}>&minus;</button>
        <input id="every-n" type="number" min="1" max="1000" inputmode="numeric" bind:value={() => String(n), (v) => (n = Math.min(1000, Math.max(1, Math.round(Number(v)) || 1)))} />
        <button type="button" class="btn btn-quiet" aria-label={t('chore.oneMore')} disabled={n >= 1000} onclick={() => (n = Math.min(1000, n + 1))}>+</button>
        <span>{t(UNIT[unit], { count: n })}</span>
      </div>
      {#if unit === 'week'}
        <div class="stack-sm" role="group" aria-labelledby="on-week">
          <span class="label" id="on-week">{t('chore.on')}</span>
          <div class="days">
            {#each DAYS as d (d)}
              <button type="button" aria-pressed={onDays.includes(d)} aria-label={dayLong(d)} onclick={() => toggleDay(d)}>{dayShort(d)}</button>
            {/each}
          </div>
          <p class="hint">{onDays.length ? t('chore.onDaysHint') : t('chore.onAnyHint')}</p>
        </div>
      {:else if unit === 'month'}
        <fieldset class="plain-fieldset stack-sm">
          <legend class="label">{t('chore.on')}</legend>
          <label class="opt"><input type="radio" name="monthOn" checked={monthOn === 'any'} onchange={() => setMonthOn('any')} /><span>{t('chore.onAny')}</span></label>
          <label class="opt"><input type="radio" name="monthOn" checked={monthOn === 'date'} onchange={() => setMonthOn('date')} /><span>{t('chore.onDate')}</span></label>
          {#if monthOn === 'date'}
            <div class="indent">
              <Select label={t('rule.dayOfMonth')} bind:value={() => String(monthDay), (v) => { monthDay = Number(v); alignFirstDue(); }} options={monthDays} />
            </div>
          {/if}
          <label class="opt"><input type="radio" name="monthOn" checked={monthOn === 'nth'} onchange={() => setMonthOn('nth')} /><span>{t('chore.onNth')}</span></label>
          {#if monthOn === 'nth'}
            <div class="indent row">
              <Select label={t('rule.nth')} bind:value={() => String(nth), (v) => { nth = Number(v); alignFirstDue(); }} options={nths} />
              <Select label={t('rule.weekday')} bind:value={() => nthDay, (v) => { nthDay = v; alignFirstDue(); }} options={DAYS.map((d) => ({ value: d, label: dayLong(d) }))} />
            </div>
          {/if}
        </fieldset>
      {/if}
    {/if}
    {#if repeat === 'rule' && rawRule}
      <div class="raw stack-sm">
        <p class="hint">{t('rule.rawNote')}</p>
        <p class="mono">{rawRule}</p>
        <p><button type="button" class="linkish" onclick={() => (rawRule = '')}>{t('rule.rawReplace')}</button></p>
      </div>
    {:else if repeat === 'rule'}
      <RuleBuilder bind:rule />
    {:else}
      <p><button type="button" class="linkish" onclick={() => setRepeat('rule')}>{t('chore.customRule')}</button></p>
    {/if}
  </fieldset>

  {#if repeat !== 'none'}
    <fieldset class="plain-fieldset stack-sm">
      <legend class="label">{t('chore.anchor')}</legend>
      <label class="opt card-opt">
        <input type="radio" name="anchor" value="from_completion" bind:group={anchor} />
        <span><strong>{t('chore.anchorCompletion')}</strong><span class="hint">{t('chore.anchorCompletionHint')}</span></span>
      </label>
      <label class="opt card-opt">
        <input type="radio" name="anchor" value="from_scheduled" bind:group={anchor} />
        <span><strong>{t('chore.anchorScheduled')}</strong><span class="hint">{t('chore.anchorScheduledHint')}</span></span>
      </label>
    </fieldset>
  {/if}
  {#if nextText}<p class="next" role="status">{nextText}</p>{/if}
</Sheet>

<Sheet open={sheet === 'reminder'} title={t('chore.reminders')} onclose={() => (sheet = '')}>
  {#if showTime}
    <Field label={t('chore.remindAt')} type="time" bind:value={() => remTime, setRemTime} hint={t(mixed ? 'chore.remindAtMixed' : 'chore.remindAtHint')} class="big-time" />
  {/if}
  <fieldset class="plain-fieldset stack-sm">
    <legend class="visually-hidden">{t('chore.reminders')}</legend>
    {#each ['', 'gentle', 'nag', 'none', 'custom'] as v (v)}
      <label class="opt"><input type="radio" name="reminder" value={v} checked={reminder === v} onchange={() => setReminder(v)} /><span>{t(REMINDER_LABEL[v])}</span></label>
    {/each}
  </fieldset>
  {#if reminder === 'custom'}<ReminderEditor bind:items={customRem} />{/if}
  <p class="hint">{t('reminder.presetHint')}</p>
</Sheet>

<style>
  :global(.big) {
    font-size: 1.375rem;
    font-weight: 700;
  }
  :global(.big-time) {
    font-size: 2rem;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  /* On phones the quick chips scroll sideways in one line instead of taking several rows. */
  @media (max-width: 767px) {
    .chips.quick {
      flex-wrap: nowrap;
      overflow-x: auto;
      margin-inline: calc(var(--space) * -1.25);
      padding-inline: calc(var(--space) * 1.25);
      scrollbar-width: none;
    }
  }
  .chip {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: var(--tap);
    padding-inline: 16px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: var(--surface-2);
    font-weight: 500;
    cursor: pointer;
  }
  .namebox {
    position: relative;
  }
  .namebox.with-ai input {
    padding-inline-end: 56px;
  }
  .ai-in {
    position: absolute;
    inset-block: 0;
    inset-inline-end: 4px;
    margin-block: auto;
    display: grid;
    place-items: center;
    inline-size: var(--tap);
    block-size: var(--tap);
    border: 0;
    border-radius: var(--radius);
    background: none;
    color: var(--accent);
    cursor: pointer;
  }
  .ai-in:hover {
    background: var(--surface-2);
  }
  .days {
    display: grid;
    grid-template-columns: repeat(7, minmax(0, 1fr));
    gap: 6px;
  }
  .days button {
    min-block-size: var(--tap);
    padding: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    font-size: 0.875rem;
    font-weight: 600;
    cursor: pointer;
  }
  .days button[aria-pressed="true"] {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .indent {
    padding-inline-start: 32px;
  }
  .group {
    margin: 0;
    border: 0;
    border-radius: var(--radius-lg);
    background: var(--surface-2);
    min-inline-size: 0;
  }
  .who {
    display: grid;
    gap: 10px;
    padding: var(--space);
  }
  .who legend {
    float: inline-start;
    padding: 0;
  }
  .pills {
    clear: both;
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-block-size: var(--tap);
    padding-inline: 6px 14px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: var(--surface);
    font-weight: 600;
    cursor: pointer;
  }
  .pill {
    position: relative;
  }
  /* The real checkbox covers the whole pill, invisible: clicks, taps and screen readers all reach it. */
  .cover {
    position: absolute;
    inset: 0;
    inline-size: 100%;
    block-size: 100%;
    margin: 0;
    opacity: 0;
    cursor: pointer;
  }
  .pill:has(input:checked) .avatar {
    background: var(--surface);
    color: var(--text);
  }
  .pill:has(input:checked) {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .pill:has(input:focus-visible) {
    outline: 3px solid var(--focus);
    outline-offset: 2px;
  }
  .order {
    list-style: decimal inside;
    margin: 0;
    padding: 0;
  }
  .order > li {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .order > li > span {
    flex: 1;
  }
  .rows {
    display: grid;
    overflow: hidden;
  }
  .rowbtn {
    display: flex;
    align-items: center;
    gap: 14px;
    inline-size: 100%;
    min-block-size: 60px;
    padding-block: 10px;
    padding-inline: var(--space);
    border: 0;
    border-block-start: 1px solid var(--border);
    background: none;
    text-align: start;
    cursor: pointer;
  }
  .rowbtn:first-child {
    border-block-start: 0;
  }
  .txt {
    flex: 1;
    display: grid;
    gap: 2px;
    min-inline-size: 0;
  }
  .txt strong {
    overflow-wrap: anywhere;
  }
  .sub {
    font-size: 0.875rem;
    color: var(--text-2);
  }
  .ico {
    display: grid;
    place-items: center;
    inline-size: 36px;
    block-size: 36px;
    border-radius: 10px;
    background: var(--surface);
    color: var(--accent);
    flex: none;
  }
  .rowbtn > :global(svg:last-child),
  .chev {
    color: var(--text-2);
    flex: none;
  }
  :global([dir="rtl"]) .rowbtn > :global(svg:last-child) {
    transform: scaleX(-1);
  }
  .chev {
    display: grid;
    transition: transform var(--dur) var(--ease);
  }
  .chev.open {
    transform: rotate(90deg);
  }
  .notes {
    padding: 0 var(--space) var(--space);
  }
  .notes[hidden] {
    display: none;
  }
  .seg {
    display: flex;
    gap: 8px;
  }
  .seg > * {
    flex: 1;
  }
  .seg button,
  .seg span {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 100%;
    min-block-size: var(--tap);
    padding-inline: 4px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    font-size: 0.9375rem;
    white-space: nowrap;
    background: var(--surface-2);
    font-weight: 600;
    cursor: pointer;
  }
  .seg button[aria-pressed="true"],
  .seg input:checked + span {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  .seg input:focus-visible + span {
    outline: 3px solid var(--focus);
    outline-offset: 2px;
  }
  .seg label {
    position: relative;
  }
  .stepper {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .stepper label {
    font-weight: 600;
  }
  .stepper input {
    inline-size: 5rem;
    text-align: center;
    font-weight: 700;
  }
  .opt {
    display: flex;
    align-items: center;
    gap: 12px;
    min-block-size: var(--tap);
    cursor: pointer;
  }
  .opt input {
    inline-size: 20px;
    block-size: 20px;
    margin: 0;
    flex: none;
  }
  .card-opt {
    align-items: flex-start;
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .card-opt:has(input:checked) {
    border: 2px solid var(--accent);
    padding: 11px 13px;
  }
  .card-opt input {
    margin-block-start: 2px;
  }
  .card-opt > span {
    display: grid;
    gap: 2px;
  }
  .linkish {
    padding: 0;
    border: 0;
    background: none;
    color: var(--accent);
    font-weight: 600;
    text-decoration: underline;
    cursor: pointer;
    min-block-size: var(--tap);
  }
  .raw {
    padding: 12px 14px;
    border-radius: var(--radius);
    background: var(--surface-2);
  }
  .next {
    margin: 0;
    padding: 12px 14px;
    border-radius: var(--radius);
    background: var(--surface-2);
  }
  p {
    margin: 0;
  }
</style>
