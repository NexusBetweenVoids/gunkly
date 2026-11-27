<script lang="ts">
  import { locale, t } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import Audit from './Audit.svelte';
  import EmailCodes from './EmailCodes.svelte';
  import Passkeys from './Passkeys.svelte';
  import Password from './Password.svelte';
  import RecoveryCodesSection from './RecoveryCodesSection.svelte';
  import Sessions from './Sessions.svelte';
  import SigninAlerts from './SigninAlerts.svelte';
  import Sso from './Sso.svelte';
  import StatusLine from './StatusLine.svelte';
  import Totp from './Totp.svelte';

  // Settings, Sign-in & security: a one-line verdict, every way to sign in as one list (each opens
  // the same forms and checks as before), then alerts, devices and activity.
  const list = (xs: string[]) => new Intl.ListFormat(locale, { type: 'conjunction' }).format(xs);
  const verdict = $derived.by(() => {
    const u = app.user;
    if (!u) return null;
    const second = u.totpEnabled || u.emailOtpEnabled || u.passkeys > 0;
    if (u.hasPassword && !second) {
      if (app.instance.hostedMode) return { tone: 'warn', title: t('verdict.required'), detail: t('verdict.requiredDetail') } as const;
      return { tone: 'warn', title: u.ssoLinked ? t('verdict.passwordStill') : t('verdict.onlyPassword'), detail: t('verdict.addSecond') } as const;
    }
    const by: string[] = [];
    if (u.hasPassword) by.push(t('verdict.password'));
    if (u.passkeys > 0) by.push(t('verdict.passkeys', { count: u.passkeys }));
    if (u.ssoLinked) by.push(t('verdict.sso'));
    if (u.totpEnabled) by.push(t('verdict.authenticator'));
    if (u.emailOtpEnabled) by.push(t('verdict.emailCodes'));
    if (!by.length) return null;
    let detail = '';
    if (!u.hasPassword && u.ssoLinked && u.passkeys === 0) detail = t('verdict.ssoDetail');
    else if (u.emailOtpEnabled && !u.totpEnabled && u.passkeys === 0) detail = t('verdict.emailDetail');
    return { tone: 'ok', title: t('verdict.protectedBy', { list: list(by) }), detail } as const;
  });
</script>

{#if verdict}
  <StatusLine
    tone={verdict.tone}
    icon={verdict.tone === 'ok' ? 'shieldCheck' : 'shieldAlert'}
    title={verdict.title}
    detail={verdict.detail}
    testid="security-verdict"
  />
{/if}
<section class="card" aria-labelledby="a-ways">
  <h2 id="a-ways" class="card-title">{t('ways.signInTitle')}</h2>
  <div class="methods">
    <Password />
    <Passkeys />
    {#if app.instance.sso}<Sso />{/if}
    <Totp />
    <EmailCodes />
    <RecoveryCodesSection />
  </div>
</section>
<SigninAlerts />
<Sessions />
<Audit />

<style>
  .methods > :global(.method:first-child) {
    border-block-start: 0;
  }
  .methods {
    margin-block-start: 4px;
  }
</style>
