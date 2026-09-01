<script>
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';
  import { api } from '../lib/api.js';

  /**
   * Container logs.
   *
   * Polled rather than streamed: the telemetry stream is a broadcast to every
   * connected dashboard, and per-container log following does not belong on it.
   * A 3-second poll while the modal is open is cheap and, unlike a second
   * WebSocket, cannot outlive the dialog that opened it.
   */
  let { open = $bindable(false), app = null } = $props();

  let text = $state('');
  let loading = $state(false);
  let error = $state('');
  let follow = $state(true);
  /** @type {HTMLPreElement|undefined} */ let pre = $state();

  async function fetchLogs() {
    // An app with no container has no logs to show. Returning quietly left an
    // empty black box on screen with nothing to explain it, which looks exactly
    // like a broken log viewer rather than an app that is not running.
    if (!app?.container_id) {
      error = 'This app has no container right now, so there is nothing to read. '
            + 'It was either never started or its container has been removed — '
            + 'try Restart, or reinstall it from the store.';
      return;
    }
    loading = !text;
    try {
      const r = await api.logs(app.container_id, 400);
      const atBottom = !pre || pre.scrollHeight - pre.scrollTop - pre.clientHeight < 40;
      text = r.logs || '(no output)';
      error = '';
      // Only auto-scroll when the reader was already at the bottom; yanking
      // the viewport while somebody is reading history is infuriating.
      if (follow && atBottom) {
        queueMicrotask(() => { if (pre) pre.scrollTop = pre.scrollHeight; });
      }
    } catch (err) {
      error = err.message;
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (!open || !app) return;
    text = '';
    error = '';
    fetchLogs();
    const id = setInterval(fetchLogs, 3000);
    return () => clearInterval(id);
  });
</script>

<Modal bind:open size="xl" title="{app?.name ?? ''} logs"
       subtitle={app?.container_id ? app.container_id.slice(0, 12) : ''}>
  {#if error}
    <p class="flex items-center gap-2 text-sm text-[var(--color-bad)]">
      <Icon name="warn" size={16} /> {error}
    </p>
  {:else if loading}
    <p class="muted text-sm">Loading…</p>
  {:else}
    <pre
      bind:this={pre}
      class="max-h-[55dvh] overflow-auto rounded-xl bg-[rgb(var(--ink)/0.06)] p-3
             font-mono text-xs leading-relaxed whitespace-pre-wrap break-words"
    >{text}</pre>
  {/if}

  {#snippet footer()}
    <label class="muted mr-auto flex items-center gap-2 text-xs">
      <input type="checkbox" bind:checked={follow} /> Follow output
    </label>
    <button class="btn" onclick={fetchLogs}>
      <Icon name="refresh" size={14} /> Refresh
    </button>
    <button class="btn" onclick={() => (open = false)}>Close</button>
  {/snippet}
</Modal>
