<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, t, type Key } from '../../lib/i18n';

  type Ev = { event: string; ip: string; createdAt: string };
  let events = $state<Ev[]>([]);
  let err = $state('');

  const LABELS: Record<string, Key> = {
    setup: 'audit.setup',
    login_success: 'audit.login_success',
    login_failure: 'audit.login_failure',
    logout: 'audit.logout',
    password_change: 'audit.password_change',
    password_reset: 'audit.password_reset',
    totp_enable: 'audit.totp_enable',
    totp_disable: 'audit.totp_disable',
    recovery_code_use: 'audit.recovery_code_use',
    session_revoke: 'audit.session_revoke',
    member_role_change: 'audit.member_role_change',
    member_remove: 'audit.member_remove',
    invite_create: 'audit.invite_create',
    invite_accept: 'audit.invite_accept',
    invite_revoke: 'audit.invite_revoke',
    household_create: 'audit.household_create',
    household_settings: 'audit.household_settings',
    admin_user_create: 'audit.admin_user_create',
    admin_settings: 'audit.admin_settings',
    register_invite: 'audit.register_invite',
  };

  api<Ev[]>('GET', '/account/audit')
    .then((r) => (events = r))
    .catch((x) => (err = failure(x).message));
</script>

<section class="card" aria-labelledby="a-audit">
  <h2 id="a-audit">{t('audit.title')}</h2>
  <p class="muted">{t('audit.intro')}</p>
  <Alert message={err} />
  {#if events.length === 0 && !err}
    <p class="muted">{t('audit.none')}</p>
  {:else}
    <ul class="list">
      {#each events as e, i (i)}
        <li>
          <span>{LABELS[e.event] ? t(LABELS[e.event]) : e.event}</span>
          <span class="muted">{formatDate(new Date(e.createdAt), { dateStyle: 'medium', timeStyle: 'short' })} · {e.ip}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>
