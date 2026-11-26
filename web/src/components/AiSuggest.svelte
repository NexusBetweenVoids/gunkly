<script lang="ts">
  // "Describe your chores": the instance's AI model proposes chores from plain language. Nothing is
  // created until the person adds a proposal (normal create endpoint) or opens it in the editor.
  import { api, ApiError } from '../lib/api';
  import { failure } from '../lib/errors';
  import { t, type Key } from '../lib/i18n';
  import { UNIT } from '../lib/names';
  import { describeRRule } from '../lib/rrule';
  import { toast } from '../lib/toast.svelte';
  import type { Area, Member } from '../lib/types';
  import Alert from './Alert.svelte';
  import Button from './Button.svelte';

  export type Suggestion = {
    name: string;
    notes: string;
    kind: 'recurring' | 'oneoff';
    schedule: { type: 'interval' | 'rrule'; interval?: { every: number; unit: 'day' | 'week' | 'month' }; rrule?: string; firstDue: string };
    areaId: string | null;
    assignees: string[];
    rotation: boolean;
    reminderTime: string | null;
  };

  let { base, areas, members, onEdit }: { base: string; areas: Area[]; members: Member[]; onEdit: (s: Suggestion) => void } = $props();

  let text = $state('');
  let busy = $state(false);
  let err = $state('');
  let list = $state<(Suggestion & { added?: boolean })[] | null>(null);
  let dropped = $state(0);

  const FAIL: Record<string, Key> = {
    unreachable: 'ai.fail.unreachable', auth: 'ai.fail.auth', not_found: 'ai.fail.notFound', rate_limited: 'ai.fail.rateLimited',
    timeout: 'ai.fail.timeout', bad_response: 'ai.fail.badResponse', server_error: 'ai.fail.server',
  };

  async function suggest(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!text.trim()) return;
    busy = true;
    err = '';
    list = null;
    try {
      const r = await api<{ suggestions: Suggestion[]; dropped: number }>('POST', `${base}/chores/suggest`, { text: text.trim() });
      list = r.suggestions;
      dropped = r.dropped;
    } catch (x) {
      if (x instanceof ApiError && x.status === 502) err = t(FAIL[String((x.body as { kind?: string } | undefined)?.kind)] ?? 'ai.fail.server');
      else if (x instanceof ApiError && x.code === 'ai_busy') err = t('ai.fail.busy');
      else if (x instanceof ApiError && x.status === 429) err = t('ai.fail.limit');
      else err = failure(x).message;
    } finally {
      busy = false;
    }
  }

  async function add(s: Suggestion & { added?: boolean }): Promise<void> {
    err = '';
    try {
      await api('POST', `${base}/chores`, { ...s, reminderTime: s.reminderTime ?? undefined });
      s.added = true;
      toast(t('ai.added', { name: s.name }));
    } catch (x) {
      err = failure(x).message;
    }
  }

  async function addAll(): Promise<void> {
    for (const s of list ?? []) if (!s.added) await add(s);
  }

  // The editor's own wording for the common intervals ("Every week", "Every 3 months").
  const PRESET: Record<string, Key> = {
    '1day': 'repeat.daily', '1week': 'repeat.weekly', '2week': 'repeat.2weeks', '1month': 'repeat.monthly',
    '3month': 'repeat.3months', '6month': 'repeat.6months', '12month': 'repeat.yearly',
  };
  function when(s: Suggestion): string {
    if (s.kind === 'oneoff') return t('repeat.none');
    if (s.schedule.type === 'rrule') return describeRRule(s.schedule.rrule ?? '');
    const { every, unit } = s.schedule.interval!;
    const p = PRESET[`${every}${unit}`];
    return p ? t(p) : t('repeat.every', { count: every, unit: t(UNIT[unit], { count: every }) });
  }
  const who = (s: Suggestion): string => s.assignees.map((u) => members.find((m) => m.userId === u)?.displayName ?? '').filter(Boolean).join(', ');
  const where = (s: Suggestion): string => areas.find((a) => a.id === s.areaId)?.name ?? '';
  const pending = $derived((list ?? []).filter((s) => !s.added).length);
</script>

<div class="ai" data-testid="ai-suggest">
  <form class="stack-sm" onsubmit={suggest}>
    <label class="label" for="ai-text">{t('ai.textLabel')}</label>
    <textarea id="ai-text" rows="3" maxlength="2000" bind:value={text} placeholder={t('ai.placeholder')}></textarea>
    <p class="hint">{t('ai.privacy')}</p>
    <p><Button type="submit" disabled={busy || !text.trim()}>{busy ? t('ai.thinking') : t('ai.suggest')}</Button></p>
  </form>
  <div aria-live="polite">
    <Alert message={err} />
    {#if list}
      {#if list.length}
        <p class="hint">{t('ai.review')}</p>
        <ul class="proposals">
          {#each list as s, i (i)}
            <li>
              <div>
                <strong>{s.name}</strong>
                <span class="muted">· {when(s)}{s.reminderTime ? ` · ${s.reminderTime}` : ''}{where(s) ? ` · ${where(s)}` : ''}{who(s) ? ` · ${who(s)}` : ''}{s.rotation ? ` · ${t('chore.takeTurns')}` : ''}</span>
              </div>
              <div class="acts">
                {#if s.added}
                  <span class="muted">{t('ai.addedShort')}</span>
                {:else}
                  <Button variant="quiet" onclick={() => onEdit(s)}>{t('ai.edit')}</Button>
                  <Button onclick={() => add(s)}>{t('ai.add')}</Button>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
        {#if pending > 1}<p><Button onclick={addAll}>{t('ai.addAll', { count: pending })}</Button></p>{/if}
      {:else}
        <p class="muted">{t('ai.none')}</p>
      {/if}
      {#if dropped}<p class="hint">{t('ai.dropped', { count: dropped })}</p>{/if}
    {/if}
  </div>
</div>

<style>
  .ai {
    display: grid;
    gap: var(--space);
  }
  textarea {
    inline-size: 100%;
    font: inherit;
  }
  .proposals {
    list-style: none;
    padding: 0;
    margin: 0;
    display: grid;
    gap: 0.5rem;
  }
  .proposals li {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    justify-content: space-between;
    align-items: center;
    padding-block: 0.5rem;
    border-block-end: 1px solid var(--border);
  }
  .acts {
    display: flex;
    gap: 0.5rem;
  }
</style>
