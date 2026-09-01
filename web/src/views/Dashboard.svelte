<script>
  import Gauge from '../components/Gauge.svelte';
  import StatCard from '../components/StatCard.svelte';
  import UsageBar from '../components/UsageBar.svelte';
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

<!-- Vitals -->
<section class="mb-6 grid gap-4 lg:grid-cols-[auto_1fr]">
  <div class="glass flex flex-wrap items-center justify-center gap-6 px-5 py-6 sm:gap-12 sm:px-8">
    <Gauge value={m?.cpu?.usage_percent ?? 0} label="CPU"
           sublabel={m?.cpu?.cores ? `${m.cpu.cores} cores` : ''}
           severity={severity(m?.cpu?.usage_percent ?? 0)} />
    <Gauge value={m?.memory?.used_percent ?? 0} label="Memory"
           sublabel={m ? bytes(m.memory.used_bytes) : ''}
           severity={severity(m?.memory?.used_percent ?? 0)} />
    {#if temp}
      <Gauge value={temp.celsius} label="Temp"
             display={celsius(temp.celsius)} sublabel={temp.label}
             severity={tempSeverity(temp.celsius)} />
    {/if}
  </div>

  <!-- auto-fit, so the row stays full whether or not this machine reports
       temperature and fan speed. -->
  <div class="grid gap-4 grid-cols-[repeat(auto-fit,minmax(13.5rem,1fr))]">
    <StatCard icon="cpu" label="Processor" value={percent(m?.cpu?.usage_percent ?? 0, 1)}
            detail={m?.load ? `load ${m.load.load1.toFixed(2)}` : ''}
            severity={severity(m?.cpu?.usage_percent ?? 0)}
            history={telemetry.history.cpu} max={100} />

  <StatCard icon="memory" label="Memory"
            value={m ? `${bytes(m.memory.used_bytes)} / ${bytes(m.memory.total_bytes)}` : '—'}
            detail={m?.swap?.total_bytes ? `swap ${percent(m.swap.used_percent)}` : ''}
            severity={severity(m?.memory?.used_percent ?? 0)}
            history={telemetry.history.mem} max={100} />

  <StatCard icon="network" label="Network"
            value={bitrate((m?.network ?? []).reduce((a, n) => a + (n.recv_bytes_per_sec ?? 0), 0))}
            detail="down"
            history={telemetry.history.net} />

  <StatCard icon="hdd" label="Storage"
            value={storage.total ? bytes(storage.total - storage.used) : 'No data disks'}
            detail={storage.total ? `${percent(storage.percent)} used` : 'nothing mounted'}
            severity={storage.total ? severity(storage.percent) : 'ok'} />

    {#if fans.length}
      <StatCard icon="restart" label="Cooling" value={`${fans[0].rpm} rpm`}
                detail={fans.length > 1 ? `${fans.length} fans` : fans[0].label} />
    {/if}
  </div>
</section>

<!-- Filesystems -->
{#if (m?.filesystems ?? []).length}
  <section class="glass mb-6 p-5">
    <h2 class="muted mb-4 flex items-center gap-2 text-xs font-medium uppercase tracking-wide">
      <Icon name="hdd" size={15} /> Filesystems
    </h2>
    <div class="grid gap-4 md:grid-cols-2">
      {#each m.filesystems as fs (fs.mountpoint)}
        <UsageBar used={fs.used_bytes} total={fs.total_bytes}
                  label={fs.mountpoint} sublabel="{fs.device} · {fs.fstype}" />
      {/each}
    </div>
  </section>
{/if}

<!-- Launcher -->
<section>
  <div class="mb-4 flex items-center justify-between">
    <h2 class="flex items-center gap-2 text-sm font-semibold">
      <Icon name="grid" size={16} /> Apps
      {#if apps.list.length}<span class="muted font-normal">({apps.list.length})</span>{/if}
    </h2>
    <button class="btn" onclick={() => onnavigate('store')}>
      <Icon name="store" size={15} /> App Store
    </button>
  </div>

  {#if apps.error}
    <div class="glass flex items-center gap-3 p-5 text-sm text-[var(--color-bad)]">
      <Icon name="warn" size={18} />
      <span>{apps.error}</span>
    </div>
  {:else if apps.loading && !apps.list.length}
    <div class="grid gap-4 grid-cols-[repeat(auto-fill,minmax(9rem,1fr))]">
      {#each Array(6) as _, i (i)}
        <div class="glass h-40 animate-pulse opacity-50"></div>
      {/each}
    </div>
  {:else if !apps.list.length}
    <div class="glass flex flex-col items-center gap-3 p-10 text-center">
      <Icon name="store" size={28} class="muted" />
      <p class="text-sm font-medium">No apps installed yet</p>
      <p class="muted max-w-sm text-sm">
        Install one from the store, or label any container with
        <code class="rounded bg-[rgb(var(--ink-muted)/0.15)] px-1">homeos.enable=true</code>
        and it appears here.
      </p>
      <button class="btn btn-primary mt-1" onclick={() => onnavigate('store')}>
        Browse the store
      </button>
    </div>
  {:else}
    <div class="grid gap-4 grid-cols-[repeat(auto-fill,minmax(9rem,1fr))]">
      {#each sortedApps as app (app.id)}
        <AppTile {app} job={installs.byApp[app.id]} onmenu={openMenu} />
      {/each}
    </div>
  {/if}
</section>

<ContextMenu bind:open={menuOpen} anchor={menuAnchor} items={menuItems} onselect={onMenuSelect} />
