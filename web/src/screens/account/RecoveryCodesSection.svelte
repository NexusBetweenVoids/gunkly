<script lang="ts">
  // How many recovery codes are left, and a way to replace them (shown once). Codes are first handed
  // out with the first second factor (on the hosted service: when the email address is confirmed),
  // which is easy to miss, so this is where to get a fresh set.
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import RecoveryCodes from '../../components/RecoveryCodes.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  let info = $state<{ remaining: number; available: boolean } | null>(null);
  let codes = $state<string[]>([]);
  let confirming = $state(false);
  let busy = $state(false);
  let err = $state('');

  async function load(): Promise<void> {
    try {
      info = await api<{ remaining: number; available: boolean }>('GET', '/account/recovery-codes');
    } catch (x) {
      err = failure(x).message;
    }
  }
  // Reload when the second factors change (a passkey or an app added elsewhere on this page).
  $effect(() => {
    void [app.user?.totpEnabled, app.user?.emailOtpEnabled, app.user?.passkeys];
    void load();
  });

  async function renew(): Promise<void> {
    busy = true;
    err = '';
    try {
      const r = await withStepUp(() => api<{ recoveryCodes: string[] }>('POST', '/account/recovery-codes'));
      codes = r.recoveryCodes;
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
      confirming = false;
    }
  }

  function saved(): void {
    codes = [];
    toast(t('recovery.renewed'));
    void load();
  }
</script>

<section class="card" aria-labelledby="a-recovery">
  <h2 id="a-recovery">{t('recovery.title')}</h2>
  {#if codes.length}
    <RecoveryCodes {codes} intro={t('recovery.newIntro')} onsaved={saved} />
  {:else if info}
    <div class="stack">
      {#if !info.available}
        <p class="muted">{t('recovery.none')}</p>
      {:else}
        <p class="muted">{t('recovery.intro')}</p>
        <p>
          <strong>{t('recovery.left', { count: info.remaining })}</strong>
          {#if info.remaining <= 3}<span class="badge">{t('recovery.low')}</span>{/if}
        </p>
        <Alert message={err} />
        <div><Button variant={info.remaining <= 3 ? 'primary' : 'quiet'} {busy} onclick={() => (confirming = true)}>{t('recovery.renew')}</Button></div>
      {/if}
    </div>
  {:else}
    <Alert message={err} />
  {/if}
</section>

<Confirm
  open={confirming}
  title={t('recovery.renewTitle')}
  message={t('recovery.renewWarn')}
  confirmLabel={t('recovery.renew')}
  {busy}
  onconfirm={renew}
  oncancel={() => (confirming = false)}
/>
