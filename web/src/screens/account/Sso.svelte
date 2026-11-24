<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, t } from '../../lib/i18n';
  import { app, refreshMe } from '../../lib/session.svelte';
  import { withStepUp } from '../../lib/stepup.svelte';
  import { toast } from '../../lib/toast.svelte';

  type Status = { name: string; linked: boolean; linkedAt: string | null; canUnlink: boolean };
  let st = $state<Status | null>(null);
  let busy = $state(false);
  let err = $state('');
  let confirmOff = $state(false);

  async function load(): Promise<void> {
    try {
      st = await api<Status>('GET', '/account/sso');
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  // The provider takes over the browser and brings it back to this page with the result.
  async function link(): Promise<void> {
    busy = true;
    err = '';
    try {
      const r = await withStepUp(() => api<{ url: string }>('POST', '/account/sso/link'));
      location.assign(r.url);
    } catch (x) {
      err = failure(x).message;
      busy = false;
    }
  }

  async function unlink(): Promise<void> {
    busy = true;
    err = '';
    try {
      await withStepUp(() => api<void>('DELETE', '/account/sso'));
      toast(t('sso.unlinked'));
      await Promise.all([load(), refreshMe()]);
    } catch (x) {
      err = failure(x).message;
    } finally {
      busy = false;
      confirmOff = false;
    }
  }
</script>

<section class="card" aria-labelledby="a-sso">
  <h2 id="a-sso">{t('sso.title')}</h2>
  <div class="stack">
    <p class="muted">{app.instance.sso?.name ? t('sso.intro', { name: app.instance.sso.name }) : t('sso.introGeneric')}</p>
    {#if st}
      <p>
        <span class="badge">{st.linked ? t('sso.linkedOn', { date: formatDate(new Date(st.linkedAt ?? ''), { dateStyle: 'medium' }) }) : t('sso.notLinked')}</span>
      </p>
      {#if st.linked && !st.canUnlink}<p class="muted">{t('sso.needPassword')}</p>{/if}
    {/if}
    <Alert message={err} />
    {#if st}
      <div>
        {#if st.linked}
          <Button variant="danger" disabled={!st.canUnlink} onclick={() => (confirmOff = true)}>{t('sso.unlink')}</Button>
        {:else}
          <Button {busy} onclick={link}>{t('sso.link')}</Button>
        {/if}
      </div>
    {/if}
  </div>
</section>

<Confirm
  open={confirmOff}
  title={t('sso.unlinkTitle')}
  message={t('sso.unlinkWarn')}
  confirmLabel={t('sso.unlink')}
  {busy}
  onconfirm={unlink}
  oncancel={() => (confirmOff = false)}
/>
