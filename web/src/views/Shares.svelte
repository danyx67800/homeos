<script>
  import Icon from '../components/Icon.svelte';
  import Modal from '../components/Modal.svelte';
  import { api } from '../lib/api.js';
  import { telemetry, toast } from '../lib/stores.svelte.js';

  /**
   * Samba shares.
   *
   * The API replaces the whole set in one PUT, matching how the file is
   * written on disk. So the editor works on a local copy and saves it whole —
   * which also means a failed validation leaves the server exactly as it was.
   */
  let shares = $state([]);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');

  let editorOpen = $state(false);
  let editing = $state(null);
  let editingIndex = $state(-1);
  let usersText = $state('');

  const blank = () => ({
    name: '', path: '', comment: '',
    read_only: false, public: false, valid_users: [],
    browseable: true, recycle_bin: true, time_machine: false,
  });

  // Mount points the backend will accept as share roots.
  const mountPoints = $derived([
    ...new Set((telemetry.metrics?.filesystems ?? [])
      .map((f) => f.mountpoint)
      .filter((m) => m.startsWith('/mnt/storage'))),
    '/var/lib/homeos/data',
  ]);

  async function load() {
    try {
      const r = await api.shares();
      shares = r.shares ?? [];
      error = '';
    } catch (err) {
      error = err.message;
    } finally {
      loading = false;
    }
  }
  $effect(() => { load(); });

  function edit(index) {
    editingIndex = index;
    editing = index < 0 ? blank() : structuredClone($state.snapshot(shares[index]));
    usersText = (editing.valid_users ?? []).join(', ');
    editorOpen = true;
  }

  async function commit() {
    if (!editing) return;
    const next = structuredClone($state.snapshot(shares));
    const record = {
      ...editing,
      valid_users: usersText.split(',').map((s) => s.trim()).filter(Boolean),
    };
    if (editingIndex < 0) next.push(record);
    else next[editingIndex] = record;
    await save(next, editingIndex < 0 ? 'Share created' : 'Share updated');
  }

  async function remove(index) {
    if (!window.confirm(`Remove the share "${shares[index].name}"? Files are not deleted.`)) return;
    const next = structuredClone($state.snapshot(shares));
    next.splice(index, 1);
    await save(next, 'Share removed');
  }

  async function save(next, message) {
    saving = true;
    try {
      await api.putShares(next);
      shares = next;
      editorOpen = false;
      toast('success', message);
    } catch (err) {
      // The backend validated and rolled back, so local state is still correct.
      toast('error', err.message);
    } finally {
      saving = false;
    }
  }

  const canSave = $derived(
    editing && editing.name.trim() && editing.path.trim()
    && (editing.public || usersText.trim().length > 0),
  );
</script>

<section class="mb-5 flex items-center justify-between">
  <h2 class="flex items-center gap-2 text-sm font-semibold">
    <Icon name="share" size={16} /> Network shares
    {#if shares.length}<span class="muted font-normal">({shares.length})</span>{/if}
  </h2>
  <button class="btn btn-primary" onclick={() => edit(-1)}>
    <Icon name="plus" size={15} /> New share
  </button>
</section>

{#if error}
  <div class="glass flex items-center gap-3 p-5 text-sm text-[var(--color-bad)]">
    <Icon name="warn" size={18} /><span>{error}</span>
  </div>
{:else if loading}
  <div class="glass h-28 animate-pulse opacity-50"></div>
{:else if !shares.length}
  <div class="glass flex flex-col items-center gap-3 p-10 text-center">
    <Icon name="share" size={26} class="muted" />
    <p class="text-sm font-medium">No shares yet</p>
    <p class="muted max-w-sm text-sm">
      A share exposes a folder over SMB to other machines on the network.
      Windows, macOS and Linux all connect without extra software.
    </p>
    <button class="btn btn-primary mt-1" onclick={() => edit(-1)}>Create one</button>
  </div>
{:else}
  <div class="flex flex-col gap-3">
    {#each shares as s, i (s.name)}
      <article class="glass flex flex-wrap items-center gap-4 p-4">
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <h3 class="font-medium">{s.name}</h3>
            {#if s.read_only}<span class="chip muted">read-only</span>{/if}
            {#if s.public}
              <span class="chip text-[var(--color-warn)]">public</span>
            {:else}
              <span class="chip muted">{(s.valid_users ?? []).length} user(s)</span>
            {/if}
            {#if s.time_machine}<span class="chip muted">Time Machine</span>{/if}
            {#if s.recycle_bin}<span class="chip muted">recycle bin</span>{/if}
          </div>
          <p class="muted mt-1 font-mono text-xs">{s.path}</p>
          {#if s.comment}<p class="muted mt-0.5 text-xs">{s.comment}</p>{/if}
        </div>

        <div class="flex items-center gap-2">
          <button class="btn" onclick={() => edit(i)}>
            <Icon name="settings" size={14} /> Edit
          </button>
          <button class="btn !p-2" onclick={() => remove(i)} aria-label="Remove {s.name}">
            <Icon name="trash" size={14} class="text-[var(--color-bad)]" />
          </button>
        </div>
      </article>
    {/each}
  </div>
{/if}

<Modal bind:open={editorOpen} size="md"
       title={editingIndex < 0 ? 'New share' : `Edit ${editing?.name}`}>
  {#if editing}
    <div class="flex flex-col gap-4">
      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">Share name</span>
        <input class="field" bind:value={editing.name} spellcheck="false"
               placeholder="media" maxlength="32" />
        <span class="muted text-xs">
          How it appears on the network: <code class="font-mono">\{'{'}host{'}'}\{editing.name || '…'}</code>
        </span>
      </label>

      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">Folder</span>
        <input class="field font-mono" bind:value={editing.path} spellcheck="false"
               list="share-roots" placeholder="/mnt/storage/media" />
        <datalist id="share-roots">
          {#each mountPoints as m (m)}<option value={m}></option>{/each}
        </datalist>
        <span class="muted text-xs">
          Must be under /mnt/storage or /var/lib/homeos/data.
        </span>
      </label>

      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">Description (optional)</span>
        <input class="field" bind:value={editing.comment} maxlength="120"
               placeholder="Films and series" />
      </label>

      <div class="flex flex-col gap-2 rounded-xl bg-[rgb(var(--ink-muted)/0.08)] p-3">
        <label class="flex items-center gap-2.5 text-sm">
          <input type="checkbox" bind:checked={editing.public} />
          <span>Allow anyone on the network (no password)</span>
        </label>

        {#if editing.public}
          <p class="flex items-start gap-2 text-xs text-[var(--color-warn)]">
            <Icon name="warn" size={14} class="mt-0.5 shrink-0" />
            <span>
              Every device on your LAN can read this folder, including anything
              that joins your Wi-Fi.
            </span>
          </p>
        {:else}
          <label class="flex flex-col gap-1.5">
            <span class="muted text-xs font-medium">Allowed users</span>
            <input class="field" bind:value={usersText} spellcheck="false"
                   placeholder="marco, anna" />
            <span class="muted text-xs">
              Comma-separated system users. Add them with
              <code class="font-mono">sudo smbpasswd -a &lt;user&gt;</code> first.
            </span>
          </label>
        {/if}
      </div>

      <div class="flex flex-col gap-2">
        <label class="flex items-center gap-2.5 text-sm">
          <input type="checkbox" bind:checked={editing.read_only} />
          <span>Read-only</span>
        </label>
        <label class="flex items-center gap-2.5 text-sm">
          <input type="checkbox" bind:checked={editing.browseable} />
          <span>Visible when browsing the network</span>
        </label>
        <label class="flex items-center gap-2.5 text-sm">
          <input type="checkbox" bind:checked={editing.recycle_bin} disabled={editing.read_only} />
          <span>Recycle bin — deleted files are recoverable</span>
        </label>
        <label class="flex items-center gap-2.5 text-sm">
          <input type="checkbox" bind:checked={editing.time_machine} />
          <span>Time Machine target (macOS backups)</span>
        </label>
      </div>
    </div>
  {/if}

  {#snippet footer()}
    <button class="btn" onclick={() => (editorOpen = false)}>Cancel</button>
    <button class="btn btn-primary" onclick={commit} disabled={!canSave || saving}>
      {saving ? 'Saving…' : editingIndex < 0 ? 'Create share' : 'Save changes'}
    </button>
  {/snippet}
</Modal>
