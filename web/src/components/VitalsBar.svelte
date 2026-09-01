<script>
  import Icon from './Icon.svelte';
  import Sparkline from './Sparkline.svelte';
  import { telemetry, primaryTemp, storageTotals } from '../lib/stores.svelte.js';
  import { bytes, percent, celsius, severity, tempSeverity } from '../lib/format.js';

  /**
   * Every vital in one strip.
   *
   * This replaced two large gauges and four cards that between them took most
   * of a screen to say six numbers. On a machine you glance at, the readings
   * should fit above the thing you came to use — which is the app grid.
   *
   * Each reading is a number, a thin bar and a fill. The bar carries the same
   * information as the old gauge at a tenth of the height, and stays legible
   * because the number beside it is monospaced and does not move.
   */
  const m = $derived(telemetry.metrics);
  const temp = $derived(primaryTemp(m));
  const storage = $derived(storageTotals(m));

  const net = $derived(
    (m?.network ?? []).reduce((a, n) => a + (n.recv_bytes_per_sec ?? 0), 0),
  );

  // "14.4 GB" is a quantity and a unit, not a six-character string. Splitting
  // them lets the figure carry the weight and the unit recede.
  const split = (v) => {
    const m = /^([-d.,]+)s*(.*)$/.exec(String(v));
    return m ? { n: m[1], u: m[2] } : { n: String(v), u: '' };
  };

  const readings = $derived.by(() => {
    const out = [
      {
        key: 'cpu', icon: 'cpu', label: 'CPU',
        value: percent(m?.cpu?.usage_percent ?? 0),
        note: m?.cpu?.cores ? `${m.cpu.cores} cores` : '',
        fill: m?.cpu?.usage_percent ?? 0,
        sev: severity(m?.cpu?.usage_percent ?? 0),
        history: telemetry.history.cpu,
      },
      {
        key: 'mem', icon: 'memory', label: 'Memory',
        value: m ? bytes(m.memory.used_bytes) : '—',
        note: m ? `of ${bytes(m.memory.total_bytes)}` : '',
        fill: m?.memory?.used_percent ?? 0,
        sev: severity(m?.memory?.used_percent ?? 0),
        history: telemetry.history.mem,
      },
      {
        key: 'disk', icon: 'hdd', label: 'Storage',
        value: storage.total ? bytes(storage.total - storage.used) : '—',
        note: storage.total ? 'free' : 'no data disks',
        fill: storage.percent,
        sev: storage.total ? severity(storage.percent) : 'ok',
      },
      {
        key: 'net', icon: 'network', label: 'Network',
        value: bytes(net, 0) + '/s',
        note: 'down',
        fill: null,
        sev: 'ok',
        history: telemetry.history.net,
      },
    ];

    // Only where the hardware reports it. A permanent "—" for temperature on a
    // machine with no sensor is a column of nothing.
    if (temp) {
      out.splice(2, 0, {
        key: 'temp', icon: 'thermometer', label: 'Temp',
        value: celsius(temp.celsius),
        note: temp.label,
        fill: Math.min(100, temp.celsius),
        sev: tempSeverity(temp.celsius),
      });
    }
    return out;
  });

  const SEV_BAR = { ok: 'var(--color-ok)', warn: 'var(--color-warn)', bad: 'var(--color-bad)' };
</script>

<section class="panel grid grid-cols-2 divide-x divide-y
                divide-[rgb(var(--line)/var(--line-a))]
                sm:grid-cols-3 lg:divide-y-0
                lg:grid-cols-[repeat(var(--n),minmax(0,1fr))]"
         style="--n:{readings.length}">
  {#each readings as r (r.key)}
    {@const v = split(r.value)}
    <div class="flex flex-col gap-1.5 px-3.5 py-3">
      <span class="label flex items-center gap-1.5">
        <Icon name={r.icon} size={12} stroke={2} />
        {r.label}
      </span>

      <div class="flex items-baseline gap-1">
        <span class="readout text-xl font-medium leading-none">{v.n}</span>
        {#if v.u}<span class="unit">{v.u}</span>{/if}
        {#if r.note}<span class="faint ml-0.5 truncate text-[11px]">{r.note}</span>{/if}
      </div>

      {#if r.fill !== null}
        <div class="h-[3px] overflow-hidden rounded-full bg-[rgb(var(--ink-3)/0.22)]">
          <div class="h-full rounded-full transition-[width] duration-500 ease-out"
               style="width:{Math.min(100, Math.max(0, r.fill))}%; background:{SEV_BAR[r.sev]}"></div>
        </div>
      {:else if r.history}
        <Sparkline values={r.history} height={12} fill={false}
                   stroke="rgb(var(--ink-3))" />
      {/if}
    </div>
  {/each}
</section>
