<script lang="ts">
  // First run for someone without a household: confirm the email (hosted service), name the household,
  // pick a few starter chores, invite someone. Every step after the household can be skipped, and
  // everything can be changed later in the normal screens.
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import TimeZone from '../components/TimeZone.svelte';
  import { api } from '../lib/api';
  import { copyText } from '../lib/clip';
  import { addDays, householdToday } from '../lib/dates';
  import { failure } from '../lib/errors';
  import { t, type Key } from '../lib/i18n';
  import { nameOf, UNIT } from '../lib/names';
  import { navigate } from '../lib/router.svelte';
  import { app, refreshHouseholds, selectHousehold } from '../lib/session.svelte';
  import { toast } from '../lib/toast.svelte';
  import type { Area, Household, Template } from '../lib/types';
  import { localZone, zones } from '../lib/zones';

  type Step = 'verify' | 'household' | 'chores' | 'invite';
  // The hosted service needs a confirmed email address (which also turns on two-step verification)
  // before a household can be created; self-hosted instances skip that step.
  const needsVerify = $derived(app.instance.hostedMode && !!app.user?.email && !app.user.emailVerified);
  const steps = $derived<Step[]>(app.instance.hostedMode ? ['verify', 'household', 'chores', 'invite'] : ['household', 'chores', 'invite']);
  let step = $state<Step>('household');
  $effect(() => {
    if (step === 'verify' && !needsVerify) step = 'household';
  });
  if (app.instance.hostedMode && app.user?.email && !app.user.emailVerified) step = 'verify';
  const LABELS: Record<Step, Key> = {
    verify: 'welcome.step.verify', household: 'welcome.step.household', chores: 'welcome.step.chores', invite: 'welcome.step.invite',
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
  let areas = $state(true);
  let tzErr = $state('');
  let hh = $state<Household | null>(null);

  async function createHousehold(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = '';
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
  const groups = $derived.by(() => {
    const by = new Map<string, Template[]>();
    for (const tp of templates) by.set(tp.area, [...(by.get(tp.area) ?? []), tp]);
    return [...by.entries()];
  });
  async function loadTemplates(): Promise<void> {
    try {
      templates = await api<Template[]>('GET', '/templates');
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
      step = 'invite';
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  // Step: invite.
  let link = $state('');
  const canShare = typeof navigator !== 'undefined' && 'share' in navigator;
  async function makeInvite(): Promise<void> {
    if (!hh) return;
    busy = true;
    err = '';
    try {
      const inv = await api<{ token: string }>('POST', `/households/${hh.id}/invites`, { role: 'member', expiresInHours: 168, maxUses: 1 });
      link = `${location.origin}/invite/${inv.token}`;
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
    <p class="muted">{t('welcome.intro')}</p>
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
      <h2>{t('welcome.householdTitle')}</h2>
      <p class="hint">{t('welcome.householdHint')}</p>
      <Field label={t('household.name')} bind:value={name} required maxlength={100} />
      <TimeZone bind:value={timezone} error={tzErr} />
      <label class="check"><input type="checkbox" bind:checked={areas} />{t('welcome.areas')}</label>
      <Alert message={err} />
      <div><Button type="submit" {busy}>{t('welcome.householdNext')}</Button></div>
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
              <span class="muted every">{every(tp)}</span>
            </label>
          {/each}
        </fieldset>
      {/each}
      <Alert message={err} />
      <div class="row">
        <Button {busy} disabled={!picked.length} onclick={addChores}>{t('welcome.addChores', { count: picked.length })}</Button>
        <Button variant="quiet" onclick={() => (step = 'invite')}>{t('welcome.skip')}</Button>
      </div>
    </div>
  {:else}
    <div class="stack">
      <h2>{t('welcome.inviteTitle')}</h2>
      <p class="hint">{t('welcome.inviteHint')}</p>
      {#if link}
        <p class="mono link">{link}</p>
        <p class="hint">{t('invite.linkOnce')}</p>
        <div class="row">
          {#if canShare}<Button onclick={share}>{t('welcome.share')}</Button>{/if}
          <Button variant={canShare ? 'quiet' : 'primary'} onclick={() => copyText(link)}>{t('common.copy')}</Button>
        </div>
      {:else}
        <div><Button variant="quiet" {busy} onclick={makeInvite}>{t('welcome.makeInvite')}</Button></div>
      {/if}
      <Alert message={err} />
      <div class="row finish">
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
  .lbl {
    overflow-wrap: anywhere;
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
</style>
