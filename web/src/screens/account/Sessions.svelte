<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, t } from '../../lib/i18n';
  import { navigate } from '../../lib/router';
  import { clearSession } from '../../lib/session.svelte';
  import { toast } from '../../lib/toast.svelte';

  type Device = { id: string; browser: string; os: string; createdAt: string; lastSeenAt: string; current: boolean };
  let devices = $state<Device[]>([]);
  let err = $state('');
  const when = (s: string) => formatDate(new Date(s), { dateStyle: 'medium', timeStyle: 'short' });

  async function load(): Promise<void> {
    try {
      devices = await api<Device[]>('GET', '/sessions');
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function revoke(d: Device): Promise<void> {
    err = '';
    try {
      await api('DELETE', `/sessions/${d.id}`);
      if (d.current) {
        clearSession();
        navigate('/signin');
        return;
      }
      toast(t('sessions.revoked'));
    } catch (x) {
      err = failure(x).message;
    }
    await load();
  }

  async function revokeOthers(): Promise<void> {
    err = '';
    try {
      await api('DELETE', '/sessions');
      toast(t('sessions.revoked'));
    } catch (x) {
      err = failure(x).message;
    }
    await load();
  }
</script>

<section class="card" aria-labelledby="a-sessions">
  <h2 id="a-sessions">{t('sessions.title')}</h2>
  <Alert message={err} />
  <ul class="list">
    {#each devices as d (d.id)}
      <li>
        <span>
          <strong>{[d.browser, d.os].filter(Boolean).join(' / ') || t('sessions.unknown')}</strong>
          {#if d.current}<span class="badge">{t('sessions.current')}</span>{/if}
          <br /><span class="muted">{t('sessions.lastSeen', { date: when(d.lastSeenAt) })}</span>
        </span>
        <Button variant="quiet" onclick={() => revoke(d)}>{t('sessions.signOut')}</Button>
      </li>
    {/each}
  </ul>
  {#if devices.length > 1}
    <p><Button variant="danger" onclick={revokeOthers}>{t('sessions.signOutOthers')}</Button></p>
  {/if}
</section>
