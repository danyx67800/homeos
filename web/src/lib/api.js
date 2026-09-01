/**
 * REST client for the HomeOS backend.
 *
 * Same-origin in production: Caddy serves this bundle and proxies /api to the
 * daemon on loopback. In development Vite's proxy fakes that, so no base URL is
 * ever configured and there is no environment to get wrong.
 */

const TOKEN_KEY = 'homeos.token';

/** @type {Set<() => void>} */
const unauthorizedHandlers = new Set();

export function onUnauthorized(fn) {
  unauthorizedHandlers.add(fn);
  return () => unauthorizedHandlers.delete(fn);
}

export function getToken() {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? '';
  } catch {
    // Private browsing throws on access rather than returning null.
    return '';
  }
}

export function setToken(token) {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token);
    else localStorage.removeItem(TOKEN_KEY);
  } catch { /* the session simply will not survive a reload */ }
}

/** Thrown for any non-2xx response, carrying the message the API wrote. */
export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function request(method, path, body, opts = {}) {
  const headers = { Accept: 'application/json' };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let res;
  try {
    res = await fetch(`/api/v1${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: opts.signal,
    });
  } catch (err) {
    if (err?.name === 'AbortError') throw err;
    // A dead box and a dropped Wi-Fi look identical from here, so the message
    // says what the user can check rather than guessing which it was.
    throw new ApiError(0, 'Cannot reach the server. Check that HomeOS is running.');
  }

  if (res.status === 401) {
    // The session is gone: drop it and let the shell show the login view. The
    // error still propagates so the caller can stop whatever it was doing.
    setToken('');
    unauthorizedHandlers.forEach((fn) => fn());
    throw new ApiError(401, 'Session expired. Please sign in again.');
  }

  if (res.status === 204) return null;

  const text = await res.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = { error: text.slice(0, 300) };
    }
  }

  if (!res.ok) {
    throw new ApiError(res.status, payload?.error || `Request failed (${res.status})`);
  }
  return payload;
}

const get = (p, o) => request('GET', p, undefined, o);
const post = (p, b, o) => request('POST', p, b ?? {}, o);
const put = (p, b, o) => request('PUT', p, b ?? {}, o);
const del = (p, o) => request('DELETE', p, undefined, o);

export const api = {
  // --- session ---
  health: () => get('/health'),
  setupStatus: () => get('/setup/status'),
  setup: (username, password) => post('/setup', { username, password }),
  login: (username, password) => post('/auth/login', { username, password }),
  logout: () => post('/auth/logout'),
  me: () => get('/auth/me'),
  changePassword: (current, next) => post('/auth/password', { current, next }),

  // --- system ---
  systemInfo: () => get('/system/info'),
  metrics: () => get('/system/metrics'),
  metricsHistory: (samples = 120) => get(`/system/metrics/history?samples=${samples}`),
  power: (action) => post(`/system/power/${action}`),

  // --- containers and apps ---
  containers: (all = false) => get(`/containers${all ? '?all=true' : ''}`),
  containerAction: (id, action) => post(`/containers/${id}/${action}`),
  removeContainer: (id, force = false) => del(`/containers/${id}${force ? '?force=true' : ''}`),
  logs: (id, tail = 300) => get(`/containers/${id}/logs?tail=${tail}`),
  apps: () => get('/apps'),

  // --- store ---
  store: (category) => get(`/store${category ? `?category=${encodeURIComponent(category)}` : ''}`),
  storeApp: (id) => get(`/store/${id}`),
  storeIconUrl: (id) => `/api/v1/store/${id}/icon`,
  install: (id, env) => post(`/store/${id}/install`, { env }),
  uninstall: (id, purge = false) => del(`/store/${id}${purge ? '?purge=true' : ''}`),
  refreshStore: () => post('/store/refresh'),
  jobs: () => get('/store/jobs'),

  // --- updates ---
  updateStatus: () => get('/system/update'),
  updateCheck: () => post('/system/update/check'),
  updateDownload: () => post('/system/update/download'),
  updateApply: (version) => post('/system/update/apply', { version }),

  // --- storage ---
  disks: () => get('/storage/disks'),
  diskHealth: (device, force = false) =>
    get(`/storage/disks/${device.replace(/^\/dev\//, '')}/health${force ? '?force=true' : ''}`),
  format: (device, filesystem, label) =>
    // `confirm` repeats the device path; the API refuses the request without
    // it, so a mistyped device in a form cannot wipe a disk.
    post('/storage/format', { device, filesystem, label, confirm: device }),
  mount: (device, name, persist = true) => post('/storage/mount', { device, name, persist }),
  unmount: (name) => post('/storage/unmount', { name }),

  // --- shares ---
  shares: () => get('/shares'),
  putShares: (shares) => put('/shares', { shares }),
};
