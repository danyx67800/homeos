<script>
  import Icon from './Icon.svelte';
  import Sparkline from './Sparkline.svelte';
  import { SEVERITY_CLASS } from '../lib/format.js';

  let {
    icon = 'info',
    label = '',
    value = '—',
    detail = '',
    severity = 'ok',
    history = null,
    max = null,
  } = $props();
</script>

<div class="glass glass-hover flex flex-col gap-3 p-4">
  <div class="flex items-center justify-between">
    <span class="muted flex items-center gap-2 text-xs font-medium uppercase tracking-wide">
      <Icon name={icon} size={15} />
      {label}
    </span>
    {#if detail}<span class="muted tabular text-xs">{detail}</span>{/if}
  </div>

  <div class="text-2xl font-semibold tabular {SEVERITY_CLASS[severity]}
              {String(value).match(/[0-9]/) ? '' : '!text-[rgb(var(--ink-muted))] !text-base !font-medium'}">
    {value}
  </div>

  {#if history}
    <Sparkline values={history} {max}
               stroke="var(--color-{severity === 'ok' ? 'accent-400' : severity})" />
  {/if}
</div>
