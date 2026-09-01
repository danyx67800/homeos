/**
 * Application state.
 *
 * Runes in a .svelte.js module, so every view reads the same objects and the
 * telemetry stream has exactly one place to write to. No store subscriptions to
 * unsubscribe and no prop-drilling through the shell.
 */

import { api, getToken, setToken, onUnauthorized } from './api.js';
import { TelemetryStream } from './stream.js';

// --------------------------------------------------------------------------
// Session
// --------------------------------------------------------------------------
export const session = $state({
  token: getToken(),
  username: '',
  needsSetup: false,
  ready: false, // the shell renders nothing until the first probe answers
});

export async function bootstrapSession() {
  try {
    const status = await api.setupStatus();
    session.needsSetup = status.needs_setup;
  } catch {
    // Unreachable backend: assume an existing install and let the login view
    // surface the connection error, rather than offering to create an admin
    // account on a box that may already have one.
    session.needsSetup = false;
  }

  if (session.token && !session.needsSetup) {
    try {
      const me = await api.me();
      session.username = me.username;
    } catch {
      session.token = '';
      setToken('');
    }
  }
  session.ready = true;
}

export function signIn(token, username) {
  setToken(token);
  session.token = token;
  session.username = username;
  session.needsSetup = false;
  telemetry.start();
}

export async function signOut() {
  try { await api.logout(); } catch { /* the token is being discarded anyway */ }
  telemetry.stop();
  setToken('');
  session.token = '';
  session.username = '';
}

onUnauthorized(() => {
  telemetry.stop();
  session.token = '';
  session.username = '';
});

// --------------------------------------------------------------------------
// Telemetry
// --------------------------------------------------------------------------
const HISTORY = 120; // 4 minutes at the 2s default, enough for a sparkline

export const telemetry = $state({
  /** @type {any} */ metrics: null,
  /** @type {any[]} */ disks: [],
  /** @type {'connecting'|'live'|'reconnecting'|'offline'} */ state: 'offline',
  history: { cpu: [], mem: [], net: [] },
  lastAt: 0,

  /** @type {TelemetryStream|null} */ _stream: null,

  start() {
    if (this._stream || !session.token) return;
    this._stream = new TelemetryStream(
      (type, data) => applyEvent(type, data),
      (s) => { telemetry.state = s; },
    );
    this._stream.connect();
    // Seed from REST so the dials are populated on the first frame instead of
    // waiting up to a full sampling interval for the first push.
    api.metricsHistory(HISTORY).then((r) => {
      for (const snap of r.samples ?? []) pushHistory(snap);
      if (r.samples?.length) telemetry.metrics = r.samples.at(-1);
    }).catch(() => { /* the stream will fill it in shortly */ });
  },

  stop() {
    this._stream?.close();
    this._stream = null;
    telemetry.state = 'offline';
  },
});

function applyEvent(type, data) {
  if (type === 'metrics') {
    telemetry.metrics = data;
    telemetry.lastAt = Date.now();
    pushHistory(data);
  } else if (type === 'disks') {
    telemetry.disks = data ?? [];
  } else if (type === 'install') {
    installs.apply(data);
  } else if (type === 'update') {
    updates.apply(data);
  }
}

function pushHistory(snap) {
  const h = telemetry.history;
  const net = (snap.network ?? []).reduce(
    (acc, n) => acc + (n.recv_bytes_per_sec ?? 0) + (n.sent_bytes_per_sec ?? 0), 0);

  h.cpu.push(snap.cpu?.usage_percent ?? 0);
  h.mem.push(snap.memory?.used_percent ?? 0);
  h.net.push(net);
  // Ring buffer by trimming the front; at 120 entries the cost of shift() is
  // irrelevant next to the readability of a plain array.
  if (h.cpu.length > HISTORY) { h.cpu.shift(); h.mem.shift(); h.net.shift(); }
}

/** The sensor the backend marked primary, or the hottest CPU-category one. */
export function primaryTemp(metrics) {
  const temps = metrics?.temperature ?? [];
  return temps.find((t) => t.primary) ?? temps.find((t) => t.category === 'cpu') ?? null;
}

/** Total storage across mounted data filesystems, for the header summary. */
export function storageTotals(metrics) {
  const fs = metrics?.filesystems ?? [];
  const total = fs.reduce((a, f) => a + (f.total_bytes ?? 0), 0);
  const used = fs.reduce((a, f) => a + (f.used_bytes ?? 0), 0);
  return { total, used, percent: total ? (used / total) * 100 : 0, count: fs.length };
}

// --------------------------------------------------------------------------
// Apps
// --------------------------------------------------------------------------
export const apps = $state({
  /** @type {any[]} */ list: [],
  loading: false,
  error: '',

  async refresh() {
    this.loading = true;
    try {
      const r = await api.apps();
      this.list = r.apps ?? [];
      this.error = '';
    } catch (err) {
      this.error = err.message;
    } finally {
      this.loading = false;
    }
  },
});

// --------------------------------------------------------------------------
// Install jobs
// --------------------------------------------------------------------------
export const installs = $state({
  /** @type {Record<string, any>} */ byApp: {},

  apply(job) {
    if (!job?.app_id) return;
    this.byApp[job.app_id] = job;

    if (job.state === 'installed' || job.state === 'removed') {
      apps.refresh();
      toast('success', job.state === 'installed'
        ? `${job.app_id} installed`
        : `${job.app_id} removed`);
      // Hold the terminal state briefly so the progress bar can finish its
      // animation, then let the tile return to its normal appearance.
      setTimeout(() => { delete this.byApp[job.app_id]; }, 4000);
    } else if (job.state === 'failed') {
      toast('error', `${job.app_id}: ${job.error || 'install failed'}`);
    }
  },

  async hydrate() {
    try {
      const r = await api.jobs();
      for (const j of r.jobs ?? []) {
        if (!j.finished) this.byApp[j.app_id] = j;
      }
    } catch { /* nothing running, or the backend is briefly unavailable */ }
  },
});

// --------------------------------------------------------------------------
// System updates
// --------------------------------------------------------------------------
export const updates = $state({
  /** @type {any} */ status: null,
  /** @type {any} */ lastApply: null,

  apply(status) {
    this.status = status;
    if (status?.state === 'staged') {
      toast('info', `${status.staged_version} is ready to install`);
    } else if (status?.state === 'failed' && status.error) {
      toast('error', `Update: ${status.error}`);
    }
  },

  async refresh() {
    try {
      const r = await api.updateStatus();
      this.status = r.status;
      this.lastApply = r.last_apply;
    } catch {
      // 503 means no channel is configured, which is a valid state, not a
      // failure worth putting in front of anyone.
      this.status = null;
    }
  },
});

// --------------------------------------------------------------------------
// Toasts
// --------------------------------------------------------------------------
let toastId = 0;
export const toasts = $state({ /** @type {any[]} */ items: [] });

/** @param {'success'|'error'|'info'} kind */
export function toast(kind, message, ttl = 5000) {
  const id = ++toastId;
  toasts.items.push({ id, kind, message });
  // Errors stay until dismissed: a failure that vanishes before it is read is
  // worse than no message at all.
  if (kind !== 'error') setTimeout(() => dismissToast(id), ttl);
  return id;
}

export function dismissToast(id) {
  const i = toasts.items.findIndex((t) => t.id === id);
  if (i >= 0) toasts.items.splice(i, 1);
}

// --------------------------------------------------------------------------
// Theme
// --------------------------------------------------------------------------
export const theme = $state({
  dark: document.documentElement.classList.contains('dark'),
  toggle() {
    this.dark = !this.dark;
    document.documentElement.classList.toggle('dark', this.dark);
    try {
      localStorage.setItem('homeos.theme', this.dark ? 'dark' : 'light');
    } catch { /* private mode: the choice just will not persist */ }
  },
});
