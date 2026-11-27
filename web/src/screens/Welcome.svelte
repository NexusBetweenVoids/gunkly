<script lang="ts">
  import { appOrigin } from '../lib/base';
  // First run for someone without a household: confirm the email (hosted service), name the household,
  // pick a few starter chores, invite someone. Every step after the household can be skipped, and
  // everything can be changed later in the normal screens. Run again (Settings, Profile) by someone
  // who already has a household: the household step offers theirs, starter chores already there
  // are left out, and the invite step only shows for owners.
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import TimeZone from '../components/TimeZone.svelte';
  import Passkeys from './account/Passkeys.svelte';
  import Totp from './account/Totp.svelte';
  import { api } from '../lib/api';
  import { copyText } from '../lib/clip';
  import { addDays, householdToday } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { t, type Key } from '../lib/i18n';
  import { nameOf, UNIT } from '../lib/names';
  import { navigate } from '../lib/router.svelte';
  import { app, currentHousehold, refreshHouseholds, selectHousehold } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { Area, Chore, Household, Template } from '../lib/types';
  import { localZone, zones } from '../lib/zones';

  type Step = 'verify' | 'household' | 'chores' | 'invite' | 'secure';
  // The hosted service needs a confirmed email address (which also turns on two-step verification)
  // before a household can be created; self-hosted instances skip that step.
  const needsVerify = $derived(app.instance.hostedMode && !!app.user?.email && !app.user.emailVerified);
  // Households this person can add chores to (a guest cannot), offered when running the setup again.
  const usable = app.households.filter((h) => h.role !== 'guest');
  const again = usable.length > 0;
  let hh = $state<Household | null>(null);
  // Only owners can invite: for a household of someone else's, that step is left out.
  const canInvite = $derived(!hh || hh.role === 'owner');
  const steps = $derived<Step[]>([
    ...(app.instance.hostedMode ? (['verify'] as Step[]) : []),
    'household',
    'chores',
    ...(canInvite ? (['invite'] as Step[]) : []),
    'secure',
  ]);
  let step = $state<Step>('household');
  $effect(() => {
    if (step === 'verify' && !needsVerify) step = 'household';
  });
  if (app.instance.hostedMode && app.user?.email && !app.user.emailVerified) step = 'verify';
  const LABELS: Record<Step, Key> = {
    verify: 'welcome.step.verify', household: 'welcome.step.household', chores: 'welcome.step.chores', invite: 'welcome.step.invite', secure: 'welcome.step.secure',
  };

  let busy = $state(false);
  let err = $state('');

  // Step: verify.
  async function resend(): Promise<void> {
    busy = true;
    try {
      await api<unknown>('POST', '/account/email/resend');
      toast(t('banner.sent'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  // Step: household.
  const first = (app.user?.displayName ?? '').trim().split(/\s+/)[0] ?? '';
  let name = $state(first ? t('welcome.nameSuggestion', { name: first }) : '');
  let timezone = $state(localZone);
  let areas = $state(false); // opt-in: grouping by area is something to choose, not a default
  let tzErr = $state('');
  const cur = currentHousehold();
  let choice = $state(again ? (cur && usable.some((h) => h.id === cur.id) ? cur.id : usable[0].id) : 'new');

  async function createHousehold(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = '';
    const existing = usable.find((h) => h.id === choice);
    if (existing) {
      hh = existing;
      areas = existing.areasEnabled;
      selectHousehold(existing.id);
      step = 'chores';
      void loadTemplates();
      return;
    }
    tzErr = zones.includes(timezone) || timezone === localZone ? '' : t('field.timezoneInvalid');
    if (tzErr) return;
    busy = true;
    try {
      hh = await api<Household>('POST', '/households', { name: name.trim(), timezone, areasEnabled: areas, starterAreas: areas });
      await refreshHouseholds(hh.id);
      selectHousehold(hh.id);
      step = 'chores';
      void loadTemplates();
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  // Step: chores. A few common ones are picked to start with.
  const PICKED = ['take_out_trash', 'vacuum', 'clean_bathroom', 'change_bedding', 'wipe_counters', 'water_plants'];
  let templates = $state<Template[]>([]);
  let picked = $state<string[]>([...PICKED]);
  let have = $state<string[]>([]); // templates whose chore the household already has (by name)
  const groups = $derived.by(() => {
    const by = new Map<string, Template[]>();
    for (const tp of templates) by.set(tp.area, [...(by.get(tp.area) ?? []), tp]);
    return [...by.entries()];
  });
  async function loadTemplates(): Promise<void> {
    try {
      const [list, chores] = await Promise.all([
        api<Template[]>('GET', '/templates'),
        hh && again ? api<Chore[]>('GET', `/households/${hh.id}/chores`) : Promise.resolve([] as Chore[]),
      ]);
      const names = new Set(chores.map((c) => c.name.trim().toLowerCase()));
      have = list.filter((tp) => names.has(nameOf('template', tp.key).toLowerCase())).map((tp) => tp.key);
      picked = picked.filter((k) => !have.includes(k));
      templates = list;
    } catch (x) {
      err = failure(x).message;
    }
  }
  const toggle = (key: string) => (picked = picked.includes(key) ? picked.filter((k) => k !== key) : [...picked, key]);
  // The editor's wording for the common intervals ("Every week", "Every year").
  const PRESET: Record<string, Key> = {
    '1day': 'repeat.daily', '1week': 'repeat.weekly', '2week': 'repeat.2weeks', '1month': 'repeat.monthly',
    '3month': 'repeat.3months', '6month': 'repeat.6months', '12month': 'repeat.yearly',
  };
  const every = (tp: Template) => {
    const { every: n, unit } = tp.interval;
    const p = PRESET[`${n}${unit}`];
    return p ? t(p) : t('repeat.every', { count: n, unit: t(UNIT[unit], { count: n }) });
  };

  async function addChores(): Promise<void> {
    if (!hh) return;
    busy = true;
    err = '';
    try {
      const areaList = areas ? await api<Area[]>('GET', `/households/${hh.id}/areas`) : [];
      const today = householdToday();
      const chosen = templates.filter((tp) => picked.includes(tp.key));
      // Spread the first due dates over the coming week, so not everything is due today.
      for (const [i, tp] of chosen.entries()) {
        const periodDays = tp.interval.every * (tp.interval.unit === 'day' ? 1 : tp.interval.unit === 'week' ? 7 : 30);
        const area = areaList.find((a) => a.name.toLowerCase() === nameOf('area', tp.area).toLowerCase());
        await api('POST', `/households/${hh.id}/chores`, {
          name: nameOf('template', tp.key),
          kind: 'recurring',
          assignees: [],
          ...(area ? { areaId: area.id } : {}),
          schedule: { type: 'interval', interval: tp.interval, firstDue: addDays(today, Math.min(i % 7, periodDays - 1)) },
        });
      }
      if (chosen.length) toast(t('welcome.choresAdded', { count: chosen.length }));
      afterChores();
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  const afterChores = () => (step = canInvite ? 'invite' : 'secure');

  // Step: invite.
  let link = $state('');
  const canShare = typeof navigator !== 'undefined' && 'share' in navigator;
  async function makeInvite(): Promise<void> {
    if (!hh) return;
    busy = true;
    err = '';
    try {
      const inv = await api<{ token: string }>('POST', `/households/${hh.id}/invites`, { role: 'member', expiresInHours: 72, maxUses: 1 });
      link = `${appOrigin()}/invite/${inv.token}`;
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
  async function share(): Promise<void> {
    try {
      await navigator.share({ title: t('welcome.shareTitle'), text: t('welcome.shareText', { household: hh?.name ?? '' }), url: link });
    } catch {
      // dismissed or unavailable: the copy button stays
    }
  }
  const finish = () => navigate('/', true);
</script>

<section class="card narrow stack welcome">
  <div>
    <h1>{t('welcome.title')}</h1>
    <p class="muted">{again ? t('welcome.introAgain') : t('welcome.intro')}</p>
  </div>
  <ol class="progress" aria-label={t('welcome.progress')}>
    {#each steps as s, i (s)}
      <li class:done={steps.indexOf(step) > i} aria-current={s === step ? 'step' : undefined}>
        <span class="dot" aria-hidden="true">{steps.indexOf(step) > i ? '✓' : i + 1}</span><span class="lbl">{t(LABELS[s])}</span>
      </li>
    {/each}
  </ol>

  {#if step === 'verify'}
    <div class="stack" aria-live="polite">
      <h2>{t('welcome.verifyTitle')}</h2>
      <p>{t('welcome.verifyBody', { email: app.user?.email ?? '' })}</p>
      <p class="hint">{t('welcome.verifyHint')}</p>
      <Alert message={err} />
      <div><Button variant="quiet" {busy} onclick={resend}>{t('banner.resend')}</Button></div>
    </div>
  {:else if step === 'household'}
    <form class="stack" onsubmit={createHousehold}>
      {#if again}
        <fieldset class="plain-fieldset stack-sm">
          <legend><h2>{t('welcome.chooseTitle')}</h2></legend>
          {#each usable as h (h.id)}
            <label class="check"><input type="radio" name="hh" value={h.id} bind:group={choice} />{h.name}</label>
          {/each}
          <label class="check"><input type="radio" name="hh" value="new" bind:group={choice} />{t('welcome.chooseNew')}</label>
        </fieldset>
      {:else}
        <h2>{t('welcome.householdTitle')}</h2>
        <p class="hint">{t('welcome.householdHint')}</p>
      {/if}
      {#if choice === 'new'}
        <Field label={t('household.name')} bind:value={name} required maxlength={100} />
        <TimeZone bind:value={timezone} error={tzErr} />
        <label class="check"><input type="checkbox" bind:checked={areas} />{t('welcome.areas')}</label>
      {/if}
      <Alert message={err} />
      <div><Button type="submit" {busy}>{choice === 'new' ? t('welcome.householdNext') : t('welcome.next')}</Button></div>
    </form>
  {:else if step === 'chores'}
    <div class="stack">
      <h2>{t('welcome.choresTitle')}</h2>
      <p class="hint">{t('welcome.choresHint')}</p>
      {#if !templates.length && !err}<p role="status" class="muted">{t('common.loading')}</p>{/if}
      {#each groups as [area, list] (area)}
        <fieldset class="plain-fieldset group">
          <legend class="label">{nameOf('area', area)}</legend>
          {#each list as tp (tp.key)}
            <label class="pick">
              <input type="checkbox" checked={picked.includes(tp.key)} onchange={() => toggle(tp.key)} />
              <span>{nameOf('template', tp.key)}</span>
              <span class="muted every">{have.includes(tp.key) ? t('welcome.alreadyThere') : every(tp)}</span>
            </label>
          {/each}
        </fieldset>
      {/each}
      <Alert message={err} />
      <div class="actions">
        <Button {busy} disabled={!picked.length} onclick={addChores}>{t('welcome.addChores', { count: picked.length })}</Button>
        <Button variant="quiet" onclick={afterChores}>{t('welcome.skip')}</Button>
      </div>
    </div>
  {:else if step === 'invite'}
    <div class="stack">
      <h2>{t('welcome.inviteTitle')}</h2>
      <p class="hint">{t('welcome.inviteHint')}</p>
      {#if link}
        <p class="mono link">{link}</p>
        <p class="hint">{t('invite.linkOnce')}</p>
        <div class="actions">
          {#if canShare}<Button onclick={share}>{t('welcome.share')}</Button>{/if}
          <Button variant={canShare ? 'quiet' : 'primary'} onclick={() => copyText(link)}>{t('common.copy')}</Button>
        </div>
      {:else}
        <div><Button variant="quiet" {busy} onclick={makeInvite}>{t('welcome.makeInvite')}</Button></div>
      {/if}
      <Alert message={err} />
      <div class="actions finish">
        <Button onclick={() => (step = 'secure')}>{t('welcome.next')}</Button>
      </div>
    </div>
  {:else}
    <div class="stack secure">
      <h2>{t('welcome.secureTitle')} <span class="badge">{t('welcome.optional')}</span></h2>
      <p class="hint">{app.user?.emailOtpEnabled ? t('welcome.secureHintEmail') : t('welcome.secureHint')}</p>
      {#if app.instance.passkeysAvailable}<Passkeys />{/if}
      <Totp />
      <p class="hint">{t('welcome.secureLater')}</p>
      <div class="actions finish">
        <Button onclick={finish}>{t('welcome.finish')}</Button>
      </div>
    </div>
  {/if}
</section>

<style>
  .welcome h1 {
    margin-block-end: 4px;
  }
  .welcome h2 {
    font-size: 1.25rem;
    margin: 0;
  }
  .progress {
    display: flex;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .progress li {
    flex: 1;
    display: grid;
    align-content: start;
    justify-items: center;
    gap: 4px;
    min-inline-size: 0;
    font-size: 0.8125rem;
    color: var(--text-2);
    text-align: center;
  }
  .dot {
    display: grid;
    place-items: center;
    inline-size: 28px;
    block-size: 28px;
    border-radius: 999px;
    background: var(--surface-2);
    border: 1px solid var(--border);
    font-weight: 700;
    color: var(--text);
  }
  .progress li[aria-current='step'] {
    color: var(--text);
    font-weight: 600;
  }
  .progress li[aria-current='step'] .dot,
  .progress li.done .dot {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  /* Step names break between words only, never inside one ("Househol/d"). */
  .lbl {
    overflow-wrap: normal;
    hyphens: manual;
  }
  /* Phone: the columns are too narrow for every name, so only the current step shows its
     name; the others keep theirs for screen readers. */
  @media (max-width: 47.99rem) {
    .welcome h1 {
      font-size: 1.75rem;
    }
    .progress li {
      grid-template-columns: minmax(0, 1fr);
    }
    .progress li[aria-current='step'] .lbl {
      white-space: nowrap; /* centred under its dot, free to reach into the empty columns beside it */
    }
    .progress li:not([aria-current='step']) .lbl {
      position: absolute;
      inline-size: 1px;
      block-size: 1px;
      overflow: hidden;
      clip-path: inset(50%);
      white-space: nowrap;
    }
  }
  .group {
    display: grid;
    gap: 2px;
  }
  .pick {
    display: flex;
    align-items: center;
    gap: 10px;
    min-block-size: var(--tap);
    cursor: pointer;
  }
  .pick input {
    inline-size: 20px;
    block-size: 20px;
    margin: 0;
    flex: none;
  }
  .every {
    margin-inline-start: auto;
    font-size: 0.875rem;
    text-align: end;
  }
  .link {
    padding: 10px 12px;
    border-radius: var(--radius);
    background: var(--surface-2);
  }
  .finish {
    margin-block-start: 8px;
  }
  .secure h2 {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  /* The settings sections, as lighter blocks inside the welcome card. */
  .secure :global(section.card) {
    box-shadow: none;
    border: 1px solid var(--border);
    background: var(--surface);
  }
  .secure :global(section.card h2) {
    font-size: 1.0625rem;
  }
  .secure :global(section.card p) {
    font-size: 0.9375rem;
  }
  /* Optional here: outlined, so "Go to my chores" stays the main action. */
  .secure :global(section.card .btn:not(.btn-quiet):not(.btn-danger)) {
    background: transparent;
    color: var(--accent);
    box-shadow: inset 0 0 0 2px var(--accent);
  }
</style>
