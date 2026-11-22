<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { has, t, type Key } from '../../lib/i18n';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  type S = {
    openSignup: boolean;
    requirePow: boolean;
    inviteRegistration: boolean;
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
    inactiveEnabled: boolean;
    inactiveWarnAfterDays: number;
    inactiveDeleteAfterDays: number;
  };
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
    for (const [k, v] of Object.entries(limitInputs)) if (Number(v) !== s.limits[k]) limits[k] = Number(v);
    try {
      adopt(
        await withStepUp(() =>
          api<S>('PATCH', '/admin/settings', {
            openSignup: s!.openSignup,
            requirePow: s!.requirePow,
            inviteRegistration: s!.inviteRegistration,
            mcpEnabled: s!.mcpEnabled,
            oauthEnabled: s!.oauthEnabled,
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

<section class="card" aria-labelledby="ad-settings">
  <h2 id="ad-settings">{t('admin.settings')}</h2>
  {#if s}
    <form class="stack" onsubmit={save}>
      <label class="check"><input type="checkbox" bind:checked={s.openSignup} />{t('admin.openSignup')}</label>
      <label class="check"><input type="checkbox" bind:checked={s.requirePow} />{t('admin.requirePow')}</label>
      <label class="check"><input type="checkbox" bind:checked={s.inviteRegistration} />{t('admin.inviteRegistration')}</label>
      <div class="stack-sm">
        <label class="check"><input type="checkbox" bind:checked={s.mcpEnabled} aria-describedby="mcp-hint" />{t('admin.mcpEnabled')}</label>
        <p class="hint" id="mcp-hint">{t('admin.mcpHint')}</p>
      </div>
      <div class="stack-sm">
        <label class="check"><input type="checkbox" bind:checked={s.oauthEnabled} disabled={!s.oauthPossible && !s.oauthEnabled} aria-describedby="oauth-hint" />{t('admin.oauthEnabled')}</label>
        <p class="hint" id="oauth-hint">{t('admin.oauthHint')} {s.oauthPossible ? '' : t('admin.oauthNeedsHttps')}</p>
      </div>
      <Field label={t('admin.minPassword')} type="number" bind:value={minLen} min="8" max="128" required />
      <h3>{t('admin.sessions')}</h3>
      <Field label={t('admin.sessionIdle')} hint={t('admin.sessionIdleHint')} type="number" bind:value={idleDays} min="1" max="365" required />
      <Field label={t('admin.sessionMax')} hint={t('admin.sessionMaxHint')} type="number" bind:value={maxDays} min="1" max="730" required />
      <h3>{t('admin.retention')}</h3>
      <p class="hint">{t('admin.retentionHint')}</p>
      <Field label={t('admin.auditRetention')} type="number" bind:value={auditDays} min="1" max="3650" required />
      <Field label={t('admin.deliveryRetention')} type="number" bind:value={deliveryDays} min="1" max="3650" required />
      <h3>{t('admin.inactive')}</h3>
      <p class="hint">{t('admin.inactiveHint')}</p>
      <label class="check"><input type="checkbox" bind:checked={s.inactiveEnabled} />{t('admin.inactiveEnabled')}</label>
      <Field label={t('admin.inactiveWarn')} type="number" bind:value={inactiveWarn} min="30" max="3650" required />
      <Field label={t('admin.inactiveDelete')} hint={t('admin.inactiveDeleteHint')} type="number" bind:value={inactiveDelete} min="60" max="3650" required />
      <fieldset class="stack-sm">
        <legend class="label">{t('admin.kinds')}</legend>
        <p class="hint">{t('admin.kindsHint')}</p>
        <div class="row">
          {#each s.availableNotificationKinds as k (k)}
            <label class="check"><input type="checkbox" value={k} bind:group={s.enabledNotificationKinds} />{k}</label>
          {/each}
        </div>
        <div><Button variant="quiet" {busy} onclick={resetKinds}>{t('admin.kindsReset')}</Button></div>
      </fieldset>
      <div class="stack-sm">
        <label class="check"><input type="checkbox" bind:checked={s.egressGuard} disabled={s.egressGuardLocked} aria-describedby="eg-hint" />{t('admin.egressGuard')}</label>
        <p class="hint" id="eg-hint">{s.egressGuardLocked ? t('admin.egressLocked') : t('admin.egressHint')}</p>
      </div>
      <h3>{t('admin.limits')}</h3>
      <p class="hint">{t('admin.limitsHint')}</p>
      {#each Object.keys(s.limits) as k (k)}
        <Field
          label={has(`limit.${k}`) ? t(`limit.${k}` as Key) : k.replace(/_/g, ' ')}
          hint={t('admin.limitDefault', { n: s.limitDefaults[k] ?? 0 })}
          type="number"
          min="0"
          max="1000000"
          bind:value={limitInputs[k]}
        />
      {/each}
      <Alert message={err} />
      <div><Button type="submit" {busy}>{t('common.save')}</Button></div>
    </form>
  {:else}
    <Alert message={err} />
    {#if !err}<p role="status" class="muted">{t('common.loading')}</p>{/if}
  {/if}
</section>
