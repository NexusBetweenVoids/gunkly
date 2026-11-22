import type { Reminder } from './types';

// Shapes of the built-in presets (kind and days only). The server's presets have fixed
// times; a chore with a chosen reminder time is stored as the same reminders with that time.
export const PRESET_REMINDERS: Record<string, Reminder[]> = {
  gentle: [{ kind: 'on', days: 0, minute: 540 }],
  nag: [
    { kind: 'before', days: 1, minute: 1080 },
    { kind: 'on', days: 0, minute: 540 },
    { kind: 'overdue', days: 1, minute: 540 },
  ],
  none: [],
};
export const DEFAULT_MINUTE = 540;

export const hm = (m: number): string => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
export const toMinute = (v: string): number => Number(v.slice(0, 2)) * 60 + Number(v.slice(3, 5));

export type Derived = { preset: 'gentle' | 'nag' | 'custom'; minute: number | null };

// Reads a chore's custom reminder rows: when they all share one time, that time; and the preset
// they stand for when they equal it apart from the time. Otherwise "custom" (minute null when the times differ).
export function derive(rows: Reminder[]): Derived {
  if (!rows.length) return { preset: 'custom', minute: null };
  const same = rows.every((r) => r.minute === rows[0].minute);
  const minute = same ? rows[0].minute : null;
  if (same) {
    for (const name of ['gentle', 'nag'] as const) {
      const want = PRESET_REMINDERS[name];
      const key = (r: Reminder) => `${r.kind}:${r.kind === 'on' ? 0 : r.days}`;
      if (want.length === rows.length && want.every((w) => rows.some((r) => key(r) === key(w)))) return { preset: name, minute };
    }
  }
  return { preset: 'custom', minute };
}
