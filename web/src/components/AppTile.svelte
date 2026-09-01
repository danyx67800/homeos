<script>
  import Icon from './Icon.svelte';
  import { api } from '../lib/api.js';

  /**
   * One app in the launcher.
   *
   * A link, not a button: opening the app is what people came for, so it should
   * middle-click into a new tab and show its URL on hover like any other link.
   * Management lives behind the menu that appears on hover.
   */
  let { app, job = null, onmenu = () => {} } = $props();

  /** @type {HTMLButtonElement|undefined} */ let menuButton = $state();
  let iconFailed = $state(false);

  const busy = $derived(job && !['installed', 'removed', 'failed'].includes(job.state));
  const running = $derived(app.state === 'running');

  // Status is a dot *and* a word. A colour alone is not a state anyone can read
  // reliably, and the word is what tells you "unhealthy" from "stopped".
  const status = $derived.by(() => {
    if (busy) return { colour: 'var(--color-accent-500)', text: job.message || job.state, pulse: true };
    if (app.health === 'unhealthy') return { colour: 'var(--color-bad)', text: 'unhealthy' };
    if (app.health === 'starting') return { colour: 'var(--color-warn)', text: 'starting', pulse: true };
    if (running) return { colour: 'var(--color-ok)', text: 'running' };
    return { colour: 'rgb(var(--ink-3))', text: app.state || 'stopped' };
  });

  const initial = $derived((app.name || app.id || '?').charAt(0).toUpperCase());
</script>

<div class="group relative">
  <a
    href={app.url || '#'}
    target="_blank"
    rel="noopener noreferrer"
    class="panel panel-hover flex flex-col items-center gap-2 px-3 py-3.5 text-center
           {running || busy ? '' : 'opacity-60'} {app.url ? '' : 'pointer-events-none'}"
    aria-label={app.url ? `Open ${app.name}` : `${app.name} has no web address`}
    aria-disabled={app.url ? undefined : 'true'}
  >
    <div class="grid h-11 w-11 place-items-center overflow-hidden rounded-lg
                bg-[rgb(var(--raised))] text-[var(--color-accent-500)]">
      {#if app.icon && !iconFailed}
        <img src={api.storeIconUrl(app.id)} alt="" class="h-7 w-7 object-contain"
             loading="lazy" onerror={() => (iconFailed = true)} />
      {:else}
        <span class="text-lg font-semibold">{initial}</span>
      {/if}
    </div>

    <div class="min-w-0 w-full">
      <p class="truncate text-[13px] font-medium leading-tight">{app.name}</p>
      <p class="faint mt-0.5 flex items-center justify-center gap-1 truncate text-[11px]">
        <span class="h-1.5 w-1.5 shrink-0 rounded-full {status.pulse ? 'animate-pulse' : ''}"
              style="background:{status.colour}"></span>
        <span class="truncate">{status.text}</span>
        {#if !app.url}
          <span class="truncate">· no address</span>
        {/if}
      </p>
    </div>
  </a>

  {#if busy}
    <div class="absolute inset-x-3 bottom-2 h-[2px] overflow-hidden rounded-full
                bg-[rgb(var(--ink-3)/0.25)]">
      <div class="h-full rounded-full bg-[var(--color-accent-500)] transition-[width] duration-300"
           style="width:{job.progress ?? 0}%"></div>
    </div>
  {/if}

  <button
    bind:this={menuButton}
    class="absolute right-1.5 top-1.5 grid h-6 w-6 place-items-center rounded
           bg-[rgb(var(--surface))] text-[rgb(var(--ink-2))] opacity-0
           ring-1 ring-[rgb(var(--line)/var(--line-a))] transition-opacity
           group-hover:opacity-100 focus-visible:opacity-100"
    onclick={(e) => { e.preventDefault(); onmenu(app, menuButton); }}
    aria-label="Actions for {app.name}"
  >
    <Icon name="settings" size={12} />
  </button>
</div>
