<script lang="ts">
  // One chore in a list (Home groups, the Calendar day). Phone: a round check
  // button at the start and two lines (name; area · status · turn · avatars).
  // Wider screens: the meta line, the avatars, then a Done button whose weight
  // follows urgency (filled for overdue and today, quiet otherwise).
  import { href } from '../lib/base';
  import { dueText, today } from '../lib/dates';
  import { t } from '../lib/i18n';
  import type { Chore } from '../lib/types';
  import Icon from './Icon.svelte';
  import Initials from './Initials.svelte';

  let {
    chore: c,
    area = '',
    names = [],
    turn = '',
    due,
    ondone,
  }: {
    chore: Chore;
    area?: string;
    names?: string[];
    turn?: string;
    /** The date this row stands for (a calendar occurrence); defaults to the chore's due date. */
    due?: string;
    /** Shows the Done button when set. */
    ondone?: () => void;
  } = $props();

  const day = $derived(due ?? c.dueDate);
  const late = $derived(day < today() && (c.status === 'overdue' || c.status === 'today' || c.status === 'thisWeek' || c.status === 'later'));
  const urgent = $derived(c.status === 'overdue' || c.status === 'today');
  const status = $derived(
    c.status === 'paused'
      ? t('status.paused')
      : c.status === 'none'
        ? t('status.none')
        : c.status === 'archived'
          ? t('home.archived')
          : dueText(day),
  );
</script>

<li class="chore" class:late>
  <a class="chore-main" href={href(`/chores/${c.id}`)}>
    <span class="name">{c.name}</span>
    <span class="meta">
      {#if area}<span>{area}</span><span class="sep" aria-hidden="true">&middot;</span>{/if}<span class="status">{status}</span>{#if turn}<span class="sep" aria-hidden="true">&middot;</span><span>{t('chore.turnOf', { name: turn })}</span>{/if}
      {#if names.length}<span class="who-inline"><Initials {names} /></span>{/if}
    </span>
  </a>
  {#if names.length}<span class="who"><Initials {names} /></span>{/if}
  {#if ondone}
    <button type="button" class="btn done" class:quiet={!urgent} aria-label={t('home.doneFor', { name: c.name })} onclick={ondone}>
      <span class="tick"><Icon name="check" size={20} /></span>
      <span class="label">{t('common.done')}</span>
    </button>
  {/if}
</li>

<style>
  .chore {
    flex-wrap: nowrap;
    gap: 12px;
  }
  .chore-main {
    flex: 1;
    display: grid;
    min-inline-size: 0;
    min-block-size: var(--tap);
    align-content: center;
    color: var(--text);
    text-decoration: none;
  }
  .name {
    font-weight: 600;
  }
  .meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    column-gap: 6px;
    font-size: 0.9375rem;
    color: var(--text-2);
  }
  .late .status {
    color: var(--danger);
    font-weight: 700;
  }
  .done.quiet {
    background: transparent;
    color: var(--text);
    box-shadow: inset 0 0 0 1px var(--border);
  }
  .tick {
    display: none;
  }

  @media (max-width: 767px) {
    .chore {
      padding-block: 8px;
    }
    .name {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font-size: 1rem;
    }
    .meta {
      font-size: 0.875rem;
    }
    .who-inline :global(.avatar) {
      min-inline-size: 22px;
      block-size: 22px;
      font-size: 0.6875rem;
    }
    .who {
      display: none;
    }
    /* A round check at the start; the text label is for wider screens. */
    .done,
    .done.quiet {
      order: -1;
      flex: none;
      inline-size: var(--tap);
      block-size: var(--tap);
      padding: 0;
      border-radius: 50%;
      background: transparent;
      color: var(--text-2);
      box-shadow: inset 0 0 0 2px var(--border);
    }
    .late .done {
      color: var(--danger);
      box-shadow: inset 0 0 0 2px var(--danger);
    }
    .tick {
      display: inline-flex;
    }
    .label {
      display: none;
    }
  }
  @media (min-width: 768px) {
    .who-inline {
      display: none;
    }
    .done {
      min-block-size: 38px;
      padding-block: 6px;
      padding-inline: 16px;
    }
  }
</style>
