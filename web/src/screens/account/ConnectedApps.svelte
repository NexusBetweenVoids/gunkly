<script lang="ts">
  import Alert from '../../components/Alert.svelte';
  import Button from '../../components/Button.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import { api } from '../../lib/api';
  import { failure } from '../../lib/errors';
  import { formatDate, t } from '../../lib/i18n';
  import { app } from '../../lib/session.svelte';
  import { toast } from '../../lib/toast.svelte';

  type Grant = { id: string; name: string; scopes: string[]; households: { id: string; name: string }[]; createdAt: string; lastUsedAt: string | null };
  const when = (s: string) => formatDate(new Date(s), { dateStyle: 'medium' });
  const short = { 'chores:read': 'tokens.shortRead', 'chores:complete': 'tokens.shortComplete', 'chores:write': 'tokens.shortWrite' } as const;

  let grants = $state<Grant[]>([]);
  let err = $state('');
  let leaving = $state<Grant | null>(null);

  async function load(): Promise<void> {
    try {
      grants = await api<Grant[]>('GET', '/account/oauth-grants');
    } catch (x) {
      err = failure(x).message;
    }
  }
  void load();

  async function disconnect(): Promise<void> {
    try {
      await api('DELETE', `/account/oauth-grants/${leaving!.id}`);
      toast(t('apps.disconnected'));
    } catch (x) {
      err = failure(x).message;
    }
    leaving = null;
    await load();
  }
</script>

{#if app.instance.oauthEnabled || grants.length}
  <section class="card" aria-labelledby="a-apps">
    <h2 id="a-apps">{t('apps.title')}</h2>
    <p class="muted">{t('apps.intro')}</p>
    <div aria-live="polite"><Alert message={err} /></div>
    {#if grants.length === 0}
      <p class="muted">{t('apps.none')}</p>
    {:else}
      <ul class="list">
        {#each grants as g (g.id)}
          <li>
            <span>
              <strong>{g.name}</strong>
              {#each g.scopes as s (s)}<span class="badge">{t(short[s as keyof typeof short] ?? 'tokens.shortRead')}</span>{' '}{/each}
              <br /><span class="muted">{g.households.map((h) => h.name).join(', ')}</span>
              <br /><span class="muted">{t('tokens.created', { date: when(g.createdAt) })} · {g.lastUsedAt ? t('tokens.lastUsed', { date: when(g.lastUsedAt) }) : t('tokens.neverUsed')}</span>
            </span>
            <Button variant="danger" onclick={() => (leaving = g)}>{t('apps.disconnect')}</Button>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}

<Confirm
  open={!!leaving}
  title={t('apps.disconnectTitle', { name: leaving?.name ?? '' })}
  message={t('apps.disconnectWarn')}
  confirmLabel={t('apps.disconnect')}
  onconfirm={disconnect}
  oncancel={() => (leaving = null)}
/>
