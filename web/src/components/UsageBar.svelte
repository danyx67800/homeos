<script>
  import { bytes, percent, severity, SEVERITY_STROKE } from '../lib/format.js';

  /** Horizontal capacity bar, for filesystems and disks. */
  let { used = 0, total = 0, label = '', sublabel = '' } = $props();

  const pct = $derived(total > 0 ? (used / total) * 100 : 0);
  const sev = $derived(severity(pct));
</script>

<div class="flex flex-col gap-1.5">
  <div class="flex items-baseline justify-between gap-3 text-sm">
    <span class="truncate font-medium">{label}</span>
    <span class="muted tabular shrink-0 text-xs">
      {bytes(used)} / {bytes(total)} · {percent(pct)}
    </span>
  </div>

  <div class="h-2 overflow-hidden rounded-full bg-[rgb(var(--ink-2)/0.16)]">
    <div class="h-full rounded-full transition-[width] duration-500 ease-out"
         style="width:{Math.min(100, pct)}%; background:{SEVERITY_STROKE[sev]}"></div>
  </div>

  {#if sublabel}<span class="muted text-xs">{sublabel}</span>{/if}
</div>
