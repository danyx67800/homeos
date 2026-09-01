<script>
  import { fly } from 'svelte/transition';
  import { toasts, dismissToast } from '../lib/stores.svelte.js';
  import Icon from './Icon.svelte';

  const STYLE = {
    success: { icon: 'check', klass: 'text-[var(--color-ok)]' },
    error:   { icon: 'warn',  klass: 'text-[var(--color-bad)]' },
    info:    { icon: 'info',  klass: 'text-[var(--color-accent-400)]' },
  };
</script>

<!-- aria-live so a screen reader announces a failed install without the user
     having to go looking for it. -->
<div class="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-80 flex-col gap-2"
     aria-live="polite" aria-atomic="false">
  {#each toasts.items as t (t.id)}
    <div
      class="glass pointer-events-auto flex items-start gap-3 px-4 py-3 text-sm"
      transition:fly={{ y: 12, duration: 180 }}
    >
      <Icon name={STYLE[t.kind].icon} size={18} class={STYLE[t.kind].klass} />
      <p class="min-w-0 flex-1 break-words">{t.message}</p>
      <button class="muted shrink-0 hover:opacity-100 opacity-60"
              onclick={() => dismissToast(t.id)} aria-label="Dismiss">
        <Icon name="close" size={14} />
      </button>
    </div>
  {/each}
</div>
