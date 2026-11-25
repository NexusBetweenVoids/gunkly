<script lang="ts">
  // Email, single sign-on, metrics and sign-up proof of work, set in Administration instead of the
  // server's environment (an environment variable still wins and locks its field).
  import Button from '../../components/Button.svelte';
  import Field from '../../components/Field.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { t, type Key } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import ConfigCard, { type FieldSpec } from './ConfigCard.svelte';

  let { which }: { which: 'email' | 'sso' | 'metrics' | 'pow' | 'updatehook' } = $props();

  type Status = { emailOn: boolean; emailProblem: string; ssoOn: boolean; ssoProblem: string; ssoAvailable: boolean; ssoCallback: string; metricsOn: boolean };
  let st = $state<Status | null>(null);
  let testTo = $state(app.user?.email ?? '');
  let testing = $state(false);
  let result = $state<{ ok: boolean; text: string } | null>(null);

  async function status(): Promise<void> {
    try {
      st = await api<Status>('GET', '/admin/services');
    } catch {
      st = null;
    }
  }
  void status();

  async function saved(): Promise<void> {
    result = null;
    await status();
    try {
      app.instance = await api('GET', '/instance'); // sign-in screen and email features follow at once
    } catch {
      // caught up on the next page load
    }
  }

  async function testEmail(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    testing = true;
    result = null;
    try {
      const r = await api<{ ok: boolean }>('POST', '/admin/email/test', { to: testTo.trim() });
      result = { ok: r.ok, text: r.ok ? t('svcAdmin.emailTestOk', { to: testTo.trim() }) : t('svcAdmin.emailTestFail') };
    } catch (x) {
      result = { ok: false, text: failure(x).message };
    } finally {
      testing = false;
    }
  }

  async function testSSO(): Promise<void> {
    testing = true;
    result = null;
    try {
      const r = await api<{ ok: boolean; authOrigin: string }>('POST', '/admin/sso/test');
      result = { ok: r.ok, text: r.ok ? t('svcAdmin.ssoTestOk', { origin: r.authOrigin }) : t('svcAdmin.ssoTestFail') };
    } catch (x) {
      result = { ok: false, text: failure(x).message };
    } finally {
      testing = false;
    }
  }

  const PROBLEM: Record<string, Key> = { incomplete: 'svcAdmin.incomplete', needs_https: 'svcAdmin.needsHttps' };
  const ssoDocs = 'https://github.com/NexusBetweenVoids/gunkly/blob/main/docs/sso.md';

  const emailFields: FieldSpec[] = [
    { key: 'email.enabled', label: t('svcAdmin.emailEnable'), kind: 'bool', hint: t('svcAdmin.emailEnableHint') },
    { key: 'email.host', label: t('svcAdmin.smtpHost'), placeholder: 'smtp.example.com' },
    { key: 'email.port', label: t('svcAdmin.smtpPort'), hint: t('svcAdmin.smtpPortHint'), placeholder: '587' },
    {
      key: 'email.tls', label: t('svcAdmin.smtpTls'), kind: 'select',
      options: [{ value: 'starttls', label: t('svcAdmin.tlsStarttls') }, { value: 'tls', label: t('svcAdmin.tlsTls') }, { value: 'none', label: t('svcAdmin.tlsNone') }],
    },
    { key: 'email.username', label: t('svcAdmin.smtpUser') },
    { key: 'email.password', label: t('svcAdmin.smtpPassword'), secret: true, hint: t('svcAdmin.secretHint') },
    { key: 'email.from', label: t('svcAdmin.smtpFrom'), hint: t('svcAdmin.smtpFromHint'), placeholder: 'Gunkly <gunkly@example.com>' },
  ];
  const ssoFields: FieldSpec[] = [
    { key: 'sso.issuer', label: t('admin.ssoIssuer'), hint: t('svcAdmin.issuerHint'), placeholder: 'https://auth.example.com' },
    { key: 'sso.client_id', label: t('admin.ssoClientId'), placeholder: 'gunkly' },
    { key: 'sso.client_secret', label: t('svcAdmin.clientSecret'), secret: true, hint: t('svcAdmin.secretHint') },
    { key: 'sso.scopes', label: t('admin.ssoScopes'), hint: t('svcAdmin.scopesHint'), placeholder: 'openid profile email' },
  ];
  const metricsFields: FieldSpec[] = [{ key: 'metrics.token', label: t('svcAdmin.metricsToken'), secret: true, hint: t('svcAdmin.metricsTokenHint') }];
  const hookFields: FieldSpec[] = [
    { key: 'updates.webhook_url', label: t('svcAdmin.hookUrl'), hint: t('svcAdmin.hookUrlHint'), placeholder: 'http://watchtower:8080/v1/update' },
    { key: 'updates.webhook_token', label: t('svcAdmin.hookToken'), secret: true, hint: t('svcAdmin.hookTokenHint') },
  ];
  const updateDocs = 'https://github.com/NexusBetweenVoids/gunkly/blob/main/docs/updating.md';
  const powFields: FieldSpec[] = [{ key: 'signup.pow_max', label: t('svcAdmin.powMax'), hint: t('svcAdmin.powMaxHint'), placeholder: '250000' }];
</script>

{#if which === 'email'}
  <ConfigCard id="email" title={t('svcAdmin.emailTitle')} fields={emailFields} onsaved={saved}>
    {#snippet intro()}
      <p>{t('svcAdmin.emailIntro')}</p>
      {#if st}
        <p data-testid="email-status">
          <span class="badge">{st.emailOn ? t('admin.ssoOn') : t('admin.ssoOff')}</span>
          {#if st.emailProblem}<span class="error-text">{t(PROBLEM[st.emailProblem] ?? 'svcAdmin.incomplete')}</span>{/if}
        </p>
      {/if}
    {/snippet}
    {#snippet after()}
      {#if st?.emailOn}
        <form class="stack-sm test" onsubmit={testEmail}>
          <Field label={t('svcAdmin.emailTestTo')} type="email" bind:value={testTo} required />
          <div><Button type="submit" variant="quiet" busy={testing}>{t('svcAdmin.emailTest')}</Button></div>
        </form>
      {/if}
      {#if result}<p role="status" class={result.ok ? '' : 'error-text'}>{result.text}</p>{/if}
    {/snippet}
  </ConfigCard>
{:else if which === 'sso'}
  {#if st && !st.ssoAvailable}
    <section class="card"><h2>{t('admin.ssoTitle')}</h2><p class="muted" data-testid="sso-status">{t('admin.ssoHosted')}</p></section>
  {:else}
    <ConfigCard id="sso" title={t('admin.ssoTitle')} fields={ssoFields} onsaved={saved}>
      {#snippet intro()}
        <p>{t('svcAdmin.ssoIntro')} <a href={ssoDocs} rel="noopener noreferrer" target="_blank">{t('admin.ssoDocs')}</a></p>
        {#if st}
          <p data-testid="sso-status">
            <span class="badge">{st.ssoOn ? t('admin.ssoOn') : t('admin.ssoOff')}</span>
            {#if st.ssoProblem}<span class="error-text">{t(PROBLEM[st.ssoProblem] ?? 'svcAdmin.incomplete')}</span>{/if}
          </p>
          <p class="hint">{t('admin.ssoStep1')}</p>
          <pre class="code">{st.ssoCallback}</pre>
        {/if}
      {/snippet}
      {#snippet after()}
        {#if st?.ssoOn}<div><Button variant="quiet" busy={testing} onclick={testSSO}>{t('svcAdmin.ssoTest')}</Button></div>{/if}
        {#if result}<p role="status" class={result.ok ? '' : 'error-text'}>{result.text}</p>{/if}
      {/snippet}
    </ConfigCard>
  {/if}
{:else if which === 'metrics'}
  <ConfigCard id="metrics" title={t('svcAdmin.metricsTitle')} fields={metricsFields} onsaved={saved}>
    {#snippet intro()}<p class="hint">{t('svcAdmin.metricsIntro')}</p>{/snippet}
  </ConfigCard>
{:else if which === 'updatehook'}
  <ConfigCard id="updatehook" title={t('svcAdmin.hookTitle')} fields={hookFields} onsaved={saved}>
    {#snippet intro()}
      <p>{t('svcAdmin.hookIntro')}</p>
      <p class="hint">{t('svcAdmin.hookWhy')} <a href={updateDocs} rel="noopener noreferrer" target="_blank">{t('svcAdmin.hookDocs')}</a></p>
    {/snippet}
  </ConfigCard>
{:else}
  <ConfigCard id="pow" title={t('svcAdmin.powTitle')} fields={powFields} onsaved={saved} />
{/if}

<style>
  .test {
    margin-block-start: var(--space);
  }
</style>
