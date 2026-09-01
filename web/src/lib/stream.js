/**
 * Live telemetry transport.
 *
 * WebSocket first, Server-Sent Events as the fallback. Both carry the same
 * typed envelope from the hub — `metrics`, `disks`, `install` — so the consumer
 * never learns which transport it got.
 *
 * The token travels in the query string because neither the WebSocket
 * constructor nor EventSource lets a browser set an Authorization header. That
 * is only acceptable because the URL terminates on loopback; see
 * docs/phase2-backend.md §7.
 */

import { getToken } from './api.js';

const MAX_BACKOFF_MS = 30_000;

export class TelemetryStream {
  /**
   * @param {(type: string, data: any) => void} onEvent
   * @param {(state: 'connecting'|'live'|'reconnecting'|'offline') => void} onState
   */
  constructor(onEvent, onState) {
    this.onEvent = onEvent;
    this.onState = onState ?? (() => {});
    this.attempt = 0;
    this.closed = false;
    /** @type {WebSocket|null} */ this.ws = null;
    /** @type {EventSource|null} */ this.sse = null;
    /** @type {number|undefined} */ this.retryTimer = undefined;
    /** @type {number|undefined} */ this.staleTimer = undefined;
    // Set once a WebSocket has failed twice; SSE then becomes the only path,
    // so a proxy that mangles upgrades does not cost a failed attempt per
    // reconnect forever.
    this.preferSSE = false;
  }

  connect() {
    if (this.closed) return;
    this.onState(this.attempt === 0 ? 'connecting' : 'reconnecting');
    if (this.preferSSE) this.#openSSE();
    else this.#openWS();
  }

  close() {
    this.closed = true;
    clearTimeout(this.retryTimer);
    clearTimeout(this.staleTimer);
    this.#teardown();
    this.onState('offline');
  }

  #teardown() {
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.onerror = null;
      this.ws.onmessage = null;
      try { this.ws.close(); } catch { /* already closing */ }
      this.ws = null;
    }
    if (this.sse) {
      try { this.sse.close(); } catch { /* already closed */ }
      this.sse = null;
    }
  }

  #url(path) {
    const t = encodeURIComponent(getToken());
    return `${path}?token=${t}`;
  }

  #openWS() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    let ws;
    try {
      ws = new WebSocket(`${proto}//${location.host}${this.#url('/ws/telemetry')}`);
    } catch {
      this.#fallbackOrRetry();
      return;
    }
    this.ws = ws;

    ws.onopen = () => this.#live();
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        this.#deliver(msg.type, msg.data);
      } catch { /* a frame we cannot parse is not worth killing the stream for */ }
    };
    ws.onerror = () => { /* onclose always follows; handle it there only */ };
    ws.onclose = () => {
      if (this.closed) return;
      this.ws = null;
      this.#fallbackOrRetry();
    };
  }

  #openSSE() {
    let es;
    try {
      es = new EventSource(this.#url('/events'));
    } catch {
      this.#retry();
      return;
    }
    this.sse = es;

    es.onopen = () => this.#live();
    for (const type of ['metrics', 'disks', 'install']) {
      es.addEventListener(type, (ev) => {
        try { this.#deliver(type, JSON.parse(ev.data)); } catch { /* ignore */ }
      });
    }
    es.onerror = () => {
      // EventSource reconnects on its own, but with no visibility and no
      // backoff we control. Owning the retry keeps one policy for both
      // transports and keeps the status indicator honest.
      if (this.closed) return;
      es.close();
      this.sse = null;
      this.#retry();
    };
  }

  #live() {
    this.attempt = 0;
    this.onState('live');
    this.#armStaleTimer();
  }

  #deliver(type, data) {
    this.#armStaleTimer();
    this.onEvent(type, data);
  }

  /**
   * A TCP connection to a box that has been unplugged can stay open for
   * minutes. If no sample arrives within four sampling intervals, treat the
   * stream as dead rather than showing stale numbers as if they were live.
   */
  #armStaleTimer() {
    clearTimeout(this.staleTimer);
    this.staleTimer = setTimeout(() => {
      if (this.closed) return;
      this.#teardown();
      this.#retry();
    }, 12_000);
  }

  #fallbackOrRetry() {
    this.attempt += 1;
    if (this.attempt >= 2 && !this.preferSSE) {
      this.preferSSE = true;
      this.attempt = 0;
      this.connect();
      return;
    }
    this.#retry();
  }

  #retry() {
    if (this.closed) return;
    this.attempt += 1;
    this.onState('reconnecting');
    // Exponential backoff with jitter, so a restarting daemon is not hit by
    // every open tab at the same instant.
    const base = Math.min(1000 * 2 ** (this.attempt - 1), MAX_BACKOFF_MS);
    const delay = base * (0.7 + Math.random() * 0.6);
    clearTimeout(this.retryTimer);
    this.retryTimer = setTimeout(() => this.connect(), delay);
  }
}
