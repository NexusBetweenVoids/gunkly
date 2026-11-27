<script lang="ts">
  import { href } from '../lib/base';
  import { qrEncode, qrLogoSide, qrPath } from '../lib/qr';

  let { text, label }: { text: string; label: string } = $props();

  const quiet = 4;
  // Level H leaves room for the logo; too-long text simply renders nothing (the secret is shown too).
  const qr = $derived.by(() => {
    try {
      const m = qrEncode(text, 'H');
      const side = qrLogoSide(m.length);
      return { ...qrPath(m, quiet), logo: { at: quiet + (m.length - side) / 2, side } };
    } catch {
      return null;
    }
  });
</script>

{#if qr}
  <!-- Colours are fixed on purpose: scanners need dark modules on a light ground in every theme. -->
  <svg class="qr" viewBox="0 0 {qr.size} {qr.size}" width="200" height="200" role="img" aria-label={label} shape-rendering="crispEdges">
    <rect width={qr.size} height={qr.size} fill="#fff" />
    <path d={qr.d} fill="#000" />
    {#if qr.logo.side}
      <rect x={qr.logo.at} y={qr.logo.at} width={qr.logo.side} height={qr.logo.side} rx="1.5" fill="#fff" />
      <image href={href('/icons/mark-light.png')} x={qr.logo.at + 1} y={qr.logo.at + 1} width={qr.logo.side - 2} height={qr.logo.side - 2} />
    {/if}
  </svg>
{/if}

<style>
  .qr {
    max-inline-size: 100%;
    block-size: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
  }
</style>
