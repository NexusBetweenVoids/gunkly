import { has, t, type Key } from './i18n';
import type { Status } from './types';

// Translated names of server-defined templates and starter areas (area.<key>, template.<key>).
export function nameOf(prefix: 'area' | 'template', key: string): string {
  const k = `${prefix}.${key}`;
  return has(k) ? t(k as Key) : key.replace(/_/g, ' ');
}

export const STATUS: Record<Status, Key> = {
  overdue: 'home.overdue', today: 'status.today', thisWeek: 'home.thisWeek', later: 'home.later',
  none: 'status.none', paused: 'status.paused', archived: 'home.archived',
};
export const UNIT: Record<'day' | 'week' | 'month', Key> = { day: 'unit.day', week: 'unit.week', month: 'unit.month' };
