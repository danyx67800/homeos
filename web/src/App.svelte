<script>
  import Header from './components/Header.svelte';
  import Icon from './components/Icon.svelte';
  import Toasts from './components/Toasts.svelte';
  import Modal from './components/Modal.svelte';
  import LogViewer from './components/LogViewer.svelte';

  import SignIn from './views/SignIn.svelte';
  import Dashboard from './views/Dashboard.svelte';
  import Store from './views/Store.svelte';
  import Storage from './views/Storage.svelte';
  import Shares from './views/Shares.svelte';
  import Settings from './views/Settings.svelte';

  import { api } from './lib/api.js';
  import {
    session, bootstrapSession, telemetry, apps, installs, toast,
  } from './lib/stores.svelte.js';

  /**
   * Shell and router.
   *
   * Routing is hash-based. Phase 1's Caddyfile already falls back to
   * index.html, so the History API would work — but a hash keeps deep links
   * correct even when the bundle is opened from a file path or served behind a
   * sub-path, and the dashboard has five destinations, not fifty.
   */
  const TABS = [
    { id: 'dashboard', label: 'Dashboard', icon: 'grid' },
    { id: 'store', label: 'Store', icon: 'store' },
    { id: 'storage', label: 'Storage', icon: 'hdd' },
    { id: 'shares', label: 'Shares', icon: 'share' },
    { id: 'settings', label: 'Settings', icon: 'settings' },
  ];

  let route = $state(parseHash());
  let info = $state(null);

  let powerOpen = $state(false);
  let logsOpen = $state(false);
  let logsApp = $state(null);

  function parseHash() {
    const raw = location.hash.replace(/^#\/?/, '');
    const [view, arg] = raw.split('/');
    return { view: TABS.some((t) => t.id === view) ? view : 'dashboard', arg: arg || null };
  }

  function navigate(view, arg = null) {
    location.hash = arg ? `#/${view}/${arg}` : `#/${view}`;
  }

  $effect(() => {
    const onHash = () => { route = parseHash(); };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  });

  // Once signed in: start the stream, load what the stream does not carry.
  $effect(() => {
    if (!session.token || !session.ready) return;
    telemetry.start();
    apps.refresh();
    installs.hydrate();
    api.systemInfo().then((i) => (info = i)).catch(() => { /* header degrades */ });
  });

  // The launcher is driven by container state, which is not on the stream.
  // A 20s refresh is enough to notice a container that died on its own without
  // making the box do pointless work.
  $effect(() => {
    if (!session.token) return;
    const id = setInterval(() => apps.refresh(), 20_000);
    return () => clearInterval(id);
  });

  $effect(() => { bootstrapSession(); });

  function openLogs(app) {
    logsApp = app;
    logsOpen = true;
  }

  async function power(action) {
    powerOpen = false;
    try {
      await api.power(action);
      toast('info', action === 'reboot'
        ? 'Rebooting. The dashboard will reconnect on its own.'
        : 'Shutting down. You will need to power the machine on by hand.');
    } catch (err) {
      toast('error', err.message);
    }
  }
</script>

{#if !session.ready}
  <!-- Deliberately blank: a spinner that flashes for 80ms is worse than
       nothing, and the probe is a single loopback request. -->
  <div class="min-h-dvh"></div>
{:else if !session.token}
  <SignIn />
{:else}
  <div class="mx-auto max-w-7xl pb-24 pt-3">
    <Header {info} onnavigate={navigate} onpower={() => (powerOpen = true)} />

    <!-- Navigation. A bottom bar on phones, where the top of a tall screen is
         out of thumb reach; inline tabs from md up. -->
    <nav class="mb-5 hidden gap-1 px-3 md:flex">
      {#each TABS as tab (tab.id)}
        <button
          class="btn {route.view === tab.id
            ? '!bg-[var(--color-accent-500)] !text-white !border-transparent' : ''}"
          onclick={() => navigate(tab.id)}
          aria-current={route.view === tab.id ? 'page' : undefined}
        >
          <Icon name={tab.icon} size={15} /> {tab.label}
        </button>
      {/each}
    </nav>

    <main class="px-3">
      {#if route.view === 'dashboard'}
        <Dashboard onnavigate={navigate} onlogs={openLogs} />
      {:else if route.view === 'store'}
        <Store focusApp={route.arg} onnavigate={navigate} />
      {:else if route.view === 'storage'}
        <Storage />
      {:else if route.view === 'shares'}
        <Shares />
      {:else if route.view === 'settings'}
        <Settings {info} />
      {/if}
    </main>

    <nav class="glass fixed inset-x-3 bottom-3 z-30 flex justify-around p-1.5 md:hidden
                [--surface-alpha:0.94] dark:[--surface-alpha:0.92]">
      {#each TABS as tab (tab.id)}
        <button
          class="flex flex-1 flex-col items-center gap-1 rounded-xl py-2 text-[11px]
                 transition-colors {route.view === tab.id
                   ? 'text-[var(--color-accent-400)]' : 'muted'}"
          onclick={() => navigate(tab.id)}
          aria-current={route.view === tab.id ? 'page' : undefined}
        >
          <Icon name={tab.icon} size={18} />
          {tab.label}
        </button>
      {/each}
    </nav>
  </div>
{/if}

<Modal bind:open={powerOpen} size="sm" title="Power">
  <p class="muted text-sm">
    The appliance keeps running your apps until it stops. Anything writing to
    disk right now is flushed first.
  </p>
  {#snippet footer()}
    <button class="btn" onclick={() => (powerOpen = false)}>Cancel</button>
    <button class="btn" onclick={() => power('reboot')}>
      <Icon name="restart" size={15} /> Restart
    </button>
    <button class="btn btn-danger" onclick={() => power('shutdown')}>
      <Icon name="power" size={15} /> Shut down
    </button>
  {/snippet}
</Modal>

<LogViewer bind:open={logsOpen} app={logsApp} />
<Toasts />
