<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { formatHashrate } from "../format";

  const W = 800;
  const H = 200;
  const PAD = { top: 10, right: 10, bottom: 24, left: 60 };
  const plotW = W - PAD.left - PAD.right;
  const plotH = H - PAD.top - PAD.bottom;

  const rawPoints = $derived(snap.data?.hashrate_history ?? []);

  // Bucket raw per-minute samples into ~96 time buckets for a readable
  // 24h chart. Averaging each bucket smooths out short-term jitter
  // without losing the trend. When we have fewer raw points than the
  // target (early in the process's life), just pass them through.
  const TARGET_POINTS = 96;
  const points = $derived.by(() => {
    if (rawPoints.length <= TARGET_POINTS) return rawPoints;
    const bucketSize = Math.ceil(rawPoints.length / TARGET_POINTS);
    const out: { t: number; v: number }[] = [];
    for (let i = 0; i < rawPoints.length; i += bucketSize) {
      let sum = 0;
      let count = 0;
      let tSum = 0;
      for (let j = i; j < Math.min(i + bucketSize, rawPoints.length); j++) {
        sum += rawPoints[j].v;
        tSum += rawPoints[j].t;
        count++;
      }
      if (count > 0) {
        out.push({ t: Math.round(tSum / count), v: sum / count });
      }
    }
    return out;
  });

  const chart = $derived.by(() => {
    if (points.length < 2) return null;

    const tMin = points[0].t;
    const tMax = points[points.length - 1].t;
    const tRange = tMax - tMin || 1;

    let vMax = 0;
    for (const p of points) {
      if (p.v > vMax) vMax = p.v;
    }
    if (vMax <= 0) vMax = 1;
    vMax *= 1.1;

    const toX = (t: number) => PAD.left + ((t - tMin) / tRange) * plotW;
    const toY = (v: number) => PAD.top + plotH - (v / vMax) * plotH;

    let line = "";
    let area = "";
    for (let i = 0; i < points.length; i++) {
      const x = toX(points[i].t).toFixed(1);
      const y = toY(points[i].v).toFixed(1);
      if (i === 0) {
        line = `M${x},${y}`;
        area = `M${x},${(PAD.top + plotH).toFixed(1)} L${x},${y}`;
      } else {
        line += ` L${x},${y}`;
        area += ` L${x},${y}`;
      }
    }
    area += ` L${toX(tMax).toFixed(1)},${(PAD.top + plotH).toFixed(1)} Z`;

    // Generate 5-7 evenly spaced Y ticks for better readability.
    const tickCount = 6;
    const yTicks: Array<{ y: number; label: string }> = [];
    for (let i = 0; i <= tickCount; i++) {
      const v = (i / tickCount) * vMax;
      yTicks.push({ y: toY(v), label: formatHashrate(v) });
    }

    const xTicks: Array<{ x: number; label: string }> = [];
    const count = Math.min(6, points.length);
    for (let i = 0; i < count; i++) {
      const idx = Math.round((i / (count - 1)) * (points.length - 1));
      const p = points[idx];
      const d = new Date(p.t * 1000);
      xTicks.push({
        x: toX(p.t),
        label: d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
      });
    }

    const last = points[points.length - 1];
    const dotX = toX(last.t);
    const dotY = toY(last.v);

    return { line, area, yTicks, xTicks, dotX, dotY };
  });
</script>

<section class="card chart-card">
  <h2>Hashrate (24h)</h2>
  {#if !chart}
    <div class="empty">Collecting data... chart appears after 2 minutes.</div>
  {:else}
    <svg viewBox="0 0 {W} {H}" preserveAspectRatio="xMidYMid meet" class="chart">
      {#each chart.yTicks as tick}
        <line
          x1={PAD.left} y1={tick.y}
          x2={W - PAD.right} y2={tick.y}
          class="grid-line"
        />
        <text x={PAD.left - 6} y={tick.y + 3} class="y-label">{tick.label}</text>
      {/each}

      <path d={chart.area} class="area" />
      <path d={chart.line} class="line" />

      <circle cx={chart.dotX} cy={chart.dotY} r="3.5" class="dot-pulse" />
      <circle cx={chart.dotX} cy={chart.dotY} r="2.5" class="dot" />

      {#each chart.xTicks as tick}
        <text x={tick.x} y={H - 4} class="x-label">{tick.label}</text>
      {/each}
    </svg>
  {/if}
</section>

<style>
  .chart-card {
    padding-bottom: 0.75rem;
  }
  h2 {
    margin: 0 0 0.75rem;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .empty {
    color: var(--fg-dim);
    padding: 1rem 0;
    text-align: center;
  }
  .chart {
    width: 100%;
    height: auto;
    display: block;
  }
  .grid-line {
    stroke: var(--border);
    stroke-width: 0.5;
    stroke-dasharray: 3 3;
  }
  .area {
    fill: var(--accent);
    opacity: 0.12;
  }
  .line {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
  }
  .y-label {
    fill: var(--fg-dim);
    font-size: 10px;
    text-anchor: end;
    font-family: "JetBrains Mono", ui-monospace, monospace;
  }
  .x-label {
    fill: var(--fg-dim);
    font-size: 10px;
    text-anchor: middle;
    font-family: "JetBrains Mono", ui-monospace, monospace;
  }
  .dot {
    fill: var(--accent);
  }
  .dot-pulse {
    fill: var(--accent);
    opacity: 0.4;
    animation: pulse 2s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { r: 3.5; opacity: 0.4; }
    50% { r: 6; opacity: 0; }
  }
</style>
