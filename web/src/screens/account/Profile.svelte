<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { available, languageName, locale, t, type Key } from '../../lib/i18n';
  import { localZone, zones } from '../../lib/zones';
  import { app } from '../../lib/session.svelte';
  import { applyTheme, storedTheme, type Theme } from '../../lib/theme';
  import { toast } from '../../lib/toast.svelte';
  import type { Me } from '../../lib/types';

  const u = app.user!;
  let displayName = $state(u.displayName);
  let lang = $state(u.locale);
  let weekStart = $state(u.weekStart === null ? '' : String(u.weekStart));
  let timeZone = $state(u.timeZone ?? '');
  let theme = $state<Theme>(storedTheme());
  let busy = $state(false);
  let err = $state('');

  const themes: { v: Theme; k: Key }[] = [
    { v: 'system', k: 'theme.system' },
    { v: 'light', k: 'theme.light' },
    { v: 'dark', k: 'theme.dark' },
  ];
  // 2024-01-07 was a Sunday, so index 0 is Sunday (matches the API's 0-6).
  const days = Array.from({ length: 7 }, (_, i) =>
    new Intl.DateTimeFormat(locale, { weekday: 'long' }).format(new Date(2024, 0, 7 + i)),
  );

  // The saved zone is always selectable, even if this browser's list lacks it.
  const zoneList = u.timeZone && !zones.includes(u.timeZone) ? [u.timeZone, ...zones] : zones;

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      app.user = await api<Me>('PATCH', '/me', {
        displayName: displayName.trim(),
        locale: lang,
        weekStart: weekStart === '' ? null : Number(weekStart),
        timeZone: timeZone === '' ? null : timeZone,
      });
      applyTheme(theme);
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

<section class="card" aria-labelledby="a-profile">
  <h2 id="a-profile">{t('account.profile')}</h2>
  <form class="stack" onsubmit={save}>
    <Field label={t('field.displayName')} bind:value={displayName} required maxlength={100} autocomplete="name" />
    <Field label={t('account.language')}>
      {#snippet children({ id })}
        <select {id} bind:value={lang}>
          <option value="">{t('account.browserDefault')}</option>
          {#each available as l (l)}<option value={l}>{languageName(l)}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <Field label={t('account.weekStart')}>
      {#snippet children({ id })}
        <select {id} bind:value={weekStart}>
          <option value="">{t('account.languageDefault')}</option>
          {#each days as d, i (i)}<option value={String(i)}>{d}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <Field label={t('account.timeZone')} hint={t('account.timeZoneHint')}>
      {#snippet children({ id, describedby })}
        <select {id} bind:value={timeZone} aria-describedby={describedby}>
          <option value="">{t('account.timeZoneHousehold')}</option>
          {#each zoneList as z (z)}<option value={z}>{z}</option>{/each}
        </select>
      {/snippet}
    </Field>
    {#if timeZone !== localZone && zoneList.includes(localZone)}
      <div><Button type="button" variant="quiet" onclick={() => (timeZone = localZone)}>{t('account.timeZoneUse', { zone: localZone })}</Button></div>
    {/if}
    <Field label={t('account.theme')} hint={t('account.themeHint')}>
      {#snippet children({ id, describedby })}
        <select {id} bind:value={theme} aria-describedby={describedby}>
          {#each themes as x (x.v)}<option value={x.v}>{t(x.k)}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <Alert message={err} />
    <div><Button type="submit" {busy}>{t('common.save')}</Button></div>
  </form>
</section>
