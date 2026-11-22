<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, t, type Key } from '../../lib/i18n';

  type Ev = { event: string; ip: string; createdAt: string };
  let events = $state<Ev[]>([]);
  let err = $state('');

  const LABELS: readonly string[] = [
    'audit.setup',
    'audit.login_success',
    'audit.login_failure',
    'audit.logout',
    'audit.password_change',
    'audit.password_reset',
    'audit.totp_enable',
    'audit.totp_disable',
    'audit.recovery_code_use',
    'audit.session_revoke',
    'audit.member_role_change',
    'audit.member_remove',
    'audit.invite_create',
    'audit.invite_accept',
    'audit.invite_revoke',
    'audit.household_create',
    'audit.household_settings',
    'audit.admin_user_create',
    'audit.admin_settings',
    'audit.register_invite',
    'audit.account_export',
    'audit.household_export',
    'audit.household_import',
  ];

  const label = (ev: string) => (LABELS.includes(`audit.${ev}`) ? t(('audit.' + ev) as Key) : ev);

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
          <span>{label(e.event)}</span>
          <span class="muted">{formatDate(new Date(e.createdAt), { dateStyle: 'medium', timeStyle: 'short' })} · {e.ip}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>
