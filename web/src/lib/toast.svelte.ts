export const toasts = $state<{ id: number; text: string }[]>([]);
let next = 0;

export function toast(text: string): void {
  const id = ++next;
  toasts.push({ id, text });
  setTimeout(() => {
    const i = toasts.findIndex((x) => x.id === id);
    if (i >= 0) toasts.splice(i, 1);
  }, 5000);
}
