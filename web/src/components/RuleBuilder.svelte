<script lang="ts">
  import { t } from '../lib/i18n';
  import { DAYS, describe, monthName, nthKey, toRRule, type Rule } from '../lib/rrule';
  import { fmt } from '../lib/dates';
  import Field from './Field.svelte';
  import Select from './Select.svelte';

  let { rule = $bindable() }: { rule: Rule } = $props();

  const freqs = [
    { value: 'WEEKLY', label: t('rule.freq.WEEKLY') },
    { value: 'MONTHLY', label: t('rule.freq.MONTHLY') },
    { value: 'YEARLY', label: t('rule.freq.YEARLY') },
  ];
  const day = (d: string) => fmt(`2024-01-0${DAYS.indexOf(d) + 1}`, { weekday: 'long' });
  const dayOpts = DAYS.map((d) => ({ value: d, label: day(d) }));
  const nths = [1, 2, 3, 4, 5, -1].map((n) => ({ value: String(n), label: t(nthKey(n)) }));
  const monthDays = [...Array.from({ length: 31 }, (_, i) => i + 1), -1].map((n) => ({
    value: String(n),
    label: n === -1 ? t('rule.lastDay.option') : String(n),
  }));
  const refine = $derived(rule.freq === 'MONTHLY' || (rule.freq === 'YEARLY' && rule.months.length > 0));
  const toggle = (list: (string | number)[], v: string | number) =>
    list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
  const modes = [
    { value: 'none', label: t('rule.mode.none') },
    { value: 'day', label: t('rule.mode.day') },
    { value: 'nth', label: t('rule.mode.nth') },
  ];
</script>

<fieldset class="stack">
  <legend class="label">{t('rule.title')}</legend>
  <div class="row">
    <Select label={t('rule.freq')} bind:value={rule.freq} options={freqs} />
    <Field label={t('rule.every')} type="number" min="1" max="1000" bind:value={() => String(rule.interval), (v) => (rule.interval = Number(v) || 1)} />
  </div>
  {#if rule.freq === 'WEEKLY'}
    <fieldset class="row">
      <legend class="label">{t('rule.weekdays')}</legend>
      {#each dayOpts as d (d.value)}
        <label class="check"><input type="checkbox" checked={rule.weekdays.includes(d.value)} onchange={() => (rule.weekdays = toggle(rule.weekdays, d.value) as string[])} />{d.label}</label>
      {/each}
    </fieldset>
  {/if}
  {#if rule.freq === 'YEARLY'}
    <fieldset class="row">
      <legend class="label">{t('rule.months')}</legend>
      {#each Array.from({ length: 12 }, (_, i) => i + 1) as m (m)}
        <label class="check"><input type="checkbox" checked={rule.months.includes(m)} onchange={() => (rule.months = toggle(rule.months, m) as number[])} />{monthName(m)}</label>
      {/each}
    </fieldset>
  {/if}
  {#if refine}
    <Select label={t('rule.mode')} bind:value={rule.mode} options={modes} />
    {#if rule.mode === 'day'}
      <Select label={t('rule.dayOfMonth')} bind:value={() => String(rule.day), (v) => (rule.day = Number(v))} options={monthDays} />
    {:else if rule.mode === 'nth'}
      <div class="row">
        <Select label={t('rule.nth')} bind:value={() => String(rule.nth), (v) => (rule.nth = Number(v))} options={nths} />
        <Select label={t('rule.weekday')} bind:value={rule.weekday} options={dayOpts} />
      </div>
    {/if}
  {/if}
  <p class="hint" aria-live="polite"><strong>{t('rule.previewLabel')}</strong> {describe(rule)} <span class="mono">{toRRule(rule)}</span></p>
</fieldset>
