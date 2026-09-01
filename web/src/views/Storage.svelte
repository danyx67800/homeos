<script>
  import Icon from '../components/Icon.svelte';
  import Modal from '../components/Modal.svelte';
  import UsageBar from '../components/UsageBar.svelte';
  import { api } from '../lib/api.js';
  import { telemetry, toast } from '../lib/stores.svelte.js';
  import { bytes, celsius, duration } from '../lib/format.js';

  let disks = $state([]);
  let mountRoot = $state('/mnt/storage');
  let loading = $state(true);
  let error = $state('');
  let busy = $state('');

  // --- format dialog -------------------------------------------------------
  let formatOpen = $state(false);
  let formatTarget = $state(null);
  let formatFs = $state('ext4');
  let formatLabel = $state('');
  let formatConfirm = $state('');

  // --- mount dialog --------------------------------------------------------
  let mountOpen = $state(false);
  let mountTarget = $state(null);
  let mountName = $state('');

  // --- SMART detail --------------------------------------------------------
  let healthOpen = $state(false);
  let healthDevice = $state('');
  let health = $state(null);
  let healthLoading = $state(false);

  async function load() {
    try {
      const r = await api.disks();
      disks = r.disks ?? [];
      mountRoot = r.mount_root ?? mountRoot;
      error = '';
    } catch (err) {
      // 503 means the storage tooling is absent (a container-based install, a
      // stripped image), which is worth saying plainly rather than as a
      // generic failure.
      error = err.status === 503
        ? 'Storage management is unavailable on this system. ' + err.message
        : err.message;
    } finally {
      loading = false;
    }
  }

  // The stream pushes the disk topology every 30s and on hotplug, so the panel
  // stays current without a poll of its own.
  $effect(() => {
    if (telemetry.disks?.length) {
      disks = telemetry.disks;
      loading = false;
    }
  });
  $effect(() => { load(); });

  function openFormat(disk) {
    formatTarget = disk;
    formatFs = 'ext4';
    formatLabel = '';
    formatConfirm = '';
    formatOpen = true;
  }

  async function doFormat() {
    if (!formatTarget || formatConfirm !== formatTarget.path) return;
    busy = formatTarget.path;
    formatOpen = false;
    try {
      const r = await api.format(formatTarget.path, formatFs, formatLabel || undefined);
      toast('success', `Formatted ${formatTarget.path} as ${formatFs} (${r.partition})`);
      await load();
    } catch (err) {
      toast('error', err.message);
    } finally {
      busy = '';
    }
  }

  function openMount(partition) {
    mountTarget = partition;
    // A sensible default the user can accept: the filesystem label, else the
    // device name.
    mountName = (partition.label || partition.name || '').replace(/[^A-Za-z0-9._-]/g, '');
    mountOpen = true;
  }

  async function doMount() {
    if (!mountTarget || !mountName) return;
    busy = mountTarget.path;
    mountOpen = false;
    try {
      const r = await api.mount(mountTarget.path, mountName, true);
      toast('success', `Mounted at ${r.mountpoint}`);
      await load();
    } catch (err) {
      toast('error', err.message);
    } finally {
      busy = '';
    }
  }

  async function unmount(partition) {
    const name = partition.mountpoint.split('/').pop();
    if (!window.confirm(`Unmount ${partition.mountpoint}?`)) return;
    busy = partition.path;
    try {
      await api.unmount(name);
      toast('success', `Unmounted ${partition.mountpoint}`);
      await load();
    } catch (err) {
      toast('error', err.message);
    } finally {
      busy = '';
    }
  }

  async function openHealth(disk, force = false) {
    healthDevice = disk.path;
    healthOpen = true;
    healthLoading = true;
    health = null;
    try {
      const r = await api.diskHealth(disk.path, force);
      health = r.health;
    } catch (err) {
      toast('error', err.message);
      healthOpen = false;
    } finally {
      healthLoading = false;
    }
  }

  function healthBadge(disk) {
    const h = disk.health;
    if (!h) return { label: 'unknown', klass: 'muted' };
    if (!h.supported) return { label: 'no SMART', klass: 'muted' };
    if (!h.passed || (h.warnings ?? []).length) {
      return { label: 'attention', klass: 'text-[var(--color-bad)]' };
    }
    return { label: 'healthy', klass: 'text-[var(--color-ok)]' };
  }

  const canFormat = $derived((d) => !d.in_use && !busy);
</script>

<section class="mb-5 flex items-center justify-between">
  <h2 class="flex items-center gap-2 text-sm font-semibold">
    <Icon name="hdd" size={16} /> Disks
    {#if disks.length}<span class="muted font-normal">({disks.length})</span>{/if}
  </h2>
  <button class="btn" onclick={load}><Icon name="refresh" size={15} /> Rescan</button>
</section>

{#if error}
  <div class="glass flex items-center gap-3 p-5 text-sm text-[var(--color-bad)]">
    <Icon name="warn" size={18} /><span>{error}</span>
  </div>
{:else if loading}
  <div class="flex flex-col gap-4">
    {#each Array(2) as _, i (i)}<div class="glass h-36 animate-pulse opacity-50"></div>{/each}
  </div>
{:else if !disks.length}
  <div class="glass flex flex-col items-center gap-3 p-10 text-center">
    <Icon name="hdd" size={26} class="muted" />
    <p class="text-sm font-medium">No disks detected</p>
    <p class="muted max-w-sm text-sm">
      Attach a drive and it appears here — the appliance watches for hotplug
      events, so no rescan is needed.
    </p>
  </div>
{:else}
  <div class="flex flex-col gap-4">
    {#each disks as disk (disk.path)}
      {@const badge = healthBadge(disk)}
      <article class="glass p-5">
        <header class="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="font-medium">{disk.model || disk.name}</h3>
              <span class="chip tabular">{bytes(disk.size_bytes)}</span>
              <span class="chip muted">{disk.rotational ? 'HDD' : 'SSD'}</span>
              {#if disk.transport}<span class="chip muted uppercase">{disk.transport}</span>{/if}
              {#if disk.removable}<span class="chip muted">removable</span>{/if}
            </div>
            <p class="muted mt-1 font-mono text-xs">
              {disk.path}{disk.serial ? ` · ${disk.serial}` : ''}
            </p>
          </div>

          <div class="flex items-center gap-2">
            <button class="chip {badge.klass}" onclick={() => openHealth(disk)}>
              {#if disk.health?.temperature_celsius}
                {celsius(disk.health.temperature_celsius)} ·
              {/if}
              {badge.label}
            </button>

            <button class="btn" disabled={!canFormat(disk)}
                    title={disk.in_use ? 'Unmount its partitions first' : 'Erase and format'}
                    onclick={() => openFormat(disk)}>
              <Icon name="trash" size={14} /> Format
            </button>
          </div>
        </header>

        {#if disk.partitions?.length}
          <div class="flex flex-col gap-4">
            {#each disk.partitions as p (p.path)}
              <div class="flex flex-wrap items-end gap-4">
                <div class="min-w-56 grow">
                  {#if p.mountpoint && p.total_bytes !== 0}
                    <UsageBar used={p.used_bytes} total={p.used_bytes + p.free_bytes}
                              label={p.mountpoint}
                              sublabel="{p.path} · {p.fstype || 'unformatted'}{p.label ? ` · ${p.label}` : ''}" />
                  {:else}
                    <div class="flex flex-col gap-1">
                      <span class="text-sm font-medium">{p.path}</span>
                      <span class="muted text-xs">
                        {bytes(p.size_bytes)} · {p.fstype || 'no filesystem'}
                        {p.label ? ` · ${p.label}` : ''}
                        {p.mountpoint ? ` · mounted at ${p.mountpoint}` : ' · not mounted'}
                      </span>
                    </div>
                  {/if}
                </div>

                {#if p.mountpoint}
                  <button class="btn" disabled={busy === p.path}
                          onclick={() => unmount(p)}>
                    <Icon name="eject" size={14} /> Unmount
                  </button>
                {:else if p.fstype}
                  <button class="btn" disabled={busy === p.path}
                          onclick={() => openMount(p)}>
                    <Icon name="share" size={14} /> Mount
                  </button>
                {/if}
              </div>
            {/each}
          </div>
        {:else}
          <p class="muted text-sm">
            No partitions. Format the disk to create one filling the whole drive.
          </p>
        {/if}
      </article>
    {/each}
  </div>
{/if}

<!-- Format. The confirmation is typing the device path, matching what the API
     requires: this is the one action that destroys every byte on a disk. -->
<Modal bind:open={formatOpen} size="md" title="Format disk"
       subtitle={formatTarget?.path ?? ''}>
  <div class="flex flex-col gap-4">
    <p class="flex items-start gap-2 rounded-xl bg-[var(--color-bad)]/10 px-3 py-2.5
              text-sm text-[var(--color-bad)]">
      <Icon name="warn" size={16} class="mt-0.5 shrink-0" />
      <span>
        This erases <strong>everything</strong> on
        {formatTarget?.model || formatTarget?.name} ({bytes(formatTarget?.size_bytes ?? 0)}).
        A new partition table and a single full-disk partition are created.
        There is no undo.
      </span>
    </p>

    <label class="flex flex-col gap-1.5">
      <span class="muted text-xs font-medium">Filesystem</span>
      <select class="field" bind:value={formatFs}>
        <option value="ext4">ext4 — the safe default</option>
        <option value="btrfs">btrfs — snapshots and checksums</option>
        <option value="xfs">xfs — large files, cannot shrink</option>
      </select>
    </label>

    <label class="flex flex-col gap-1.5">
      <span class="muted text-xs font-medium">Label (optional)</span>
      <input class="field" bind:value={formatLabel} placeholder="media"
             maxlength="64" spellcheck="false" />
    </label>

    <label class="flex flex-col gap-1.5">
      <span class="muted text-xs font-medium">
        Type <code class="font-mono">{formatTarget?.path}</code> to confirm
      </span>
      <input class="field font-mono" bind:value={formatConfirm}
             autocomplete="off" spellcheck="false" />
    </label>
  </div>

  {#snippet footer()}
    <button class="btn" onclick={() => (formatOpen = false)}>Cancel</button>
    <button class="btn btn-danger" onclick={doFormat}
            disabled={formatConfirm !== formatTarget?.path}>
      Erase and format
    </button>
  {/snippet}
</Modal>

<!-- Mount -->
<Modal bind:open={mountOpen} size="sm" title="Mount partition"
       subtitle={mountTarget?.path ?? ''}>
  <label class="flex flex-col gap-1.5">
    <span class="muted text-xs font-medium">Mount as</span>
    <div class="flex items-center gap-1.5">
      <span class="muted shrink-0 font-mono text-sm">{mountRoot}/</span>
      <input class="field font-mono" bind:value={mountName} spellcheck="false"
             placeholder="media" />
    </div>
    <span class="muted text-xs">
      Letters, digits, dot, dash and underscore. The mount is written to
      /etc/fstab by UUID, so it survives a reboot and a change of device name.
    </span>
  </label>

  {#snippet footer()}
    <button class="btn" onclick={() => (mountOpen = false)}>Cancel</button>
    <button class="btn btn-primary" onclick={doMount} disabled={!mountName}>Mount</button>
  {/snippet}
</Modal>

<!-- SMART detail -->
<Modal bind:open={healthOpen} size="lg" title="Drive health" subtitle={healthDevice}>
  {#if healthLoading}
    <p class="muted text-sm">Reading SMART data…</p>
  {:else if !health}
    <p class="muted text-sm">No data.</p>
  {:else if !health.supported}
    <div class="flex flex-col gap-2">
      <p class="text-sm">This drive does not report SMART data.</p>
      {#if health.error}<p class="muted text-xs">{health.error}</p>{/if}
      <p class="muted text-xs">
        Common with USB enclosures whose bridge chip does not pass SMART
        through. It means unknown, not unhealthy.
      </p>
    </div>
  {:else}
    <div class="flex flex-col gap-5">
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div class="rounded-xl bg-[rgb(var(--ink-muted)/0.08)] p-3">
          <p class="muted text-xs">Status</p>
          <p class="mt-0.5 font-medium {health.passed ? 'text-[var(--color-ok)]' : 'text-[var(--color-bad)]'}">
            {health.passed ? 'Passed' : 'Failed'}
          </p>
        </div>
        <div class="rounded-xl bg-[rgb(var(--ink-muted)/0.08)] p-3">
          <p class="muted text-xs">Temperature</p>
          <p class="mt-0.5 font-medium tabular">{celsius(health.temperature_celsius)}</p>
        </div>
        <div class="rounded-xl bg-[rgb(var(--ink-muted)/0.08)] p-3">
          <p class="muted text-xs">Powered on</p>
          <p class="mt-0.5 font-medium tabular">{duration((health.power_on_hours ?? 0) * 3600)}</p>
        </div>
        <div class="rounded-xl bg-[rgb(var(--ink-muted)/0.08)] p-3">
          <p class="muted text-xs">{health.percentage_used ? 'Endurance used' : 'Power cycles'}</p>
          <p class="mt-0.5 font-medium tabular">
            {health.percentage_used ? `${health.percentage_used}%` : (health.power_cycle_count ?? '—')}
          </p>
        </div>
      </div>

      <!-- Warnings come first: a drive can report "passed" while quietly
           reallocating sectors, and that is exactly what needs attention. -->
      {#if (health.warnings ?? []).length}
        <div class="rounded-xl bg-[var(--color-bad)]/10 p-3">
          <p class="mb-2 flex items-center gap-2 text-sm font-medium text-[var(--color-bad)]">
            <Icon name="warn" size={15} /> Needs attention
          </p>
          <ul class="flex list-inside list-disc flex-col gap-1 text-sm">
            {#each health.warnings as w (w)}<li>{w}</li>{/each}
          </ul>
          <p class="muted mt-2 text-xs">
            SMART still reports this drive as passing. These counters are the
            ones that predict failure, so back up what is on it.
          </p>
        </div>
      {/if}

      {#if (health.attributes ?? []).length}
        <details>
          <summary class="muted cursor-pointer text-xs font-medium">
            All attributes ({health.attributes.length})
          </summary>
          <div class="mt-3 overflow-x-auto">
            <table class="w-full text-xs">
              <thead class="muted text-left">
                <tr>
                  <th class="py-1 pr-3 font-medium">ID</th>
                  <th class="py-1 pr-3 font-medium">Attribute</th>
                  <th class="py-1 pr-3 font-medium">Value</th>
                  <th class="py-1 pr-3 font-medium">Worst</th>
                  <th class="py-1 pr-3 font-medium">Thresh</th>
                  <th class="py-1 font-medium">Raw</th>
                </tr>
              </thead>
              <tbody class="tabular">
                {#each health.attributes as a (a.id)}
                  <tr class="border-t border-[rgb(var(--hairline)/0.08)]
                             {a.failing ? 'text-[var(--color-bad)]' : ''}">
                    <td class="py-1 pr-3">{a.id}</td>
                    <td class="py-1 pr-3 font-sans">{a.name}</td>
                    <td class="py-1 pr-3">{a.value}</td>
                    <td class="py-1 pr-3">{a.worst}</td>
                    <td class="py-1 pr-3">{a.threshold}</td>
                    <td class="py-1">{a.raw}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>
      {/if}
    </div>
  {/if}

  {#snippet footer()}
    <button class="btn" onclick={() => openHealth({ path: healthDevice }, true)}
            disabled={healthLoading}>
      <Icon name="refresh" size={14} /> Re-read now
    </button>
    <button class="btn" onclick={() => (healthOpen = false)}>Close</button>
  {/snippet}
</Modal>
