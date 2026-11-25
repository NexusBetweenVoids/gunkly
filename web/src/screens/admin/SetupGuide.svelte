<script lang="ts">
  // The guided setup after the first admin account: one optional step at a time, each the same card as
  // in Administration, so everything set here can be changed there later.
  import Button from '../../components/Button.svelte';
  import { t, type Key } from '../../lib/i18n';
  import { navigate } from '../../lib/router.svelte';
  import { app } from '../../lib/session.svelte';
  import Ai from './Ai.svelte';
  import ServiceCards from './ServiceCards.svelte';
  import Updates from './Updates.svelte';

  type Step = { id: string; title: Key; intro: Key };
  const steps: Step[] = [
    { id: 'address', title: 'guide.addressTitle', intro: 'guide.addressIntro' },
    { id: 'email', title: 'guide.emailTitle', intro: 'guide.emailIntro' },
    { id: 'signin', title: 'guide.signinTitle', intro: 'guide.signinIntro' },
    { id: 'notify', title: 'guide.notifyTitle', intro: 'guide.notifyIntro' },
    { id: 'ai', title: 'guide.aiTitle', intro: 'guide.aiIntro' },
    { id: 'updates', title: 'guide.updatesTitle', intro: 'guide.updatesIntro' },
    { id: 'done', title: 'guide.doneTitle', intro: 'guide.doneIntro' },
  ];
  const fromUrl = Number(new URLSearchParams(location.search).get('step'));
  let i = $state(Number.isInteger(fromUrl) && fromUrl >= 0 && fromUrl < steps.length ? fromUrl : 0);
  const step = $derived(steps[i]);

  function go(n: number): void {
    i = Math.max(0, Math.min(steps.length - 1, n));
    history.replaceState(history.state, '', `/setup/guide?step=${i}`);
    document.getElementById('guide-title')?.focus();
  }

  // The address check: GUNKLY_BASE_URL stays in the environment (links, cookies and passkeys depend on it).
  const base = app.instance.baseUrl ?? '';
  const here = location.origin;
  const addr = $derived.by((): { kind: 'ok' | 'warn' | 'bad'; text: Key } => {
    if (!base) return { kind: 'bad', text: 'guide.addressMissing' };
    if (base.replace(/\/$/, '') !== here) return { kind: 'warn', text: 'guide.addressMismatch' };
    if (!base.startsWith('https://') && !/^http:\/\/(localhost|127\.0\.0\.1|\[::1\])/.test(base)) return { kind: 'warn', text: 'guide.addressHttp' };
    return { kind: 'ok', text: 'guide.addressOk' };
  });
</script>

<section class="card narrow guide" aria-labelledby="guide-title">
  <p class="muted">{t('guide.progress', { n: i + 1, total: steps.length })}</p>
  <h1 id="guide-title" tabindex="-1">{t(step.title)}</h1>
  <p>{t(step.intro)}</p>
  <ol class="dots" aria-hidden="true">
    {#each steps as s, n (s.id)}<li class:on={n <= i}></li>{/each}
  </ol>
</section>

<div class="narrow stack body">
  {#if step.id === 'address'}
    <section class="card">
      <dl class="facts">
        <dt>{t('guide.addressConfigured')}</dt>
        <dd class="mono">{base || '—'}</dd>
        <dt>{t('guide.addressBrowser')}</dt>
        <dd class="mono">{here}</dd>
      </dl>
      <p class={addr.kind === 'ok' ? '' : 'error-text'} data-testid="address-check">{t(addr.text, { base, here })}</p>
      <p class="hint">{t('guide.addressWhy')}</p>
    </section>
  {:else if step.id === 'email'}
    <ServiceCards which="email" />
  {:else if step.id === 'signin'}
    <ServiceCards which="sso" />
    <p class="hint">{t('guide.signinMore')} <a href="/admin/signin">{t('guide.toAdmin')}</a></p>
  {:else if step.id === 'notify'}
    <section class="card">
      <ul class="stack-sm">
        <li>{t('guide.notifyPush')}</li>
        <li>{t('guide.notifyApps')}</li>
        <li>{t('guide.notifyHousehold')}</li>
      </ul>
      <p class="hint">{t('guide.notifyAdmin')} <a href="/admin/connections">{t('guide.toConnections')}</a></p>
    </section>
  {:else if step.id === 'ai'}
    <Ai />
  {:else if step.id === 'updates'}
    <Updates />
    <ServiceCards which="updatehook" />
  {:else}
    <section class="card">
      <ul class="stack-sm">
        <li><a href="/households/new">{t('guide.doneHousehold')}</a></li>
        <li><a href="/admin/users">{t('guide.doneUsers')}</a></li>
        <li><a href="/admin">{t('guide.doneAdmin')}</a></li>
      </ul>
    </section>
  {/if}

  <div class="nav">
    {#if i > 0}<Button variant="quiet" onclick={() => go(i - 1)}>{t('guide.back')}</Button>{/if}
    {#if step.id === 'done'}
      <Button onclick={() => navigate(app.households.length ? '/' : '/households/new')}>{t('guide.finish')}</Button>
    {:else}
      <Button variant="quiet" onclick={() => go(steps.length - 1)}>{t('guide.skipAll')}</Button>
      <Button onclick={() => go(i + 1)}>{t('guide.next')}</Button>
    {/if}
  </div>
</div>

<style>
  .guide h1:focus {
    outline: none;
  }
  .dots {
    display: flex;
    gap: 6px;
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .dots li {
    flex: 1;
    block-size: 4px;
    border-radius: 2px;
    background: var(--surface-2);
  }
  .dots li.on {
    background: var(--accent);
  }
  .body {
    margin-block-start: var(--space);
  }
  .nav {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    justify-content: flex-end;
  }
</style>
