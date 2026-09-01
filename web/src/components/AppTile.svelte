<script>
  import Icon from './Icon.svelte';
  import { api } from '../lib/api.js';

  /**
   * One app in the launcher grid.
   *
   * The tile is a link, not a button: opening the app is the overwhelmingly
   * common action, so it should middle-click into a new tab and show its URL
   * on hover like any other link. Management lives behind the menu button.
   */
  let { app, job = null, onmenu = () => {} } = $props();

  /** @type {HTMLButtonElement|undefined} */ let menuButton = $state();
  let iconFailed = $state(false);

  const busy = $derived(job && !['installed', 'removed', 'failed'].includes(job.state));
  const running = $derived(app.state === 'running');

  const status = $derived.by(() => {
    if (busy) return { klass: 'bg-[var(--color-accent-400)]', label: job.state, pulse: true };
    if (app.health === 'unhealthy') return { klass: 'bg-[var(--color-bad)]', label: 'unhealthy' };
    if (app.health === 'starting') return { klass: 'bg-[var(--color-warn)]', label: 'starting', pulse: true };
    if (running) return { klass: 'bg-[var(--color-ok)]', label: 'running' };
    return { klass: 'bg-[rgb(var(--ink-muted))]', label: app.state || 'stopped' };
  });

  // The first letter is the fallback when a catalogue ships no icon, or when
  // the app was installed from a catalogue that has since moved on.
  const initial = $derived((app.name || app.id || '?').charAt(0).toUpperCase());
</script>

<div class="group relative">
  <a
    href={app.url || '#'}
    target="_blank"
    rel="noopener noreferrer"
    class="glass glass-hover flex flex-col items-center gap-3 p-4 text-center
           {running ? '' : 'opacity-70'} {app.url ? '' : 'pointer-events-none'}"
    aria-label="Open {app.name}"
  >
    <div class="relative">
      <div class="grid h-16 w-16 place-items-center overflow-hidden rounded-2xl
                  bg-[rgb(var(--surface)/0.9)] ring-1 ring-[rgb(var(--hairline)/0.12)]">
        {#if app.icon && !iconFailed}
          <img src={api.storeIconUrl(app.id)} alt="" class="h-10 w-10 object-contain"
               loading="lazy" onerror={() => (iconFailed = true)} />
        {:else}
          <span class="text-2xl font-semibold text-[var(--color-accent-400)]">{initial}</span>
        {/if}
      </div>

      <span
        class="absolute -bottom-0.5 -right-0.5 h-3.5 w-3.5 rounded-full
               ring-2 ring-[rgb(var(--surface))] {status.klass}
               {status.pulse ? 'animate-pulse' : ''}"
        title={status.label}
      ></span>
    </div>

    <div class="min-w-0 w-full">
      <p class="truncate text-sm font-medium">{app.name}</p>
      <p class="muted truncate text-xs">{busy ? job.message || job.state : status.label}</p>
    </div>
  </a>

  <!-- Progress replaces the subtitle area during an install, so the tile does
       not change height and the grid stays still. -->
  {#if busy}
    <div class="absolute inset-x-4 bottom-3 h-1 overflow-hidden rounded-full
                bg-[rgb(var(--ink-muted)/0.2)]">
      <div class="h-full rounded-full bg-[var(--color-accent-400)]
                  transition-[width] duration-300"
           style="width:{job.progress ?? 0}%"></div>
    </div>
  {/if}

  <button
    bind:this={menuButton}
    class="absolute right-2 top-2 grid h-7 w-7 place-items-center rounded-lg
           bg-[rgb(var(--surface)/0.9)] opacity-0 transition-opacity
           group-hover:opacity-100 focus-visible:opacity-100"
    onclick={(e) => { e.preventDefault(); onmenu(app, menuButton); }}
    aria-label="Actions for {app.name}"
  >
    <Icon name="settings" size={14} />
  </button>
</div>
