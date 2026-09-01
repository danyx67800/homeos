<script>
  import { SEVERITY_STROKE } from '../lib/format.js';

  /**
   * Circular gauge for a 0-100 value.
   *
   * SVG rather than canvas: it scales to any DPI without work, and the value
   * animates through a CSS transition on stroke-dashoffset, so the browser
   * interpolates it off the main thread instead of us re-rendering on a timer.
   */
  let {
    value = 0,
    label = '',
    sublabel = '',
    severity = 'ok',
    size = 132,
    thickness = 10,
    display = null, // overrides the centre text when the unit is not a percent
  } = $props();

  const radius = $derived((size - thickness) / 2);
  const circumference = $derived(2 * Math.PI * radius);
  // The arc spans 270 degrees with a gap at the bottom, which reads as a dial
  // rather than a pie and leaves room for the label underneath.
  const SWEEP = 0.75;
  const clamped = $derived(Math.max(0, Math.min(100, value ?? 0)));
  const dash = $derived(circumference * SWEEP);
  const offset = $derived(dash * (1 - clamped / 100));
  const colour = $derived(SEVERITY_STROKE[severity] ?? SEVERITY_STROKE.ok);
</script>

<div class="flex flex-col items-center gap-2">
  <div class="relative" style="width:{size}px;height:{size}px">
    <svg width={size} height={size} viewBox="0 0 {size} {size}" role="img"
         aria-label="{label}: {display ?? Math.round(clamped) + '%'}">
      <!-- Rotated so the gap sits at the bottom centre. -->
      <g transform="rotate(135 {size / 2} {size / 2})">
        <circle
          cx={size / 2} cy={size / 2} r={radius}
          fill="none" stroke="currentColor" stroke-width={thickness}
          stroke-linecap="round" class="opacity-10"
          stroke-dasharray="{dash} {circumference}"
        />
        <circle
          cx={size / 2} cy={size / 2} r={radius}
          fill="none" stroke={colour} stroke-width={thickness}
          stroke-linecap="round"
          stroke-dasharray="{dash} {circumference}"
          stroke-dashoffset={offset}
          style="transition: stroke-dashoffset 600ms cubic-bezier(.4,0,.2,1), stroke 400ms ease"
        />
      </g>
    </svg>

    <div class="absolute inset-0 flex flex-col items-center justify-center">
      <span class="text-2xl font-semibold tabular leading-none">
        {display ?? `${Math.round(clamped)}%`}
      </span>
      {#if sublabel}
        <span class="muted mt-1 text-[11px] tabular">{sublabel}</span>
      {/if}
    </div>
  </div>

  {#if label}
    <span class="muted text-xs font-medium uppercase tracking-wide">{label}</span>
  {/if}
</div>
