<script>
  import Icon from './Icon.svelte';
  import { telemetry, storageTotals, theme, session, signOut } from '../lib/stores.svelte.js';
  import { bytes, percent, clock, longDate, duration, severity, SEVERITY_CLASS } from '../lib/format.js';

  let { info = null, onnavigate = () => {}, onpower = () => {} } = $props();

  // A single ticking clock for the whole app; the telemetry stream drives
  // everything else, so this is the only interval in the dashboard.
  let now = $state(new Date());
  $effect(() => {
    const id = setInterval(() => (now = new Date()), 1000);
    return () => clearInterval(id);
  });

  const storage = $derived(storageTotals(telemetry.metrics));
  const net = $derived.by(() => {
    const ifaces = telemetry.metrics?.network ?? [];
    return {
      down: ifaces.reduce((a, n) => a + (n.recv_bytes_per_sec ?? 0), 0),
      up: ifaces.reduce((a, n) => a + (n.sent_bytes_per_sec ?? 0), 0),
      name: ifaces[0]?.interface ?? '',
    };
  });

  const CONNECTION = {
    live:         { klass: 'bg-[var(--color-ok)]',   label: 'Live' },
    connecting:   { klass: 'bg-[var(--color-warn)]', label: 'Connecting', pulse: true },
    reconnecting: { klass: 'bg-[var(--color-warn)]', label: 'Reconnecting', pulse: true },
    offline:      { klass: 'bg-[var(--color-bad)]',  label: 'Offline' },
  };
  const conn = $derived(CONNECTION[telemetry.state] ?? CONNECTION.offline);
</script>

<header class="glass sticky top-3 z-30 mx-3 mb-5 flex flex-wrap items-center gap-x-5 gap-y-3 px-4 py-3">
  <!-- Identity and clock -->
  <div class="flex items-center gap-3">
    <div class="grid h-9 w-9 place-items-center rounded-xl bg-[var(--color-accent-500)] text-white">
      <Icon name="grid" size={18} />
    </div>
    <div class="leading-tight">
      <p class="text-sm font-semibold">{info?.hostname ?? 'HomeOS'}</p>
      <p class="muted text-xs">{longDate(now)}</p>
    </div>
  </div>

  <div class="tabular text-2xl font-semibold leading-none">{clock(now)}</div>

  <div class="grow"></div>

  <!-- At-a-glance status. Hidden below md: on a phone the dashboard's own
       cards carry the same numbers, and a cramped header is worse than none. -->
  <div class="hidden items-center gap-5 text-xs md:flex">
    <div class="flex items-center gap-2" title="Network {net.name}">
      <Icon name="network" size={15} class="muted" />
      <span class="tabular">
        <span class="muted">↓</span> {bytes(net.down, 0)}/s
        <span class="muted ml-1.5">↑</span> {bytes(net.up, 0)}/s
      </span>
    </div>

    {#if storage.total > 0}
      <div class="flex items-center gap-2"
           title="{storage.count} filesystem{storage.count === 1 ? '' : 's'}">
        <Icon name="hdd" size={15} class="muted" />
        <span class="tabular {SEVERITY_CLASS[severity(storage.percent)]}">
          {bytes(storage.total - storage.used)} free
        </span>
      </div>
    {/if}

    {#if telemetry.metrics?.uptime_seconds}
      <span class="muted tabular">up {duration(telemetry.metrics.uptime_seconds)}</span>
    {/if}
  </div>

  <!-- Connection state. The dot is the honest answer to "are these numbers
       live?", which every other widget silently depends on. -->
  <div class="flex items-center gap-2 text-xs" title="Telemetry stream: {conn.label}">
    <span class="h-2 w-2 rounded-full {conn.klass} {conn.pulse ? 'animate-pulse' : ''}"></span>
    <span class="muted hidden sm:inline">{conn.label}</span>
  </div>

  <div class="flex items-center gap-1">
    <button class="btn !p-2" onclick={() => theme.toggle()}
            aria-label={theme.dark ? 'Switch to light theme' : 'Switch to dark theme'}>
      <Icon name={theme.dark ? 'sun' : 'moon'} size={16} />
    </button>

    <button class="btn !p-2" onclick={() => onnavigate('settings')} aria-label="Settings">
      <Icon name="settings" size={16} />
    </button>

    <button class="btn !p-2" onclick={() => onpower()} aria-label="Power options">
      <Icon name="power" size={16} />
    </button>

    <button class="btn !p-2" onclick={signOut} aria-label="Sign out ({session.username})">
      <Icon name="logout" size={16} />
    </button>
  </div>
</header>
