<script>
  import VitalsBar from '../components/VitalsBar.svelte';
  import AppTile from '../components/AppTile.svelte';
  import ContextMenu from '../components/ContextMenu.svelte';
  import Icon from '../components/Icon.svelte';
  import { telemetry, apps, installs, primaryTemp, storageTotals, toast }
    from '../lib/stores.svelte.js';
  import { api } from '../lib/api.js';
  import { bytes, bitrate, percent, celsius, severity, tempSeverity } from '../lib/format.js';

  let { onnavigate = () => {}, onlogs = () => {} } = $props();

  const m = $derived(telemetry.metrics);
  const temp = $derived(primaryTemp(m));
  const storage = $derived(storageTotals(m));

  // Fans are absent on most SBCs and many mini PCs, so the card only appears
  // when the hardware actually reports one.
  const fans = $derived((m?.fans ?? []).filter((f) => f.rpm > 0));

  let menuOpen = $state(false);
  let menuApp = $state(null);
  let menuAnchor = $state(null);

  function openMenu(app, anchor) {
    menuApp = app;
    menuAnchor = anchor;
    menuOpen = true;
  }

  const menuItems = $derived.by(() => {
    if (!menuApp) return [];
    const running = menuApp.state === 'running';
    return [
      { id: 'open', label: 'Open', icon: 'open', disabled: !menuApp.url || !running },
      { id: 'restart', label: 'Restart', icon: 'restart', disabled: !running },
      running
        ? { id: 'stop', label: 'Stop', icon: 'stop' }
        : { id: 'start', label: 'Start', icon: 'play' },
      { id: 'd1', divider: true },
      { id: 'logs', label: 'View logs', icon: 'logs' },
      { id: 'settings', label: 'Settings', icon: 'settings' },
      { id: 'd2', divider: true },
      { id: 'uninstall', label: 'Uninstall', icon: 'trash', danger: true },
    ];
  });

  async function onMenuSelect(id) {
    const app = menuApp;
    if (!app) return;

    if (id === 'open') { window.open(app.url, '_blank', 'noopener'); return; }
    if (id === 'logs') { onlogs(app); return; }
    if (id === 'settings') { onnavigate('store', app.id); return; }

    if (id === 'uninstall') {
      // A native confirm rather than a styled dialog: uninstalling is rare and
      // destructive, and a modal backdrop can be dismissed by a stray click.
      if (!window.confirm(`Uninstall ${app.name}? App data is kept on disk.`)) return;
      try {
        await api.uninstall(app.id, false);
        toast('info', `Removing ${app.name}…`);
      } catch (err) { toast('error', err.message); }
      return;
    }

    try {
      await api.containerAction(app.container_id, id);
      toast('success', `${app.name} ${id === 'stop' ? 'stopped' : id + 'ed'}`);
      // The container list is not on the telemetry stream, so this is the one
      // place the UI has to go and ask.
      setTimeout(() => apps.refresh(), 600);
    } catch (err) {
      toast('error', err.message);
    }
  }

  const sortedApps = $derived(
    [...apps.list].sort((a, b) => {
      // Running apps first: on a box with twenty apps, the stopped ones are
      // the least likely to be what you are reaching for.
      if ((a.state === 'running') !== (b.state === 'running')) return a.state === 'running' ? -1 : 1;
      return (a.name || a.id).localeCompare(b.name || b.id);
    }),
  );
</script>


<!-- Apps first. The launcher is what this page is for; the vitals below are
     what you check when something feels wrong. The old order had it backwards
     and cost most of a screen before you reached anything you could click. -->
<section class="mb-5">
  <div class="mb-3 flex items-center justify-between gap-3">
    <h2 class="flex items-center gap-2 text-sm font-semibold">
      Apps
      {#if apps.list.length}
        <span class="faint readout font-normal">{apps.list.length}</span>
      {/if}
    </h2>
    <button class="btn" onclick={() => onnavigate('store')}>
      <Icon name="store" size={14} /> Store
    </button>
  </div>

  {#if apps.error}
    <div class="note bad">
      <Icon name="warn" size={15} class="mt-0.5 text-[var(--color-bad)]" />
      <span>{apps.error}</span>
    </div>
  {:else if apps.loading && !apps.list.length}
    <div class="grid gap-2.5 grid-cols-[repeat(auto-fill,minmax(8rem,1fr))]">
      {#each Array(8) as _, i (i)}
        <div class="panel h-28 animate-pulse opacity-40"></div>
      {/each}
    </div>
  {:else if !apps.list.length}
    <div class="panel flex flex-col items-center gap-2 px-6 py-8 text-center">
      <Icon name="store" size={22} class="faint" />
      <p class="text-sm font-medium">Nothing installed yet</p>
      <p class="muted max-w-sm text-[13px]">
        Install something from the store, or label any container with
        <code class="mono rounded bg-[rgb(var(--ink-3)/0.15)] px-1">homeos.enable=true</code>
        and it turns up here on its own.
      </p>
      <button class="btn btn-primary mt-1" onclick={() => onnavigate('store')}>
        Browse the store
      </button>
    </div>
  {:else}
    <div class="grid gap-2.5 grid-cols-[repeat(auto-fill,minmax(8rem,1fr))]">
      {#each sortedApps as app (app.id)}
        <AppTile {app} job={installs.byApp[app.id]} onmenu={openMenu} />
      {/each}
    </div>
  {/if}
</section>

<!-- Vitals: one strip, five readings. What two gauges and four cards used to
     say in most of a screen. -->
<VitalsBar />

{#if (m?.filesystems ?? []).length}
  <section class="panel mt-4 divide-y divide-[rgb(var(--line)/var(--line-a))]">
    {#each m.filesystems as fs (fs.mountpoint)}
      <div class="flex items-center gap-3 px-3.5 py-2.5">
        <Icon name="hdd" size={14} class="faint shrink-0" />
        <span class="mono w-40 shrink-0 truncate text-[13px]">{fs.mountpoint}</span>
        <div class="h-[3px] min-w-16 flex-1 overflow-hidden rounded-full bg-[rgb(var(--ink-3)/0.22)]">
          <div class="h-full rounded-full"
               style="width:{fs.used_percent}%;
                      background:{fs.used_percent >= 90 ? 'var(--color-bad)'
                                : fs.used_percent >= 75 ? 'var(--color-warn)' : 'var(--color-ok)'}"></div>
        </div>
        <span class="readout faint shrink-0 text-[12px] tabular">
          {bytes(fs.free_bytes)} free
        </span>
        <span class="faint hidden shrink-0 text-[11px] sm:inline">{fs.fstype}</span>
      </div>
    {/each}
  </section>
{/if}

<ContextMenu bind:open={menuOpen} anchor={menuAnchor} items={menuItems} onselect={onMenuSelect} />
