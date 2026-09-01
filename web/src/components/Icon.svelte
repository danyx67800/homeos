<script>
  /**
   * Inline SVG icons.
   *
   * A hand-picked set rather than an icon package: the dashboard needs about
   * twenty glyphs, and shipping a library for that would add more bytes than
   * the rest of the app. Paths are 24x24, stroke-based, so they inherit colour
   * and line weight from context.
   */
  let { name, size = 20, stroke = 1.75, class: klass = '' } = $props();

  const PATHS = {
    cpu: 'M9 3v2m6-2v2M9 19v2m6-2v2M3 9h2m-2 6h2m14-6h2m-2 6h2M7 7h10v10H7z',
    memory: 'M4 6h16v12H4zM8 6v12M12 6v12M16 6v12M2 9h2M2 15h2M20 9h2M20 15h2',
    disk: 'M3 12a9 9 0 1 0 18 0 9 9 0 0 0-18 0M12 12a2 2 0 1 0 0-.001M12 8v.01',
    thermometer: 'M14 14.76V4a2 2 0 0 0-4 0v10.76a4 4 0 1 0 4 0z',
    network: 'M5 12a7 7 0 0 1 14 0M2 12a10 10 0 0 1 20 0M8.5 12a3.5 3.5 0 0 1 7 0M12 19h.01',
    grid: 'M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z',
    store: 'M3 9h18l-1.5-5H4.5zM4 9v10a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1V9M9 20v-6h6v6',
    hdd: 'M4 5h16a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM7 15h.01M11 15h6',
    share: 'M4 12v7a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-7M12 3v12M8 7l4-4 4 4',
    settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z',
    power: 'M12 3v9M6.3 6.3a9 9 0 1 0 11.4 0',
    restart: 'M3 12a9 9 0 1 0 3-6.7L3 8m0-5v5h5',
    play: 'M7 4l12 8-12 8z',
    stop: 'M6 6h12v12H6z',
    trash: 'M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m2 0v14a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V6M10 11v6M14 11v6',
    logs: 'M4 4h16v16H4zM8 9h8M8 13h8M8 17h4',
    open: 'M14 3h7v7M21 3l-9 9M19 14v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h6',
    search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.35-4.35',
    close: 'M6 6l12 12M18 6L6 18',
    check: 'M4 12l5 5L20 6',
    warn: 'M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
    info: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 16v-4M12 8h.01',
    sun: 'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10zM12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4',
    moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z',
    logout: 'M9 21H5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h4M16 17l5-5-5-5M21 12H9',
    refresh: 'M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6',
    plus: 'M12 5v14M5 12h14',
    chevron: 'M9 6l6 6-6 6',
    lock: 'M5 11h14a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1zM8 11V7a4 4 0 0 1 8 0v4',
    eject: 'M5 17h14M12 4l7 9H5z',
  };
</script>

<svg
  class={klass}
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width={stroke}
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
  focusable="false"
>
  <path d={PATHS[name] ?? PATHS.info} />
</svg>
