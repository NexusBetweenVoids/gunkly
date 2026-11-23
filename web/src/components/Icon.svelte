<script lang="ts" module>
  // 24×24 outline icons, drawn with a 2px round stroke. Decorative only:
  // the button around an icon carries the accessible name.
  const PATHS = {
    snooze: 'M12 21a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM12 9v4l2.5 2.5M5 3 2 6M19 3l3 3',
    skip: 'M5 5l10 7-10 7zM19 5v14',
    pause: 'M8 5v14M16 5v14',
    resume: 'M7 5l12 7-12 7z',
    edit: 'M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4',
    bell: 'M6 16v-5a6 6 0 0 1 12 0v5l2 2H4zM10 21h4',
    bellOff: 'M6 16v-5a6 6 0 0 1 12 0v5l2 2H4zM10 21h4M3 3l18 18',
    archive: 'M3 4h18v5H3zM5 9v11h14V9M10 13h4',
    trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6',
  } as const;
  export type IconName = keyof typeof PATHS;
</script>

<script lang="ts">
  let { name }: { name: IconName } = $props();
</script>

<svg aria-hidden="true" viewBox="0 0 24 24" width="20" height="20"><path d={PATHS[name]} fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
