<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import { href } from '../../lib/base';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import type { Me } from '../../lib/types';

  let err = $state('');
  let busy = $state(false);
  const verified = $derived(!!app.user?.email && app.user.emailVerified);

  async function set(on: boolean): Promise<void> {
    busy = true;
    err = '';
    try {
      app.user = await api<Me>('PATCH', '/me', { signinAlerts: on });
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

{#if app.instance.emailEnabled && app.instance.signinAlerts}
  <section class="card stack-sm" aria-labelledby="a-signin-alerts">
    <h2 id="a-signin-alerts" class="card-title">{t('signinAlerts.title')}</h2>
    {#if verified}
      <label class="check">
        <input type="checkbox" checked={!!app.user?.signinAlerts} disabled={busy} onchange={(e) => set(e.currentTarget.checked)} />
        {t('signinAlerts.label')}
      </label>
      <p class="hint">{t('signinAlerts.hint')}</p>
    {:else}
      <!-- Nothing can be sent without a confirmed address, so the setting shows as off and inactive. -->
      <label class="check inactive">
        <input type="checkbox" checked={false} disabled aria-describedby="a-signin-alerts-why" />
        {t('signinAlerts.label')}
      </label>
      <p class="hint" id="a-signin-alerts-why" data-testid="signin-alerts-inactive">
        {t('signinAlerts.needEmail')} <a href={href('/settings/profile')}>{app.user?.email ? t('ways.confirmEmail') : t('ways.addEmail')}</a>
      </p>
    {/if}
    <div aria-live="polite"><Alert message={err} /></div>
  </section>
{/if}

<style>
  .inactive {
    color: var(--text-2);
  }
</style>
