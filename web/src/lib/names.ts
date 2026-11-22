import { t, type Key } from './i18n';
import type { Status } from './types';

// Translated names of server-defined templates and starter areas, by "<prefix>.<key>".
const NAMES: readonly string[] = [
  'area.kitchen',
  'area.bathroom',
  'area.garden',
  'area.exterior',
  'area.appliances',
  'area.general',
  'template.wipe_counters',
  'template.clean_stove',
  'template.clean_fridge',
  'template.mop_floors',
  'template.vacuum',
  'template.dust_surfaces',
  'template.change_bedding',
  'template.clean_bathroom',
  'template.clean_shower',
  'template.wash_towels',
  'template.take_out_trash',
  'template.water_plants',
  'template.mow_lawn',
  'template.prune_hedges',
  'template.weed_beds',
  'template.clean_gutters',
  'template.wash_windows',
  'template.sweep_entrance',
  'template.descale_kettle',
  'template.clean_dishwasher_filter',
  'template.clean_washing_machine',
  'template.replace_smoke_detector_batteries',
  'template.change_hvac_filter',
];

export function nameOf(prefix: 'area' | 'template', key: string): string {
  const k = `${prefix}.${key}`;
  return NAMES.includes(k) ? t(k as Key) : key.replace(/_/g, ' ');
}

export const STATUS: Record<Status, Key> = {
  overdue: 'home.overdue', today: 'status.today', thisWeek: 'home.thisWeek', later: 'home.later',
  none: 'status.none', paused: 'status.paused', archived: 'home.archived',
};
export const UNIT: Record<'day' | 'week' | 'month', Key> = { day: 'unit.day', week: 'unit.week', month: 'unit.month' };
