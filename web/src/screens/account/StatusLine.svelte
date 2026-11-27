<script lang="ts">
  import Icon, { type IconName } from '../../components/Icon.svelte';

  // The one-line answer at the top of a settings page ("how is my account protected?", "will I get
  // reminders?"). The wording carries the meaning; the colour and icon only repeat it.
  let { tone, icon, title, detail = '', testid }: { tone: 'ok' | 'warn'; icon: IconName; title: string; detail?: string; testid?: string } =
    $props();
</script>

<div class="statusline {tone}" data-testid={testid}>
  <span class="icon"><Icon name={icon} size={22} /></span>
  <div class="text">
    <strong>{title}</strong>
    {#if detail}<span class="detail">{detail}</span>{/if}
  </div>
</div>

<style>
  .statusline {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding-block: 14px;
    padding-inline: 16px;
    border-radius: var(--radius-lg);
    color: var(--text);
  }
  .ok {
    background: var(--ok-bg);
  }
  .warn {
    background: var(--warn-bg);
  }
  .icon {
    display: flex;
    flex: none;
    margin-block-start: 1px;
  }
  .ok .icon {
    color: var(--ok);
  }
  .warn .icon {
    color: var(--warn);
  }
  .text {
    display: grid;
    gap: 2px;
  }
  .detail {
    font-size: 0.9375rem;
    color: var(--text-2);
  }
</style>
