<script>
  import Icon from './Icon.svelte';

  /**
   * Anchored menu for app tiles.
   *
   * Positioned in fixed coordinates from the trigger's rectangle and flipped
   * when it would leave the viewport — a grid tile near the right edge is
   * exactly where a naive absolute menu gets clipped.
   */
  let { open = $bindable(false), anchor = null, items = [], onselect = () => {} } = $props();

  /** @type {HTMLDivElement|undefined} */ let menu = $state();
  let pos = $state({ x: 0, y: 0 });

  $effect(() => {
    if (!open || !anchor || !menu) return;
    const r = anchor.getBoundingClientRect();
    const w = menu.offsetWidth || 200;
    const h = menu.offsetHeight || 240;
    pos = {
      x: Math.min(r.left, window.innerWidth - w - 12),
      y: r.bottom + h > window.innerHeight ? Math.max(12, r.top - h - 6) : r.bottom + 6,
    };
    menu.querySelector('button')?.focus();
  });

  function choose(item) {
    if (item.disabled) return;
    open = false;
    onselect(item.id);
  }

  function onKey(event) {
    if (event.key === 'Escape') open = false;
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <!-- A full-screen catcher, so any click outside dismisses the menu without
       needing a document-level listener that has to be torn down. -->
  <button class="fixed inset-0 z-40 cursor-default"
          onclick={() => (open = false)} aria-label="Close menu" tabindex="-1"></button>

  <div
    bind:this={menu}
    class="glass fixed z-50 w-52 overflow-hidden p-1 text-sm"
    style="left:{pos.x}px; top:{pos.y}px"
    role="menu"
  >
    {#each items as item (item.id)}
      {#if item.divider}
        <div class="my-1 h-px bg-[rgb(var(--hairline)/0.12)]"></div>
      {:else}
        <button
          role="menuitem"
          class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left
                 transition-colors hover:bg-[rgb(var(--surface)/0.9)]
                 disabled:opacity-40 disabled:hover:bg-transparent
                 {item.danger ? 'text-[var(--color-bad)]' : ''}"
          disabled={item.disabled}
          onclick={() => choose(item)}
        >
          <Icon name={item.icon} size={16} />
          <span>{item.label}</span>
        </button>
      {/if}
    {/each}
  </div>
{/if}
