<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Field from '../../components/Field.svelte';
  import RecoveryCodes from '../../components/RecoveryCodes.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, t } from '../../lib/i18n';
  import { app, refreshMe } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  type Passkey = { id: string; name: string; createdAt: string; lastUsedAt: string | null };
  let list = $state<Passkey[]>([]);
  let adding = $state(false);
  let name = $state('');
  let codes = $state<string[]>([]);
  let busy = $state(false);
  let err = $state({ code: '', message: '' });
  let removing = $state<Passkey | null>(null);
  let supported = $state(true);
  const when = (s: string) => formatDate(new Date(s), { dateStyle: 'medium' });

  async function load(): Promise<void> {
    try {
      list = await api<Passkey[]>('GET', '/account/passkeys');
    } catch (x) {
      err = failure(x);
    }
  }
  void load();
  void import('../../lib/passkey').then((m) => (supported = m.passkeysSupported()));

  async function add(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    err = { code: '', message: '' };
    try {
      const { createPasskey } = await import('../../lib/passkey');
      const o = await withStepUp(() => api<{ challenge: string; options: { publicKey: Record<string, unknown> } }>('POST', '/account/passkeys/options'));
      const credential = await createPasskey(o.options);
      const r = await withStepUp(() =>
        api<{ recoveryCodes?: string[] }>('POST', '/account/passkeys', { challenge: o.challenge, credential, name: name.trim() }),
      );
      codes = r.recoveryCodes ?? [];
      adding = false;
      name = '';
      toast(codes.length ? t('passkeys.added') : t('passkeys.addedKeepCodes'));
      await Promise.all([load(), refreshMe()]);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
    }
  }

  async function remove(): Promise<void> {
    const p = removing;
    if (!p) return;
    busy = true;
    err = { code: '', message: '' };
    try {
      await withStepUp(() => api<void>('DELETE', `/account/passkeys/${p.id}`));
      toast(t('passkeys.removed'));
      await Promise.all([load(), refreshMe()]);
    } catch (x) {
      err = failure(x);
    } finally {
      busy = false;
      removing = null;
    }
  }
</script>

<section class="card" aria-labelledby="a-passkeys">
  <h2 id="a-passkeys">{t('passkeys.title')}</h2>
  {#if codes.length}
    <RecoveryCodes {codes} intro={t('passkeys.codesIntro')} onsaved={() => (codes = [])} />
  {:else}
    <div class="stack">
      <p class="muted">{t('passkeys.intro')}</p>
      {#if !app.instance.passkeysAvailable}
        <p class="muted" role="note">{t('passkeys.needsHttps')}</p>
      {:else if !supported}
        <p class="muted" role="note">{t('passkeys.unsupported')}</p>
      {/if}
      {#if list.length}
        <ul class="list">
          {#each list as p (p.id)}
            <li>
              <span>
                <strong>{p.name}</strong><br />
                <span class="muted"
                  >{t('passkeys.created', { date: when(p.createdAt) })} · {p.lastUsedAt
                    ? t('passkeys.lastUsed', { date: when(p.lastUsedAt) })
                    : t('passkeys.neverUsed')}</span
                >
              </span>
              <Button variant="quiet" onclick={() => (removing = p)}>{t('common.delete')}</Button>
            </li>
          {/each}
        </ul>
        <p class="muted">{t('passkeys.secondFactorNote')}</p>
      {/if}
      {#if app.instance.hostedMode && list.length === 1 && !app.user?.totpEnabled && !app.user?.emailOtpEnabled}
        <p class="muted">{t('totp.mandatory')}</p>
      {/if}
      <Alert message={err.message} />
      {#if adding}
        <form class="stack" onsubmit={add}>
          <Field label={t('passkeys.name')} hint={t('passkeys.nameHint')} bind:value={name} maxlength={100} required autocomplete="off" />
          <div class="row">
            <Button type="submit" {busy}>{t('passkeys.create')}</Button>
            <Button variant="quiet" onclick={() => (adding = false)}>{t('common.cancel')}</Button>
          </div>
        </form>
      {:else}
        <div>
          <Button disabled={!app.instance.passkeysAvailable || !supported} onclick={() => (adding = true)}>{t('passkeys.add')}</Button>
        </div>
      {/if}
    </div>
  {/if}
</section>

<Confirm
  open={removing !== null}
  title={t('passkeys.removeTitle')}
  message={t('passkeys.removeWarn', { name: removing?.name ?? '' })}
  confirmLabel={t('common.delete')}
  {busy}
  onconfirm={remove}
  oncancel={() => (removing = null)}
/>
