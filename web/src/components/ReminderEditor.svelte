<script lang="ts">
  import { t } from '../lib/i18n';
  import type { Reminder } from '../lib/types';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Select from './Select.svelte';

  let { items = $bindable() }: { items: Reminder[] } = $props();
  const kinds = [
    { value: 'before', label: t('reminder.before') },
    { value: 'on', label: t('reminder.on') },
    { value: 'overdue', label: t('reminder.overdue') },
  ];
  const hm = (m: number) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
</script>

<fieldset class="stack">
  <legend class="label">{t('reminder.custom')}</legend>
  <p class="hint">{t('reminder.customHint')}</p>
  {#each items as r, i (i)}
    <div class="row rem">
      <Select label={t('reminder.kind')} bind:value={r.kind} options={kinds} />
      {#if r.kind !== 'on'}
        <Field
          label={r.kind === 'before' ? t('reminder.daysBefore') : t('reminder.daysEvery')}
          type="number"
          min="1"
          max="365"
          bind:value={() => String(r.days), (v) => (r.days = Number(v) || 1)}
        />
      {/if}
      <Field
        label={t('reminder.time')}
        type="time"
        bind:value={() => hm(r.minute), (v) => (r.minute = Number(v.slice(0, 2)) * 60 + Number(v.slice(3, 5)))}
        required
      />
      <Button variant="danger" onclick={() => (items = items.filter((_, j) => j !== i))}>{t('common.remove')}</Button>
    </div>
  {/each}
  <div><Button variant="quiet" onclick={() => (items = [...items, { kind: 'on', days: 1, minute: 540 }])}>{t('reminder.add')}</Button></div>
</fieldset>
