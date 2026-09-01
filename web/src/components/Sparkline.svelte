<script>
  /**
   * A trend line for a fixed-length history buffer.
   *
   * Deliberately axis-free and unlabelled: at 40px tall the only readable
   * information is the shape, and adding ticks would just spend pixels on
   * decoration. The exact number lives in the gauge beside it.
   */
  let {
    values = [],
    stroke = 'var(--color-accent-400)',
    height = 40,
    fill = true,
    max = null, // null auto-scales; pass 100 to pin a percentage axis
  } = $props();

  const W = 240;

  const path = $derived.by(() => {
    if (!values || values.length < 2) return { line: '', area: '' };
    const hi = max ?? Math.max(...values, 1);
    const step = W / (values.length - 1);
    const pts = values.map((v, i) => {
      const x = i * step;
      const y = height - (Math.max(0, Math.min(hi, v)) / hi) * (height - 2) - 1;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    });
    return {
      line: `M${pts.join('L')}`,
      area: `M0,${height} L${pts.join('L')} L${W},${height} Z`,
    };
  });

  const gradientId = `spark-${Math.random().toString(36).slice(2, 9)}`;
</script>

<svg viewBox="0 0 {W} {height}" preserveAspectRatio="none"
     class="w-full" style="height:{height}px" aria-hidden="true">
  {#if fill}
    <defs>
      <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" stop-color={stroke} stop-opacity="0.28" />
        <stop offset="100%" stop-color={stroke} stop-opacity="0" />
      </linearGradient>
    </defs>
    <path d={path.area} fill="url(#{gradientId})" />
  {/if}
  <path d={path.line} fill="none" stroke={stroke} stroke-width="1.75"
        stroke-linejoin="round" stroke-linecap="round"
        vector-effect="non-scaling-stroke" />
</svg>
