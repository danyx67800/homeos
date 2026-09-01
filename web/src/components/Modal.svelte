<script>
  import Icon from './Icon.svelte';

  /**
   * Dialog shell. Uses the native <dialog> element so focus trapping, the
   * top layer and Escape-to-close come from the platform rather than from
   * hand-written keyboard handling that is always subtly wrong.
   */
  let {
    open = $bindable(false),
    title = '',
    subtitle = '',
    size = 'md', // sm | md | lg | xl
    onclose = () => {},
    children,
    footer,
  } = $props();

  /** @type {HTMLDialogElement|undefined} */ let dialog = $state();

  const WIDTH = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-2xl', xl: 'max-w-4xl' };

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  });

  function close() {
    open = false;
    onclose();
  }

  // The native backdrop is part of the dialog element, so a click on it lands
  // on the dialog itself; comparing the target is how you tell them apart.
  function onBackdrop(event) {
    if (event.target === dialog) close();
  }
</script>

<dialog
  bind:this={dialog}
  onclose={close}
  onclick={onBackdrop}
  class="m-auto w-[calc(100vw-2rem)] {WIDTH[size]} bg-transparent p-0
         backdrop:bg-black/50 backdrop:backdrop-blur-sm"
>
  <div class="panel max-h-[85dvh] overflow-hidden flex flex-col">
    <header class="flex items-start justify-between gap-4 border-b
                   border-[rgb(var(--line)/0.1)] px-5 py-4">
      <div class="min-w-0">
        <h2 class="truncate text-base font-semibold">{title}</h2>
        {#if subtitle}<p class="muted mt-0.5 text-sm">{subtitle}</p>{/if}
      </div>
      <button class="btn !p-2" onclick={close} aria-label="Close">
        <Icon name="close" size={16} />
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-5 py-4">
      {@render children?.()}
    </div>

    {#if footer}
      <footer class="flex items-center justify-end gap-2 border-t
                     border-[rgb(var(--line)/0.1)] px-5 py-3">
        {@render footer()}
      </footer>
    {/if}
  </div>
</dialog>

<style>
  dialog[open] {
    animation: pop 180ms cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes pop {
    from { opacity: 0; transform: translateY(8px) scale(0.98); }
    to   { opacity: 1; transform: none; }
  }
  @media (prefers-reduced-motion: reduce) {
    dialog[open] { animation: none; }
  }
</style>
