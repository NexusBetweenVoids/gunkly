<script lang="ts">
  // Shown while the server cannot deliver email (admin health: emailFailing).
  import { api } from '../../lib/api';
  import { formatDate, has, t, type Key } from '../../lib/i18n';

  type Failing = { since: string; lastError: string };
  let failing = $state<Failing | null>(null);

  // A failure here only means no warning: the rest of the page works without it.
  api<{ emailFailing?: Failing }>('GET', '/admin/health')
    .then((h) => (failing = h.emailFailing ?? null))
    .catch(() => {});

  const since = $derived(failing ? formatDate(new Date(failing.since), { dateStyle: 'medium', timeStyle: 'short' }) : '');
  const reasonKey = $derived(`emailFailing.reason.${failing?.lastError}`);
</script>

{#if failing}
  <section class="email" role="alert" aria-labelledby="email-title">
    <h2 id="email-title">{t('emailFailing.title')}</h2>
    <p>{t('emailFailing.body', { since })}</p>
    <p>{has(reasonKey) ? t(reasonKey as Key) : t('emailFailing.reason.other')}</p>
    <p>{t('emailFailing.fix')}</p>
  </section>
{/if}

<style>
  .email {
    border: 2px solid var(--danger, #b3261e);
    border-radius: 0.5rem;
    padding: 1rem;
    margin-block-end: 1.5rem;
  }
  .email h2 {
    margin-block-start: 0;
  }
</style>
