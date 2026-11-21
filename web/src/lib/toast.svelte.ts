export type Toast = { id: number; text: string; action?: { label: string; run: () => void } };
export const toasts = $state<Toast[]>([]);
let next = 0;

/** Show a message; with `action` (e.g. Undo) it stays for 10 s instead of 5. */
export function toast(text: string, action?: Toast['action']): void {
  const id = ++next;
  toasts.push({ id, text, action });
  setTimeout(() => dismiss(id), action ? 10000 : 5000);
}

export function dismiss(id: number): void {
  const i = toasts.findIndex((x) => x.id === id);
  if (i >= 0) toasts.splice(i, 1);
}
