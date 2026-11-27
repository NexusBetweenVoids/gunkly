import { api } from './api';
import { failure } from './errors';
import { t } from './i18n';
import { toast } from './toast.svelte';
import type { Chore } from './types';

/**
 * Mark a chore done from a list (Home, Calendar) and offer Undo in a toast.
 * Returns the updated chore; `undone` gets the chore after an Undo, `fail`
 * the message when the Undo fails. A failed completion throws.
 */
export async function completeChore(
  base: string,
  c: Chore,
  undone: (c: Chore) => void,
  fail: (message: string) => void,
): Promise<Chore> {
  const r = await api<{ completion: { id: string }; chore: Chore }>('POST', `${base}/chores/${c.id}/complete`);
  toast(t('home.completed', { name: c.name }), {
    label: t('common.undo'),
    run: async () => {
      try {
        undone(await api<Chore>('POST', `${base}/chores/${c.id}/completions/${r.completion.id}/revert`));
        toast(t('home.undone', { name: c.name }));
      } catch (x) {
        fail(failure(x).message);
      }
    },
  });
  return r.chore;
}
