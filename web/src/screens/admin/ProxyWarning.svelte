<script lang="ts">
  import Button from '../../components/Button.svelte';
  import { api } from '../../lib/api';
  import { copyText } from '../../lib/clip';
  import { t } from '../../lib/i18n';

  type Peer = { ip: string; lastSeen: string };
  let peers = $state<Peer[]>([]);

  // A failure here only means no hint: the rest of the page works without it.
  api<{ untrustedProxies: Peer[] }>('GET', '/admin/health')
    .then((h) => (peers = h.untrustedProxies))
    .catch(() => {});

  const setting = $derived(`GUNKLY_TRUSTED_PROXIES=${peers.map((p) => `${p.ip}/${p.ip.includes(':') ? 128 : 32}`).join(',')}`);
</script>

{#if peers.length}
  <section class="proxy" role="alert" aria-labelledby="proxy-title">
    <h2 id="proxy-title">{t('proxy.title')}</h2>
    <p>{t('proxy.body', { peers: peers.map((p) => p.ip).join(', ') })}</p>
    <p>{t('proxy.fix')}</p>
    <p><code data-testid="proxy-setting">{setting}</code></p>
    <p><Button variant="quiet" onclick={() => copyText(setting)}>{t('proxy.copy')}</Button></p>
    <p class="hint">{t('proxy.shared')}</p>
    <p><a href="https://github.com/NexusBetweenVoids/gunkly#behind-a-reverse-proxy" rel="noopener noreferrer" target="_blank">{t('proxy.docs')}</a></p>
  </section>
{/if}

<style>
  .proxy {
    border: 2px solid var(--danger, #b3261e);
    border-radius: 0.5rem;
    padding: 1rem;
    margin-block-end: 1.5rem;
  }
  .proxy h2 {
    margin-block-start: 0;
  }
  code {
    overflow-wrap: anywhere;
    user-select: all;
  }
</style>
