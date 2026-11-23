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
    user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
    users: 'M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM2 21a7 7 0 0 1 14 0M16 3.5a4 4 0 0 1 0 7M18 14a6 6 0 0 1 4 7',
    lock: 'M5 11h14v10H5zM8 11V7a4 4 0 0 1 8 0v4',
    plug: 'M9 2v6M15 2v6M6 8h12v4a6 6 0 0 1-12 0zM12 18v4',
    download: 'M12 3v12M7 10l5 5 5-5M4 21h16',
    sliders: 'M4 7h9M17 7h3M4 17h3M11 17h9M15 5v4M9 15v4',
    link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
    clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 3',
    gauge: 'M4 18a8 8 0 1 1 16 0M12 18l4-6',
    refresh: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5',
  } as const;
  export type IconName = keyof typeof PATHS;
</script>

<script lang="ts">
  let { name }: { name: IconName } = $props();
</script>

<svg aria-hidden="true" viewBox="0 0 24 24" width="20" height="20"><path d={PATHS[name]} fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
