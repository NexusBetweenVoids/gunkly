// Admin configuration (GET/PATCH /admin/config): values an admin sets here instead of in the server's
// environment. An environment variable always wins; such a field is locked. Secrets are never returned.
import { api } from '../../lib/api';
import { withStepUp } from '../../lib/stepup.svelte';

export type ConfigField = { value: string | null; set: boolean; locked: boolean; env: string };
export type Config = Record<string, ConfigField>;

export async function loadConfig(): Promise<Config> {
  return (await api<{ fields: Config }>('GET', '/admin/config')).fields;
}

/** Saves the given keys (null or '' resets to the default); secrets only when a new value was typed. */
export async function saveConfig(values: Record<string, string | null>): Promise<Config> {
  return (await withStepUp(() => api<{ fields: Config }>('PATCH', '/admin/config', { values }))).fields;
}

/** The keys of values that differ from what is stored and are not locked. */
export function changed(cfg: Config, draft: Record<string, string>, secrets: Record<string, string>): Record<string, string | null> {
  const out: Record<string, string | null> = {};
  for (const [k, v] of Object.entries(draft)) if (!cfg[k]?.locked && (cfg[k]?.value ?? '') !== v) out[k] = v;
  for (const [k, v] of Object.entries(secrets)) if (!cfg[k]?.locked && v) out[k] = v;
  return out;
}
