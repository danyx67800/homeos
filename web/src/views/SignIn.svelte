<script>
  import Icon from '../components/Icon.svelte';
  import { api } from '../lib/api.js';
  import { session, signIn, toast } from '../lib/stores.svelte.js';

  /**
   * Login, and the first-run wizard when no admin account exists yet.
   *
   * One component for both because they are the same form with a different
   * verb, and splitting them would duplicate the error handling that is the
   * only interesting part.
   */
  let username = $state('');
  let password = $state('');
  let confirm = $state('');
  let busy = $state(false);
  let error = $state('');

  const setup = $derived(session.needsSetup);
  const tooShort = $derived(setup && password.length > 0 && password.length < 10);
  const mismatch = $derived(setup && confirm.length > 0 && confirm !== password);
  const canSubmit = $derived(
    !busy && username.trim().length >= (setup ? 3 : 1) && password.length > 0
    && (!setup || (password.length >= 10 && confirm === password)),
  );

  async function submit(event) {
    event.preventDefault();
    if (!canSubmit) return;
    busy = true;
    error = '';
    try {
      if (setup) {
        await api.setup(username.trim(), password);
        toast('success', 'Admin account created');
      }
      const r = await api.login(username.trim(), password);
      signIn(r.token, username.trim());
    } catch (err) {
      error = err.message;
      password = '';
      confirm = '';
    } finally {
      busy = false;
    }
  }
</script>

<div class="grid min-h-dvh place-items-center p-4">
  <div class="panel w-full max-w-sm p-7">
    <div class="mb-6 flex flex-col items-center gap-3 text-center">
      <div class="grid h-12 w-12 place-items-center rounded-2xl
                  bg-[var(--color-accent-500)] text-white">
        <Icon name={setup ? 'plus' : 'lock'} size={22} />
      </div>
      <div>
        <h1 class="text-lg font-semibold">{setup ? 'Set up HomeOS' : 'Sign in'}</h1>
        <p class="muted mt-1 text-sm">
          {setup
            ? 'Create the administrator account for this appliance.'
            : 'Enter your administrator credentials.'}
        </p>
      </div>
    </div>

    <form onsubmit={submit} class="flex flex-col gap-3">
      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">Username</span>
        <input class="field" bind:value={username} autocomplete="username"
               autocapitalize="none" spellcheck="false" required />
      </label>

      <label class="flex flex-col gap-1.5">
        <span class="muted text-xs font-medium">Password</span>
        <input class="field" type="password" bind:value={password}
               autocomplete={setup ? 'new-password' : 'current-password'} required />
        {#if setup}
          <span class="text-xs {tooShort ? 'text-[var(--color-bad)]' : 'muted'}">
            At least 10 characters.
          </span>
        {/if}
      </label>

      {#if setup}
        <label class="flex flex-col gap-1.5">
          <span class="muted text-xs font-medium">Confirm password</span>
          <input class="field" type="password" bind:value={confirm}
                 autocomplete="new-password" required />
          {#if mismatch}
            <span class="text-xs text-[var(--color-bad)]">Passwords do not match.</span>
          {/if}
        </label>
      {/if}

      {#if error}
        <p class="flex items-start gap-2 rounded-xl bg-[var(--color-bad)]/10 px-3 py-2
                  text-sm text-[var(--color-bad)]" role="alert">
          <Icon name="warn" size={16} class="mt-0.5 shrink-0" />
          <span>{error}</span>
        </p>
      {/if}

      <button class="btn btn-primary mt-1 justify-center" type="submit" disabled={!canSubmit}>
        {busy ? 'Working…' : setup ? 'Create account' : 'Sign in'}
      </button>
    </form>

    {#if setup}
      <p class="muted mt-5 text-center text-xs leading-relaxed">
        HomeOS is reachable from your local network only. This account is the
        single administrator; there is no recovery flow, so store the password
        somewhere safe.
      </p>
    {/if}
  </div>
</div>
