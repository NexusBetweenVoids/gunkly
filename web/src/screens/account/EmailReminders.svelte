<script lang="ts">
  import Alert from '../../components/Alert.svelte';
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
      app.user = await api<Me>('PATCH', '/me', { emailReminders: on });
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

{#if app.instance.emailEnabled}
  <section class="card stack-sm" aria-labelledby="a-email-rem">
    <h2 id="a-email-rem">{t('emailReminders.title')}</h2>
    {#if verified}
      <label class="check">
        <input type="checkbox" checked={!!app.user?.emailReminders} disabled={busy} onchange={(e) => set(e.currentTarget.checked)} />
        {t('emailReminders.label')}
      </label>
      <p class="hint">{t('emailReminders.hint', { email: app.user?.email ?? '' })}</p>
      <p class="hint">{t('emailReminders.changed')}</p>
    {:else}
      <p class="muted" data-testid="email-reminders-hint">{t('emailReminders.needVerified')}</p>
      <p><a href="/account/profile">{t('emailReminders.addLink')}</a></p>
    {/if}
    <div aria-live="polite"><Alert message={err} /></div>
  </section>
{/if}
