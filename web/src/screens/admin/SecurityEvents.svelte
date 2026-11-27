<script lang="ts">
  // Recent security alert events, from the admin health check. Each event kind has its own text,
  // secEvents.<event>, filled with the account, the household and the event's details (meta).
  import { api } from '../../lib/api';
  import { formatDate, has, t, type Key } from '../../lib/i18n';

  type Ev = { event: string; householdId: string; username: string; meta?: Record<string, string>; at: string };
  let events = $state<Ev[]>([]);
  // A failure here only means no notice: the rest of the page works without it.
  api<{ securityEvents?: Ev[] }>('GET', '/admin/health')
    .then((h) => (events = h.securityEvents ?? []))
    .catch(() => {});
  const when = (s: string) => formatDate(new Date(s), { dateStyle: 'medium', timeStyle: 'short' });
  const text = (e: Ev) => {
    const k = `secEvents.${e.event}`;
    const meta = { ...(e.meta ?? {}) };
    const sk = `secEvents.state.${meta.state}`;
    if (meta.state && has(sk)) meta.state = t(sk as Key);
    const params = { ...meta, username: e.username || t('secEvents.deletedUser'), household: e.householdId };
    return has(k) ? t(k as Key, params) : e.event;
  };
</script>

{#if events.length}
  <section class="sec" role="status" aria-labelledby="sec-title">
    <h2 id="sec-title">{t('secEvents.title')}</h2>
    <p>{t('secEvents.intro')}</p>
    <ul class="list">
      {#each events as e, i (i)}
        <li>
          <span>{text(e)}</span>
          <span class="muted">{when(e.at)}</span>
        </li>
      {/each}
    </ul>
    <p class="hint">{t('secEvents.hint')}</p>
  </section>
{/if}

<style>
  .sec {
    border: 2px solid var(--accent);
    border-radius: 0.5rem;
    padding: 1rem;
    margin-block-end: 1.5rem;
  }
  .sec h2 {
    margin-block-start: 0;
  }
  .sec li > span:first-child {
    overflow-wrap: anywhere;
  }
</style>
