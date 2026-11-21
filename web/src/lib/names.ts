import { t, type Key } from './i18n';
import type { Status } from './types';

// Translated names of server-defined templates and starter areas, by "<prefix>.<key>".
const NAMES: Record<string, Key> = {
  'area.kitchen': 'area.kitchen',
  'area.bathroom': 'area.bathroom',
  'area.garden': 'area.garden',
  'area.exterior': 'area.exterior',
  'area.appliances': 'area.appliances',
  'area.general': 'area.general',
  'template.wipe_counters': 'template.wipe_counters',
  'template.clean_stove': 'template.clean_stove',
  'template.clean_fridge': 'template.clean_fridge',
  'template.mop_floors': 'template.mop_floors',
  'template.vacuum': 'template.vacuum',
  'template.dust_surfaces': 'template.dust_surfaces',
  'template.change_bedding': 'template.change_bedding',
  'template.clean_bathroom': 'template.clean_bathroom',
  'template.clean_shower': 'template.clean_shower',
  'template.wash_towels': 'template.wash_towels',
  'template.take_out_trash': 'template.take_out_trash',
  'template.water_plants': 'template.water_plants',
  'template.mow_lawn': 'template.mow_lawn',
  'template.prune_hedges': 'template.prune_hedges',
  'template.weed_beds': 'template.weed_beds',
  'template.clean_gutters': 'template.clean_gutters',
  'template.wash_windows': 'template.wash_windows',
  'template.sweep_entrance': 'template.sweep_entrance',
  'template.descale_kettle': 'template.descale_kettle',
  'template.clean_dishwasher_filter': 'template.clean_dishwasher_filter',
  'template.clean_washing_machine': 'template.clean_washing_machine',
  'template.replace_smoke_detector_batteries': 'template.replace_smoke_detector_batteries',
  'template.change_hvac_filter': 'template.change_hvac_filter',
};

export function nameOf(prefix: 'area' | 'template', key: string): string {
  const k = NAMES[`${prefix}.${key}`];
  return k ? t(k) : key.replace(/_/g, ' ');
}

export const STATUS: Record<Status, Key> = {
  overdue: 'status.overdue', today: 'status.today', thisWeek: 'status.thisWeek', later: 'status.later',
  none: 'status.none', paused: 'status.paused', archived: 'status.archived',
};
export const UNIT: Record<'day' | 'week' | 'month', Key> = { day: 'unit.day', week: 'unit.week', month: 'unit.month' };
