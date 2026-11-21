import { ApiError } from './api';

// A call that needs a recent password check: on step_up_required the shared
// dialog opens; once confirmed, the call is retried once.
export const stepUp = $state({ open: false });
let waiting: ((ok: boolean) => void) | null = null;

export function resolveStepUp(ok: boolean): void {
  stepUp.open = false;
  waiting?.(ok);
  waiting = null;
}

export async function withStepUp<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    if (!(e instanceof ApiError) || e.code !== 'step_up_required') throw e;
  }
  const ok = await new Promise<boolean>((resolve) => {
    waiting = resolve;
    stepUp.open = true;
  });
  if (!ok) throw new ApiError(0, 'cancelled');
  return fn();
}
