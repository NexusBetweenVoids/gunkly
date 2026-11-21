export const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone;

export const zones: string[] = (() => {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return [localZone];
  }
})();
