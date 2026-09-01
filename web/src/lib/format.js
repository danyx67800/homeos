/** Formatting helpers. Kept in one place so units read identically everywhere. */

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

/**
 * Base-1000, matching what disk vendors print on the label. Using base-1024
 * here would make a "4 TB" disk report 3.6 TB and generate support questions
 * forever.
 */
export function bytes(n, digits = 1) {
  if (n === null || n === undefined || Number.isNaN(n)) return '—';
  if (n < 1000) return `${Math.round(n)} B`;
  let i = 0;
  let v = n;
  while (v >= 1000 && i < UNITS.length - 1) { v /= 1000; i += 1; }
  return `${v.toFixed(v >= 100 ? 0 : digits)} ${UNITS[i]}`;
}

export function bitrate(bytesPerSec) {
  if (!bytesPerSec) return '0 B/s';
  return `${bytes(bytesPerSec, 0)}/s`;
}

export function percent(v, digits = 0) {
  if (v === null || v === undefined || Number.isNaN(v)) return '—';
  return `${v.toFixed(digits)}%`;
}

/** Compact duration: 3d 4h, 4h 12m, 12m, 45s. */
export function duration(seconds) {
  if (!seconds && seconds !== 0) return '—';
  const s = Math.floor(seconds);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}

export function celsius(v) {
  return v === null || v === undefined ? '—' : `${Math.round(v)}°`;
}

export function clock(date = new Date()) {
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

export function longDate(date = new Date()) {
  return date.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' });
}

export function relative(iso) {
  if (!iso) return '—';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '—';
  const diff = Math.round((then - Date.now()) / 1000);
  const abs = Math.abs(diff);
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
  if (abs < 60) return rtf.format(Math.round(diff), 'second');
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour');
  return rtf.format(Math.round(diff / 86400), 'day');
}

/**
 * Severity for a utilisation figure, used by gauges, bars and badges so the
 * same number never reads green in one widget and amber in another.
 */
export function severity(pct, { warn = 75, bad = 90 } = {}) {
  if (pct >= bad) return 'bad';
  if (pct >= warn) return 'warn';
  return 'ok';
}

/** CPU temperature thresholds are higher than utilisation ones. */
export function tempSeverity(c) {
  if (c === null || c === undefined) return 'ok';
  return severity(c, { warn: 70, bad: 85 });
}

export const SEVERITY_CLASS = {
  ok: 'text-[var(--color-ok)]',
  warn: 'text-[var(--color-warn)]',
  bad: 'text-[var(--color-bad)]',
};

export const SEVERITY_STROKE = {
  ok: 'var(--color-ok)',
  warn: 'var(--color-warn)',
  bad: 'var(--color-bad)',
};
