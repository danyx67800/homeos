<script>
  import Icon from '../components/Icon.svelte';
  import UpdatePanel from '../components/UpdatePanel.svelte';
  import { api } from '../lib/api.js';
  import { session, theme, telemetry, toast } from '../lib/stores.svelte.js';
  import { duration, bytes } from '../lib/format.js';

  let { info = null } = $props();

  let current = $state('');
  let next = $state('');
  let confirm = $state('');
  let changing = $state(false);

  const canChange = $derived(
    !changing && current.length > 0 && next.length >= 10 && next === confirm,
  );

  async function changePassword(event) {
    event.preventDefault();
    if (!canChange) return;
    changing = true;
    try {
      await api.changePassword(current, next);
      // Every session is invalidated server-side, so the next request will
      // 401 and drop us at the login view. Say so rather than letting it look
      // like a bug.
      toast('success', 'Password changed. Signing you out of all devices…');
      setTimeout(() => location.reload(), 1500);
    } catch (err) {
      toast('error', err.message);
    } finally {
      changing = false;
      current = next = confirm = '';
    }
  }
</script>

<!-- Two columns from lg. The panels below System are all narrower than their
     content box — stacking them left-aligned left the right half of a desktop
     screen empty and pushed the password form below the fold for no reason. -->
<div class="grid max-w-5xl grid-cols-1 gap-5 lg:grid-cols-2">
  <section class="panel p-5 lg:col-span-2">
    <h2 class="mb-4 flex items-center gap-2 text-sm font-semibold">
      <Icon name="info" size={16} /> System
    </h2>
    <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
      {#each [
        ['Hostname', info?.hostname],
        ['Address', info?.fqdn],
        ['Version', info?.version],
        ['Architecture', info?.architecture],
        ['Docker', info?.docker_version || 'unavailable'],
        ['Timezone', info?.timezone],
        ['Routing', info?.route_mode],
        ['Uptime', telemetry.metrics ? duration(telemetry.metrics.uptime_seconds) : '—'],
        ['Memory', telemetry.metrics ? bytes(telemetry.metrics.memory.total_bytes) : '—'],
      ] as [label, value] (label)}
        <div>
          <dt class="muted text-xs">{label}</dt>
          <dd class="mt-0.5 truncate font-medium">{value ?? '—'}</dd>
        </div>
      {/each}
    </dl>
  </section>

  <UpdatePanel version={info?.version} />

  <section class="panel p-5">
    <h2 class="mb-4 flex items-center gap-2 text-sm font-semibold">
      <Icon name="sun" size={16} /> Appearance
    </h2>
    <label class="flex items-center justify-between gap-4 text-sm">
      <span>
        Dark theme
        <span class="muted mt-0.5 block text-xs">
          Follows your system preference until you choose here.
        </span>
      </span>
      <button class="btn" onclick={() => theme.toggle()}>
        <Icon name={theme.dark ? 'moon' : 'sun'} size={15} />
        {theme.dark ? 'Dark' : 'Light'}
      </button>
    </label>
  </section>

  <section class="panel p-5">
    <h2 class="mb-4 flex items-center gap-2 text-sm font-semibold">
      <Icon name="lock" size={16} /> Password
    </h2>
    <form onsubmit={changePassword} class="flex max-w-sm flex-col gap-3 lg:max-w-none">
      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">Current password</span>
        <input class="field" type="password" bind:value={current}
               autocomplete="current-password" />
      </label>
      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">New password</span>
        <input class="field" type="password" bind:value={next} autocomplete="new-password" />
        <span class="muted text-xs">At least 10 characters.</span>
      </label>
      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">Confirm new password</span>
        <input class="field" type="password" bind:value={confirm} autocomplete="new-password" />
      </label>
      <p class="muted text-xs">
        Changing the password signs out every device, including this one.
      </p>
      <button class="btn btn-primary self-start" type="submit" disabled={!canChange}>
        {changing ? 'Changing…' : 'Change password'}
      </button>
    </form>
  </section>

  <p class="muted px-1 text-xs lg:col-span-2">
    Signed in as <strong>{session.username}</strong>. HomeOS accepts connections
    from your local network only.
  </p>
</div>
