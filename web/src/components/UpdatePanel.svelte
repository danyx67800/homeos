<script>
  import Icon from './Icon.svelte';
  import { api } from '../lib/api.js';
  import { updates, toast } from '../lib/stores.svelte.js';
  import { relative } from '../lib/format.js';

  let { version = '' } = $props();

  let busy = $state('');

  const st = $derived(updates.status);
  const rel = $derived(st?.available);
  const staged = $derived(st?.staged_version);
  const downloading = $derived(st?.state === 'downloading' || st?.state === 'verifying');
  const applying = $derived(st?.state === 'applying');

  async function run(action, fn, message) {
    busy = action;
    try {
      await fn();
      if (message) toast('info', message);
      await updates.refresh();
    } catch (err) {
      toast('error', err.message);
    } finally {
      busy = '';
    }
  }

  const check = () => run('check', api.updateCheck, null);
  const download = () => run('download', api.updateDownload, 'Downloading in the background');

  function install() {
    if (!window.confirm(
      `Install ${staged}? The appliance restarts, which briefly stops every app. ` +
      `If the new version does not come up healthy it rolls back on its own.`)) return;
    run('apply', () => api.updateApply(staged), null);
  }

  $effect(() => { updates.refresh(); });
</script>

<section class="panel p-5">
  <h2 class="mb-4 flex items-center gap-2 text-sm font-semibold">
    <Icon name="refresh" size={16} /> Software updates
  </h2>

  {#if st === null}
    <p class="muted text-sm">
      Over-the-air updates are not configured on this appliance. Set
      <code class="rounded bg-[rgb(var(--ink-2)/0.15)] px-1">update.channel_url</code>
      in <code class="font-mono">/etc/homeos/config.yaml</code> to enable them.
    </p>
  {:else}
    <div class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p class="text-sm">
            Running <span class="font-medium tabular">{version || st.current_version}</span>
          </p>
          {#if st.last_checked_at && !st.last_checked_at.startsWith('0001')}
            <p class="muted mt-0.5 text-xs">Last checked {relative(st.last_checked_at)}</p>
          {/if}
        </div>
        <button class="btn" onclick={check} disabled={!!busy || downloading || applying}>
          <Icon name="refresh" size={14} class={busy === 'check' ? 'animate-spin' : ''} />
          {busy === 'check' ? 'Checking…' : 'Check now'}
        </button>
      </div>

      {#if applying}
        <div class="rounded-xl bg-[var(--color-accent-500)]/10 p-3 text-sm">
          <p class="flex items-center gap-2 font-medium text-[var(--color-accent-400)]">
            <Icon name="refresh" size={15} class="animate-spin" /> Installing
          </p>
          <p class="muted mt-1 text-xs">
            The service is restarting. This page reconnects on its own; if the new
            version does not come up healthy, the previous one is restored
            automatically.
          </p>
        </div>

      {:else if downloading}
        <div class="flex flex-col gap-2">
          <div class="flex items-center justify-between text-sm">
            <span>{st.message || 'Downloading'}</span>
            <span class="muted tabular text-xs">{st.progress ?? 0}%</span>
          </div>
          <div class="h-2 overflow-hidden rounded-full bg-[rgb(var(--ink-2)/0.16)]">
            <div class="h-full rounded-full bg-[var(--color-accent-500)] transition-[width] duration-300"
                 style="width:{st.progress ?? 0}%"></div>
          </div>
        </div>

      {:else if staged}
        <div class="rounded-xl bg-[var(--color-ok)]/10 p-3">
          <p class="flex items-center gap-2 text-sm font-medium text-[var(--color-ok)]">
            <Icon name="check" size={15} /> {staged} is downloaded and verified
          </p>
          <p class="muted mt-1 text-xs">
            Nothing has changed yet. Installing swaps the release and restarts
            the service; your apps, data and settings are untouched.
          </p>
          <button class="btn btn-primary mt-3" onclick={install} disabled={!!busy}>
            {busy === 'apply' ? 'Starting…' : `Install ${staged}`}
          </button>
        </div>

      {:else if rel}
        <div class="rounded-xl bg-[rgb(var(--ink-2)/0.08)] p-3">
          <p class="text-sm font-medium">Version {rel.version} is available</p>
          {#if rel.notes}
            <p class="muted mt-1 whitespace-pre-line text-xs leading-relaxed">{rel.notes}</p>
          {/if}
          <button class="btn btn-primary mt-3" onclick={download} disabled={!!busy}>
            <Icon name="chevron" size={14} class="rotate-90" /> Download
          </button>
        </div>

      {:else if st.state === 'up_to_date'}
        <p class="flex items-center gap-2 text-sm text-[var(--color-ok)]">
          <Icon name="check" size={15} /> Up to date
        </p>

      {:else if st.error}
        <p class="flex items-start gap-2 text-sm text-[var(--color-bad)]">
          <Icon name="warn" size={15} class="mt-0.5 shrink-0" /> {st.error}
        </p>
      {/if}

      <!-- Written by the privileged helper after this process was restarted,
           so a rollback the daemon was not alive to see still gets reported. -->
      {#if updates.lastApply}
        {@const la = updates.lastApply}
        <p class="muted border-t border-[rgb(var(--line)/0.1)] pt-3 text-xs">
          {#if la.status === 'rolled_back'}
            <span class="text-[var(--color-warn)]">
              Version {la.version} was rolled back: {la.message}
            </span>
          {:else if la.status === 'failed'}
            <span class="text-[var(--color-bad)]">
              Last update failed: {la.message}
            </span>
          {:else}
            Last update: {la.version} — {la.message}
          {/if}
        </p>
      {/if}
    </div>
  {/if}
</section>
