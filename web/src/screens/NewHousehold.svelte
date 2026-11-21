<script lang="ts">
  import Alert from '../components/Alert.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import TimeZone from '../components/TimeZone.svelte';
  import { api } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t } from '../lib/i18n';
  import { navigate } from '../lib/router';
  import { refreshHouseholds, selectHousehold } from '../lib/session.svelte';
  import type { Household } from '../lib/types';
  import { localZone, zones } from '../lib/zones';

  let name = $state('');
  let timezone = $state(localZone);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let starter = $state(true);
  let tzErr = $state('');

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    err = { code: '', message: '' };
    tzErr = zones.includes(timezone) || timezone === localZone ? '' : t('field.timezoneInvalid');
    if (tzErr) return;
    busy = true;
    try {
      const h = await api<Household>('POST', '/households', { name: name.trim(), timezone, starterAreas: starter });
      await refreshHouseholds(h.id);
      selectHousehold(h.id);
      navigate(`/households/${h.id}`);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }
</script>

<section class="card narrow">
  <h1>{t('household.new')}</h1>
  <form class="stack" onsubmit={submit}>
    <Field label={t('household.name')} bind:value={name} required maxlength={100} />
    <TimeZone bind:value={timezone} error={tzErr} />
    <label class="check"><input type="checkbox" bind:checked={starter} />{t('household.starterAreas')}</label>
    <p class="hint">{t('household.starterAreasHint')}</p>
    <Alert message={err.message} />
    <Button type="submit" {busy}>{t('household.create')}</Button>
  </form>
</section>
