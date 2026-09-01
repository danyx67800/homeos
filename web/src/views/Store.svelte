<script>
  import Icon from '../components/Icon.svelte';
  import Modal from '../components/Modal.svelte';
  import { api } from '../lib/api.js';
  import { installs, toast } from '../lib/stores.svelte.js';
  import { relative as rel } from '../lib/format.js';

  let { focusApp = null, onnavigate = () => {} } = $props();

  const CATEGORIES = [
    { id: '', label: 'All' },
    { id: 'media', label: 'Media' },
    { id: 'productivity', label: 'Productivity' },
    { id: 'networking', label: 'Networking' },
    { id: 'automation', label: 'Automation' },
    { id: 'storage', label: 'Storage' },
    { id: 'developer', label: 'Developer' },
    { id: 'monitoring', label: 'Monitoring' },
    { id: 'security', label: 'Security' },
    { id: 'communication', label: 'Communication' },
    { id: 'games', label: 'Games' },
    { id: 'other', label: 'Other' },
  ];

  let apps = $state([]);
  let rejected = $state({});
  let syncedAt = $state('');
  let loading = $state(true);
  let error = $state('');
  let category = $state('');
  let query = $state('');
  let refreshing = $state(false);

  let detail = $state(null);   // the full manifest of the open app
  let detailOpen = $state(false);
  let answers = $state({});
  let showAdvanced = $state(false);
  let installing = $state(false);

  async function load() {
    loading = true;
    try {
      const r = await api.store();
      apps = r.apps ?? [];
      rejected = r.rejected ?? {};
      syncedAt = r.synced_at;
      error = '';
    } catch (err) {
      error = err.message;
    } finally {
      loading = false;
    }
  }

  async function refresh() {
    refreshing = true;
    try {
      await api.refreshStore();
      await load();
      toast('success', 'Catalogue updated');
    } catch (err) {
      toast('error', err.message);
    } finally {
      refreshing = false;
    }
  }

  async function openDetail(id) {
    try {
      const r = await api.storeApp(id);
      detail = { ...r.app, installed: r.installed };
      // Seed the form with defaults so the common case is "press Install".
      answers = Object.fromEntries((detail.env ?? []).map((e) => [e.key, e.default ?? '']));
      showAdvanced = false;
      detailOpen = true;
    } catch (err) {
      toast('error', err.message);
    }
  }

  async function install() {
    if (!detail) return;
    installing = true;
    try {
      await api.install(detail.id, answers);
      toast('info', `Installing ${detail.name}…`);
      detailOpen = false;
      // Progress arrives on the telemetry stream, so the modal closing is not
      // the end of the story — the launcher tile takes over from here.
      onnavigate('dashboard');
    } catch (err) {
      toast('error', err.message);
    } finally {
      installing = false;
    }
  }

  const visible = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return apps.filter((a) => {
      if (category && a.category !== category) return false;
      if (!q) return true;
      return `${a.name} ${a.tagline} ${a.id}`.toLowerCase().includes(q);
    });
  });

  // Only the fields a person must actually decide on are shown up front.
  const basicFields = $derived((detail?.env ?? []).filter((e) => !e.advanced));
  const advancedFields = $derived((detail?.env ?? []).filter((e) => e.advanced));

  const missingRequired = $derived(
    (detail?.env ?? []).some(
      (e) => e.required && !e.generate && !String(answers[e.key] ?? '').trim(),
    ),
  );

  $effect(() => { load(); });
  $effect(() => { if (focusApp) openDetail(focusApp); });
</script>

<section class="mb-5 flex flex-wrap items-center gap-3">
  <h2 class="flex items-center gap-2 text-sm font-semibold">
    <Icon name="store" size={16} /> App Store
  </h2>

  <div class="relative min-w-48 grow sm:max-w-xs">
    <Icon name="search" size={15} class="muted absolute left-3 top-1/2 -translate-y-1/2" />
    <input class="field !pl-9" placeholder="Search apps…" bind:value={query} />
  </div>

  <button class="btn" onclick={refresh} disabled={refreshing}>
    <Icon name="refresh" size={15} class={refreshing ? 'animate-spin' : ''} />
    {refreshing ? 'Updating…' : 'Refresh'}
  </button>

  {#if syncedAt && !syncedAt.startsWith('0001')}
    <span class="muted text-xs">Catalogue updated {rel(syncedAt)}</span>
  {/if}
</section>

<!-- Category filter. A horizontal scroller rather than a dropdown: on a phone
     the categories stay one tap away instead of two. -->
<div class="mb-5 -mx-1 flex gap-2 overflow-x-auto px-1 pb-1">
  {#each CATEGORIES as c (c.id)}
    <button
      class="chip shrink-0 transition-colors
             {category === c.id
               ? 'bg-[var(--color-accent-500)] text-white border-transparent'
               : 'hover:bg-[rgb(var(--surface)/0.8)]'}"
      onclick={() => (category = c.id)}
    >{c.label}</button>
  {/each}
</div>

{#if error}
  <div class="glass flex items-center gap-3 p-5 text-sm text-[var(--color-bad)]">
    <Icon name="warn" size={18} /><span>{error}</span>
  </div>
{:else if loading}
  <div class="grid gap-4 grid-cols-[repeat(auto-fill,minmax(16rem,1fr))]">
    {#each Array(6) as _, i (i)}<div class="glass h-32 animate-pulse opacity-50"></div>{/each}
  </div>
{:else if !visible.length}
  <div class="glass flex flex-col items-center gap-3 p-10 text-center">
    <Icon name="search" size={26} class="muted" />
    <p class="text-sm font-medium">Nothing matches</p>
    <p class="muted max-w-sm text-sm">
      {apps.length
        ? 'Try another category or a different search.'
        : 'The catalogue has not been fetched yet. Press Refresh to pull it.'}
    </p>
  </div>
{:else}
  <div class="grid gap-4 grid-cols-[repeat(auto-fill,minmax(16rem,1fr))]">
    {#each visible as app (app.id)}
      {@const job = installs.byApp[app.id]}
      <button class="glass glass-hover flex gap-3.5 p-4 text-left"
              onclick={() => openDetail(app.id)}>
        <div class="grid h-12 w-12 shrink-0 place-items-center overflow-hidden rounded-xl
                    bg-[rgb(var(--surface)/0.9)] ring-1 ring-[rgb(var(--hairline)/0.12)]">
          {#if app.icon}
            <img src={api.storeIconUrl(app.id)} alt="" class="h-8 w-8 object-contain" loading="lazy" />
          {:else}
            <span class="text-lg font-semibold text-[var(--color-accent-400)]">
              {app.name.charAt(0)}
            </span>
          {/if}
        </div>

        <div class="min-w-0 flex-1">
          <div class="flex items-start justify-between gap-2">
            <p class="truncate text-sm font-medium">{app.name}</p>
            {#if app.installed}
              <span class="chip shrink-0 text-[var(--color-ok)]">
                <Icon name="check" size={12} /> Installed
              </span>
            {:else if job}
              <span class="chip shrink-0 text-[var(--color-accent-400)]">{job.progress ?? 0}%</span>
            {/if}
          </div>
          <p class="muted mt-0.5 line-clamp-2 text-xs leading-relaxed">{app.tagline}</p>
          <div class="muted mt-2 flex items-center gap-2 text-[11px]">
            <span class="chip">{app.category}</span>
            <span class="tabular">v{app.version}</span>
            {#if app.deprecated}
              <span class="text-[var(--color-warn)]">deprecated</span>
            {/if}
          </div>
        </div>
      </button>
    {/each}
  </div>
{/if}

<!-- Manifests the backend refused. Surfaced rather than hidden so a catalogue
     author can see why their app is missing instead of guessing. -->
{#if Object.keys(rejected).length}
  <details class="glass mt-6 p-4 text-sm">
    <summary class="cursor-pointer font-medium text-[var(--color-warn)]">
      {Object.keys(rejected).length} manifest(s) rejected
    </summary>
    <ul class="muted mt-3 flex flex-col gap-1.5 text-xs">
      {#each Object.entries(rejected) as [id, reason] (id)}
        <li><code class="font-medium">{id}</code> — {reason}</li>
      {/each}
    </ul>
  </details>
{/if}

<!-- App detail and install form -->
<Modal bind:open={detailOpen} size="lg"
       title={detail?.name ?? ''} subtitle={detail?.tagline ?? ''}>
  {#if detail}
    <div class="flex flex-col gap-5">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span class="chip">{detail.category}</span>
        <span class="chip tabular">v{detail.version}</span>
        {#each detail.architectures ?? [] as arch (arch)}
          <span class="chip muted">{arch}</span>
        {/each}
        {#if detail.license}<span class="chip muted">{detail.license}</span>{/if}
        {#if detail.website}
          <a class="chip text-[var(--color-accent-400)]" href={detail.website}
             target="_blank" rel="noopener noreferrer">
            Website <Icon name="open" size={11} />
          </a>
        {/if}
      </div>

      {#if detail.deprecated}
        <p class="flex items-start gap-2 rounded-xl bg-[var(--color-warn)]/10 px-3 py-2
                  text-sm text-[var(--color-warn)]">
          <Icon name="warn" size={16} class="mt-0.5 shrink-0" />
          <span>{detail.notice || 'This app is deprecated and no longer recommended.'}</span>
        </p>
      {/if}

      {#if detail.description}
        <p class="whitespace-pre-line text-sm leading-relaxed">{detail.description}</p>
      {/if}

      <!-- The routing mode is shown because it determines the URL the app will
           live at, which is the first thing people ask after installing. -->
      <div class="muted grid gap-2 rounded-xl bg-[rgb(var(--ink-muted)/0.08)] p-3 text-xs">
        <div class="flex justify-between gap-4">
          <span>Image</span><code class="truncate text-right">{detail.image}</code>
        </div>
        <div class="flex justify-between gap-4">
          <span>Port</span><span class="tabular">{detail.port}</span>
        </div>
        {#if detail.sidecars && Object.keys(detail.sidecars).length}
          <div class="flex justify-between gap-4">
            <span>Also runs</span>
            <span class="text-right">{Object.keys(detail.sidecars).join(', ')}</span>
          </div>
        {/if}
      </div>

      {#if !detail.installed}
        {#if basicFields.length}
          <div class="flex flex-col gap-3">
            <h3 class="text-sm font-medium">Configuration</h3>
            {#each basicFields as f (f.key)}
              <label class="flex flex-col gap-1.5">
                <span class="muted text-xs font-medium">
                  {f.label || f.key}
                  {#if f.required}<span class="text-[var(--color-bad)]">*</span>{/if}
                </span>

                {#if f.type === 'select'}
                  <select class="field" bind:value={answers[f.key]}>
                    {#each f.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}
                  </select>
                {:else if f.type === 'bool'}
                  <select class="field" bind:value={answers[f.key]}>
                    <option value="true">Enabled</option>
                    <option value="false">Disabled</option>
                  </select>
                {:else}
                  <input
                    class="field"
                    type={f.type === 'password' ? 'password' : f.type === 'number' ? 'number' : 'text'}
                    bind:value={answers[f.key]}
                    placeholder={f.generate ? 'Leave blank to generate a strong value' : ''}
                  />
                {/if}

                {#if f.description}<span class="muted text-xs">{f.description}</span>{/if}
              </label>
            {/each}
          </div>
        {/if}

        {#if advancedFields.length}
          <div>
            <button class="muted flex items-center gap-1.5 text-xs font-medium"
                    onclick={() => (showAdvanced = !showAdvanced)}>
              <Icon name="chevron" size={13}
                    class="transition-transform {showAdvanced ? 'rotate-90' : ''}" />
              Advanced ({advancedFields.length})
            </button>
            {#if showAdvanced}
              <div class="mt-3 flex flex-col gap-3">
                {#each advancedFields as f (f.key)}
                  <label class="flex flex-col gap-1.5">
                    <span class="muted text-xs font-medium">{f.label || f.key}</span>
                    <input class="field" type={f.type === 'password' ? 'password' : 'text'}
                           bind:value={answers[f.key]}
                           placeholder={f.generate ? 'Generated if left blank' : ''} />
                    {#if f.description}<span class="muted text-xs">{f.description}</span>{/if}
                  </label>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
      {/if}
    </div>
  {/if}

  {#snippet footer()}
    {#if detail?.installed}
      <span class="muted mr-auto flex items-center gap-1.5 text-sm">
        <Icon name="check" size={15} class="text-[var(--color-ok)]" /> Installed
      </span>
      <button class="btn" onclick={() => (detailOpen = false)}>Close</button>
    {:else}
      <button class="btn" onclick={() => (detailOpen = false)}>Cancel</button>
      <button class="btn btn-primary" onclick={install}
              disabled={installing || missingRequired}
              title={missingRequired ? 'Fill in the required fields first' : ''}>
        {installing ? 'Starting…' : 'Install'}
      </button>
    {/if}
  {/snippet}
</Modal>
