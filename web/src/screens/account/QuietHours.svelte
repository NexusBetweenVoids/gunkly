<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import { toast } from '../../lib/toast.svelte';
  import type { Me } from '../../lib/types';

  const hm = (m: number) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
  const mins = (v: string) => Number(v.slice(0, 2)) * 60 + Number(v.slice(3, 5));

  const current = app.user?.quietHours;
  let on = $state(!!current);
  let start = $state(current ? hm(current.start) : '22:00');
  let end = $state(current ? hm(current.end) : '07:00');
  let busy = $state(false);
  let err = $state('');

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      app.user = await api<Me>('PATCH', '/me', { quietHours: on ? { start: mins(start), end: mins(end) } : null });
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

<!-- Only the switch until quiet hours are on (or were saved and are being switched off). -->
<section class="card stack-sm" aria-labelledby="a-quiet">
  <h2 id="a-quiet" class="card-title">{t('quietHours.title')}</h2>
  <form class="stack-sm" onsubmit={save}>
    <label class="check"><input type="checkbox" bind:checked={on} />{t('quietHours.label')}</label>
    {#if on}
      <div class="row">
        <Field label={t('quietHours.start')} type="time" bind:value={start} required />
        <Field label={t('quietHours.end')} type="time" bind:value={end} required />
      </div>
      <p class="hint">{t('quietHours.hint')}</p>
      <p class="hint">
        {app.user?.timeZone ? t('quietHours.zoneOwn', { zone: app.user.timeZone }) : t('quietHours.zoneHousehold')}
      </p>
    {:else}
      <p class="hint">{t('quietHours.off')}</p>
    {/if}
    <div aria-live="polite"><Alert message={err} /></div>
    {#if on || app.user?.quietHours}
      <div><Button type="submit" {busy}>{t('common.save')}</Button></div>
    {/if}
  </form>
</section>
