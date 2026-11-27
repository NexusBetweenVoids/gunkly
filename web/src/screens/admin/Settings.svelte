<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { serviceName } from '../../lib/services';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { has, t, type Key } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { app } from '../../lib/session.svelte';
  import { toast } from '../../lib/toast.svelte';

  type S = {
    openSignup: boolean;
    requirePow: boolean;
    inviteRegistration: boolean;
    securityAlerts: boolean;
    minPasswordLength: number;
    sessionIdleDays: number;
    sessionMaxDays: number;
    auditRetentionDays: number;
    deliveryRetentionDays: number;
    limits: Record<string, number>;
    limitDefaults: Record<string, number>;
    enabledNotificationKinds: string[];
    availableNotificationKinds: string[];
    egressGuard: boolean;
    egressGuardLocked: boolean;
    mcpEnabled: boolean;
    oauthEnabled: boolean;
    oauthPossible: boolean;
    webhooksEnabled: boolean;
    ssoConfigured: boolean;
    ssoCreateAccounts: boolean;
    ssoName: string;
    ssoNameDefault: string;
    ssoAvailable: boolean;
    ssoIssuer?: string;
    ssoClientId?: string;
    ssoScopes?: string[];
    ssoCallback: string;
    signinLocation: 'none' | 'header' | 'database' | 'both';
    inactiveEnabled: boolean;
    inactiveWarnAfterDays: number;
    inactiveDeleteAfterDays: number;
  };
  // Which group of the admin screen this form shows; saving sends every value,
  // so the parts never overwrite each other with stale data.
  let { part, title }: { part: 'signin' | 'connections' | 'data' | 'limits'; title: string } = $props();
  let s = $state<S | null>(null);
  let minLen = $state('8');
  let idleDays = $state('30');
  let maxDays = $state('90');
  let auditDays = $state('90');
  let deliveryDays = $state('30');
  let inactiveWarn = $state('365');
  let inactiveDelete = $state('548');
  let limitInputs = $state<Record<string, string>>({});
  let savedKinds = '';
  let busy = $state(false);
  let err = $state('');

  function adopt(v: S): void {
    s = v;
    app.instance = { ...app.instance, webhooksEnabled: v.webhooksEnabled }; // the household screens follow at once
    savedKinds = [...v.enabledNotificationKinds].sort().join();
    minLen = String(v.minPasswordLength);
    idleDays = String(v.sessionIdleDays);
    maxDays = String(v.sessionMaxDays);
    auditDays = String(v.auditRetentionDays);
    deliveryDays = String(v.deliveryRetentionDays);
    inactiveWarn = String(v.inactiveWarnAfterDays);
    inactiveDelete = String(v.inactiveDeleteAfterDays);
    limitInputs = Object.fromEntries(Object.entries(v.limits).map(([k, n]) => [k, String(n)]));
  }
  api<S>('GET', '/admin/settings')
    .then(adopt)
    .catch((x) => (err = failure(x).message));

  // Limits by topic; a limit not listed here (a new one) goes to the last group.
  const LIMIT_GROUPS: { title: Key; keys: string[] }[] = [
    { title: 'admin.limitGroup.households', keys: ['households_per_user', 'members_per_household', 'chores_per_household', 'areas_per_household', 'reminders_per_chore', 'completions_per_chore', 'active_invites_per_household', 'invites_per_household_day'] },
    { title: 'admin.limitGroup.signin', keys: ['login_attempts_per_minute', 'signups_per_ip_hour', 'email_codes_account_hour', 'passkeys_per_user'] },
    { title: 'admin.limitGroup.notify', keys: ['emails_global_hour', 'emails_account_hour', 'emails_ip_hour', 'notifications_household_hour', 'notifications_household_day', 'notifications_user_day', 'channels_per_user', 'channels_per_household', 'push_subscriptions_per_user'] },
    { title: 'admin.limitGroup.integrations', keys: ['api_tokens_per_user', 'calendar_feeds_per_user', 'webhooks_per_household', 'oauth_clients', 'oauth_grants_per_user', 'oauth_unused_clients_per_ip', 'ai_requests_per_user_day'] },
  ];
  const limitGroups = $derived.by(() => {
    if (!s) return [];
    const all = Object.keys(s.limits);
    const known = new Set(LIMIT_GROUPS.flatMap((g) => g.keys));
    return LIMIT_GROUPS.map((g, i) => ({
      title: g.title,
      keys: [...g.keys.filter((k) => k in s!.limits), ...(i === LIMIT_GROUPS.length - 1 ? all.filter((k) => !known.has(k)) : [])],
    })).filter((g) => g.keys.length);
  });
  const limitLabel = (k: string) => (has(`limit.${k}`) ? t(`limit.${k}` as Key) : k.replace(/_/g, ' '));
  const isDefault = (k: string) => Number(limitInputs[k]) === (s?.limitDefaults[k] ?? 0);
  const changedCount = $derived(s ? Object.keys(s.limits).filter((k) => !isDefault(k)).length : 0);
  // Notification services by their display names, in a stable order.
  const kinds = $derived(s ? [...s.availableNotificationKinds].sort((a, b) => serviceName(a).localeCompare(serviceName(b))) : []);

  async function resetKinds(): Promise<void> {
    busy = true;
    err = '';
    try {
      adopt(await withStepUp(() => api<S>('PATCH', '/admin/settings', { enabledNotificationKinds: null })));
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function save(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!s) return;
    busy = true;
    err = '';
    const limits: Record<string, number> = {};
    // Back at the default: 0 drops the override, so a later change of the default applies here too.
    for (const [k, v] of Object.entries(limitInputs)) if (Number(v) !== s.limits[k]) limits[k] = Number(v) === s.limitDefaults[k] ? 0 : Number(v);
    try {
      adopt(
        await withStepUp(() =>
          api<S>('PATCH', '/admin/settings', {
            openSignup: s!.openSignup,
            requirePow: s!.requirePow,
            inviteRegistration: s!.inviteRegistration,
            securityAlerts: s!.securityAlerts,
            mcpEnabled: s!.mcpEnabled,
            oauthEnabled: s!.oauthEnabled,
            webhooksEnabled: s!.webhooksEnabled,
            minPasswordLength: Number(minLen),
            sessionIdleDays: Number(idleDays),
            sessionMaxDays: Number(maxDays),
            auditRetentionDays: Number(auditDays),
            deliveryRetentionDays: Number(deliveryDays),
            inactiveEnabled: s!.inactiveEnabled,
            inactiveWarnAfterDays: Number(inactiveWarn),
            inactiveDeleteAfterDays: Number(inactiveDelete),
            limits,
            // Only when the admin changed it, so saving other settings never freezes the defaults.
            ...([...s!.enabledNotificationKinds].sort().join() === savedKinds ? {} : { enabledNotificationKinds: s!.enabledNotificationKinds }),
            ...(s!.egressGuardLocked ? {} : { egressGuard: s!.egressGuard }),
          }),
        ),
      );
      toast(t('common.saved'));
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
    }
  }
</script>

{#snippet saveRow()}
  <Alert message={err} />
  <div><Button type="submit" {busy}>{t('common.save')}</Button></div>
{/snippet}

{#if s && part === 'data'}
  <section class="card" aria-labelledby="ad-retention">
    <form class="stack" onsubmit={save}>
      <h2 id="ad-retention">{t('admin.retention')}</h2>
      <p class="muted">{t('admin.retentionHint')}</p>
      <Field label={t('admin.auditRetention')} type="number" bind:value={auditDays} min="1" max="3650" required />
      <Field label={t('admin.deliveryRetention')} type="number" bind:value={deliveryDays} min="1" max="3650" required />
      {@render saveRow()}
    </form>
  </section>
  <section class="card" aria-labelledby="ad-inactive">
    <form class="stack" onsubmit={save}>
      <h2 id="ad-inactive">{t('admin.inactive')}</h2>
      <p class="muted">{t('admin.inactiveHint')}</p>
      <label class="check"><input type="checkbox" bind:checked={s.inactiveEnabled} />{t('admin.inactiveEnabled')}</label>
      {#if s.inactiveEnabled}
        <Field label={t('admin.inactiveWarn')} type="number" bind:value={inactiveWarn} min="30" max="3650" required />
        <Field label={t('admin.inactiveDelete')} hint={t('admin.inactiveDeleteHint')} type="number" bind:value={inactiveDelete} min="60" max="3650" required />
      {/if}
      {@render saveRow()}
    </form>
  </section>
{:else}
<section class="card" aria-labelledby="ad-{part}">
  {#if s}
    <form class="stack" onsubmit={save}>
      {#if part === 'limits'}
        <div class="limits-head">
          <h2 id="ad-{part}">{title}</h2>
          {#if changedCount}<span class="muted">{t('admin.limitsChanged', { count: changedCount })}</span>{/if}
        </div>
      {:else}
        <h2 id="ad-{part}">{title}</h2>
      {/if}
      {#if part === 'signin'}
        <div class="stack-sm">
          <label class="check"><input type="checkbox" bind:checked={s.openSignup} />{t('admin.openSignup')}</label>
          <label class="check"><input type="checkbox" bind:checked={s.requirePow} />{t('admin.requirePow')}</label>
          <label class="check"><input type="checkbox" bind:checked={s.inviteRegistration} />{t('admin.inviteRegistration')}</label>
          <label class="check"><input type="checkbox" bind:checked={s.securityAlerts} aria-describedby="sec-alerts-hint" />{t('admin.securityAlerts')}</label>
          <p class="hint" id="sec-alerts-hint">{t('admin.securityAlertsHint')}</p>
        </div>
        <p class="hint" data-testid="signin-location">{t('admin.signinLocation')}: {#if s.signinLocation === 'header'}{t('admin.signinLocation.header')}{:else if s.signinLocation === 'database'}{t('admin.signinLocation.database')}{:else if s.signinLocation === 'both'}{t('admin.signinLocation.both')}{:else}{t('admin.signinLocation.none')}{/if}</p>
        <Field label={t('admin.minPassword')} type="number" bind:value={minLen} min="8" max="128" required />
        <h3>{t('admin.sessions')}</h3>
        <Field label={t('admin.sessionIdle')} hint={t('admin.sessionIdleHint')} type="number" bind:value={idleDays} min="1" max="365" required />
        <Field label={t('admin.sessionMax')} hint={t('admin.sessionMaxHint')} type="number" bind:value={maxDays} min="1" max="730" required />
      {:else if part === 'connections'}
        <div class="stack-sm">
          <label class="check"><input type="checkbox" bind:checked={s.mcpEnabled} aria-describedby="mcp-hint" />{t('admin.mcpEnabled')}</label>
          <p class="hint" id="mcp-hint">{t('admin.mcpHint')}</p>
        </div>
        <div class="stack-sm">
          <label class="check"><input type="checkbox" bind:checked={s.oauthEnabled} disabled={!s.oauthPossible && !s.oauthEnabled} aria-describedby="oauth-hint" />{t('admin.oauthEnabled')}</label>
          <p class="hint" id="oauth-hint">{t('admin.oauthHint')} {s.oauthPossible ? '' : t('admin.oauthNeedsHttps')}</p>
        </div>
        <div class="stack-sm">
          <label class="check"><input type="checkbox" bind:checked={s.webhooksEnabled} aria-describedby="webhooks-hint" />{t('admin.webhooksEnabled')}</label>
          <p class="hint" id="webhooks-hint">{t('admin.webhooksHint')}</p>
        </div>
        <fieldset class="stack-sm" aria-describedby="kinds-hint">
          <legend class="label">{t('admin.kinds')}</legend>
          <p class="hint" id="kinds-hint">{t('admin.kindsCount', { n: s.enabledNotificationKinds.length, total: s.availableNotificationKinds.length })} {t('admin.kindsHint')}</p>
          <div class="kinds">
            {#each kinds as k (k)}
              <label><input type="checkbox" value={k} bind:group={s.enabledNotificationKinds} /><span>{serviceName(k)}</span></label>
            {/each}
          </div>
          <div><Button variant="quiet" {busy} onclick={resetKinds}>{t('admin.kindsReset')}</Button></div>
        </fieldset>
        <div class="stack-sm">
          <label class="check"><input type="checkbox" bind:checked={s.egressGuard} disabled={s.egressGuardLocked} aria-describedby="eg-hint" />{t('admin.egressGuard')}</label>
          {#if !s.egressGuard && !s.egressGuardLocked}
            <p class="warn" id="eg-hint">{t('admin.egressOffWarn')}</p>
          {:else}
            <p class="hint" id="eg-hint">{s.egressGuardLocked ? t('admin.egressLocked') : t('admin.egressHint')}</p>
          {/if}
        </div>
      {:else}
        <p class="muted">{t('admin.limitsHint')}</p>
        <div class="groups">
          {#each limitGroups as g (g.title)}
            <div class="group" role="group" aria-labelledby="lg-{g.title}">
              <h3 id="lg-{g.title}">{t(g.title)}</h3>
              {#each g.keys as k (k)}
                {@const changed = !isDefault(k)}
                <div class="limit">
                  <span class="what">
                    <label for="limit-{k}">{limitLabel(k)}</label>
                    {#if changed}
                      <span class="changed" id="limit-{k}-default">
                        {t('admin.limitDefault', { n: s.limitDefaults[k] ?? 0 })} ·
                        <button type="button" class="link" aria-label={t('admin.limitResetFor', { name: limitLabel(k) })} onclick={() => (limitInputs[k] = String(s!.limitDefaults[k] ?? 0))}>{t('admin.limitReset')}</button>
                      </span>
                    {/if}
                  </span>
                  <input id="limit-{k}" type="number" min="0" max="1000000" inputmode="numeric" bind:value={limitInputs[k]} aria-describedby={changed ? `limit-${k}-default` : undefined} />
                </div>
              {/each}
            </div>
          {/each}
        </div>
      {/if}
      {@render saveRow()}
    </form>
  {:else}
    <h2 id="ad-{part}">{title}</h2>
    <Alert message={err} />
    {#if !err}<p role="status" class="muted">{t('common.loading')}</p>{/if}
  {/if}
</section>
{/if}

<style>
  /* Notification services: an aligned grid, each box right before its own name. */
  .kinds {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr));
    gap: 0 12px;
  }
  .kinds label {
    display: flex;
    align-items: center;
    gap: 10px;
    min-block-size: var(--tap);
    cursor: pointer;
  }
  .kinds input {
    inline-size: 20px;
    block-size: 20px;
    margin: 0;
    flex: none;
  }
  .warn {
    margin: 0;
    padding-block: 8px;
    padding-inline: 12px;
    border-inline-start: 3px solid var(--danger);
    background: var(--surface-2);
    border-radius: var(--radius);
    font-size: 0.875rem;
  }
  .warn::before {
    content: '\26A0\FE0F\00a0';
  }
  .limits-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 4px 12px;
  }
  .limits-head h2 {
    flex: 1;
    margin: 0;
  }
  .groups {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(17rem, 1fr));
    gap: var(--space) 28px;
  }
  .group h3 {
    margin: 0;
    padding-block: 4px;
    font-size: 0.8125rem;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-2);
  }
  .limit {
    display: flex;
    align-items: center;
    gap: 12px;
    padding-block: 6px;
    border-block-start: 1px solid var(--border);
  }
  .what {
    flex: 1;
    display: grid;
    min-inline-size: 0;
    font-size: 0.9375rem;
  }
  .changed {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-block: -8px;
    font-size: 0.8125rem;
    color: var(--text-2);
  }
  .link {
    display: inline-flex;
    align-items: center;
    min-block-size: var(--tap);
    padding: 0;
    border: 0;
    background: none;
    color: var(--accent);
    font: inherit;
    font-weight: 600;
    text-decoration: underline;
    cursor: pointer;
  }
  .limit input {
    flex: none;
    inline-size: 5.5rem;
    padding-inline: 10px;
    text-align: end;
    font-variant-numeric: tabular-nums;
  }
  /* Compact on wide screens; full tap height on phones. */
  @media (min-width: 48rem) {
    .limit input {
      min-block-size: 36px;
      padding-block: 4px;
    }
    .limit {
      padding-block: 4px;
    }
  }
</style>
