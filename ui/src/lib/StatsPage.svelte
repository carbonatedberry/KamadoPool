<script lang="ts">
  import { clearSelection } from "../stores/selection.svelte";
  import { snap } from "../stores/snapshot.svelte";
  import { formatDifficulty } from "../format";

  const data = $derived(snap.data!);

  // --- Rejection reason formatting ---
  const reasonInfo: Record<string, { label: string; tip: string }> = {
    "Stale":                 { label: "Stale",                tip: "The share was mined on a block that has already been found. Usually caused by network latency." },
    "Duplicate":             { label: "Duplicate",            tip: "This exact share was already submitted. Indicates a bug in the mining software or a network retry." },
    "Above target":          { label: "Above Target",         tip: "The share difficulty was below the required minimum target set by the pool." },
    "Dupe":                  { label: "Duplicate",            tip: "This exact share was already submitted." },
    "High":                  { label: "Above Target",         tip: "The share difficulty was below the required minimum target set by the pool." },
    "Ntime out of range":    { label: "Invalid nTime",        tip: "The share timestamp was outside the acceptable range (too far in the future or past)." },
    "Invalid JobID":         { label: "Invalid Job",          tip: "The share referenced a work unit that no longer exists. Likely stale work from a slow connection." },
    "Invalid nonce2 length": { label: "Bad Nonce2",           tip: "The extranonce2 field had an unexpected length. Indicates a stratum protocol mismatch." },
    "Worker mismatch":       { label: "Worker Mismatch",      tip: "The share was submitted by a different worker than the one that requested the work." },
    "No nonce":              { label: "Missing Nonce",        tip: "The share submission was missing the required nonce field." },
    "No ntime":              { label: "Missing nTime",        tip: "The share submission was missing the required ntime field." },
    "No nonce2":             { label: "Missing Nonce2",       tip: "The share submission was missing the required extranonce2 field." },
    "No job_id":             { label: "Missing Job ID",       tip: "The share submission was missing the required job_id field." },
    "No username":           { label: "Missing Username",     tip: "The share submission was missing the worker username." },
    "Invalid array size":    { label: "Bad Params Size",      tip: "The mining.submit parameters had the wrong number of elements." },
    "Params not array":      { label: "Bad Params Format",    tip: "The mining.submit parameters were not a JSON array." },
    "Invalid version mask":  { label: "Bad Version Mask",     tip: "The version rolling mask in the share was invalid." },
  };

  function fmtReason(raw: string): string {
    return reasonInfo[raw]?.label ?? raw;
  }
  function reasonTip(raw: string): string {
    return reasonInfo[raw]?.tip ?? "";
  }

  // --- Rejection reason rows ---
  type ReasonRow = { reason: string; count: number; pct: number };

  function reasonRows(reasons: Record<string, number> | undefined): ReasonRow[] {
    if (!reasons) return [];
    const entries = Object.entries(reasons);
    const total = entries.reduce((s, [, v]) => s + v, 0);
    if (total === 0) return [];
    return entries
      .map(([reason, count]) => ({ reason, count, pct: (count / total) * 100 }))
      .sort((a, b) => b.count - a.count);
  }

  const sessionReasons = $derived(reasonRows(data.reject_reasons_session));
  const alltimeReasons = $derived(reasonRows(data.reject_reasons_alltime));

  // --- Difficulty distribution ---
  const bucketLabels = ["< 1M", "1M\u2013100M", "100M\u20131G", "1G\u2013100G", "100G\u20131T", "\u2265 1T"];
  // Fire intensity classes: none for <1M, then progressively more intense.
  const fireClasses = ["", "fire-1", "fire-2", "fire-3", "fire-4", "fire-5"];

  type BucketRow = {
    label: string;
    session: number; sessionPct: number;
    alltime: number; alltimePct: number;
  };

  const bucketRows = $derived.by((): BucketRow[] => {
    const sd = data.diff_dist_session ?? [0, 0, 0, 0, 0, 0];
    const ad = data.diff_dist_alltime ?? [0, 0, 0, 0, 0, 0];
    const sTotal = sd.reduce((s: number, v: number) => s + v, 0);
    const aTotal = ad.reduce((s: number, v: number) => s + v, 0);
    return bucketLabels.map((label, i) => ({
      label,
      session: sd[i] ?? 0,
      sessionPct: sTotal > 0 ? ((sd[i] ?? 0) / sTotal) * 100 : 0,
      alltime: ad[i] ?? 0,
      alltimePct: aTotal > 0 ? ((ad[i] ?? 0) / aTotal) * 100 : 0,
    }));
  });

  const hasDistData = $derived(
    bucketRows.some(r => r.session > 0 || r.alltime > 0),
  );


  function onKey(ev: KeyboardEvent): void {
    if (ev.key === "Escape") clearSelection();
  }
</script>

<svelte:window onkeydown={onKey} />

<section class="page">
  <nav class="crumbs">
    <button type="button" class="back" onclick={clearSelection}>
      &larr; Back to dashboard
    </button>
  </nav>

  <header class="head">
    <div class="stat-label">Share Statistics</div>
    <h2>Difficulty &amp; Rejections</h2>
  </header>

  <!-- Average Difficulty Cards -->
  <section class="totals">
    <div class="card">
      <div class="stat-label">Session Avg Difficulty</div>
      <div class="stat-value">{data.avg_diff_session ? formatDifficulty(data.avg_diff_session) : "\u2014"}</div>
      <div class="stat-sub">{(data.diff_dist_session ?? []).reduce((s: number, v: number) => s + v, 0).toLocaleString()} accepted shares</div>
    </div>
    <div class="card">
      <div class="stat-label">All-time Avg Difficulty</div>
      <div class="stat-value">{data.avg_diff_alltime ? formatDifficulty(data.avg_diff_alltime) : "\u2014"}</div>
      <div class="stat-sub">{(data.diff_dist_alltime ?? []).reduce((s: number, v: number) => s + v, 0).toLocaleString()} accepted shares</div>
    </div>
    <div class="card">
      <div class="stat-label">Session Rejected</div>
      <div class="stat-value">{(data.session_rejected ?? 0).toLocaleString()}</div>
      <div class="stat-sub">
        {#if (data.session_accepted ?? 0) > 0}
          {((data.session_rejected ?? 0) / ((data.session_accepted ?? 0) + (data.session_rejected ?? 0)) * 100).toFixed(2)}% reject rate
        {:else}
          no shares yet
        {/if}
      </div>
    </div>
    <div class="card">
      <div class="stat-label">All-time Rejected</div>
      <div class="stat-value">{(data.alltime_rejected ?? 0).toLocaleString()}</div>
      <div class="stat-sub">
        {#if (data.alltime_accepted ?? 0) > 0}
          {((data.alltime_rejected ?? 0) / ((data.alltime_accepted ?? 0) + (data.alltime_rejected ?? 0)) * 100).toFixed(2)}% reject rate
        {:else}
          no shares yet
        {/if}
      </div>
    </div>
  </section>

  <!-- Rejection Reasons -->
  <div class="section-pair">
    <section class="card">
      <h3>Rejection Reasons (Session)</h3>
      {#if sessionReasons.length === 0}
        <div class="empty">No rejected shares this session</div>
      {:else}
        <div class="table-wrap">
          <table>
            <thead><tr><th>Reason</th><th class="num">Count</th><th class="num">%</th></tr></thead>
            <tbody>
              {#each sessionReasons as r}
                <tr title={reasonTip(r.reason)}>
                  <td>{fmtReason(r.reason)}</td>
                  <td class="num">{r.count.toLocaleString()}</td>
                  <td class="num">{r.pct.toFixed(1)}%</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
    <section class="card">
      <h3>Rejection Reasons (All-time)</h3>
      {#if alltimeReasons.length === 0}
        <div class="empty">No rejected shares recorded</div>
      {:else}
        <div class="table-wrap">
          <table>
            <thead><tr><th>Reason</th><th class="num">Count</th><th class="num">%</th></tr></thead>
            <tbody>
              {#each alltimeReasons as r}
                <tr title={reasonTip(r.reason)}>
                  <td>{fmtReason(r.reason)}</td>
                  <td class="num">{r.count.toLocaleString()}</td>
                  <td class="num">{r.pct.toFixed(1)}%</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  </div>

  <!-- Difficulty Distribution Table -->
  <section class="card">
    <h3>Difficulty Distribution - Amount of shares in different difficulty ranges</h3>
    {#if !hasDistData}
      <div class="empty">No accepted shares yet</div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Range</th>
              <th class="num">Session</th>
              <th class="num">%</th>
              <th class="num">All-time</th>
              <th class="num">%</th>
            </tr>
          </thead>
          <tbody>
            {#each bucketRows as row, i}
              {@const hasShares = row.session > 0 || row.alltime > 0}
              <tr class:dim-row={!hasShares && !fireClasses[i]}>
                <td>
                  {#if fireClasses[i]}
                    <span class="fire-label {fireClasses[i]}" class:fire-active={hasShares} class:fire-dimmed={!hasShares}>{row.label}</span>
                  {:else}
                    {row.label}
                  {/if}
                </td>
                <td class="num">{row.session.toLocaleString()}</td>
                <td class="num">{row.sessionPct.toFixed(1)}%</td>
                <td class="num">{row.alltime.toLocaleString()}</td>
                <td class="num">{row.alltimePct.toFixed(1)}%</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

</section>

<style>
  /* Page layout, identical to UserDetailPage / WorkerDetailPage */
  .page {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }
  .crumbs {
    margin-bottom: 0.25rem;
  }
  .back {
    font: inherit;
    color: var(--fg-dim);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.4em 0.85em;
    cursor: pointer;
  }
  .back:hover {
    color: var(--fg);
    border-color: var(--accent);
    background: var(--bg-hover);
  }
  .head {
    display: flex;
    flex-direction: column;
    gap: 0.4em;
    margin-bottom: 0.25rem;
  }
  .head h2 {
    margin: 0;
    font-size: 1.35rem;
    font-weight: 600;
    line-height: 1.25;
  }

  /* Stat cards, matches .totals / .stats in other pages */
  .totals {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1rem;
  }
  @media (max-width: 900px) {
    .totals { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
  @media (max-width: 520px) {
    .totals { grid-template-columns: 1fr 1fr; }
  }
  @media (max-width: 360px) {
    .totals { grid-template-columns: 1fr; }
  }

  .section-pair {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 1.25rem;
  }
  @media (max-width: 700px) {
    .section-pair { grid-template-columns: 1fr; }
  }

  h3 {
    margin: 0 0 1rem;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .empty {
    color: var(--fg-dim);
    padding: 1rem 0;
  }

  /* Tables */
  .table-wrap { overflow-x: auto; }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th {
    text-align: left;
    font-weight: 600;
    color: var(--fg-dim);
    border-bottom: 1px solid var(--border);
    padding: 0.5em 0.6em;
    font-size: 0.78em;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  td {
    padding: 0.5em 0.6em;
    border-bottom: 1px solid rgba(255, 255, 255, 0.04);
  }
  .num {
    text-align: right !important;
    font-variant-numeric: tabular-nums;
  }
  tr[title] { cursor: help; }
  tr[title]:hover td { color: var(--accent); }
  .dim-row td { opacity: 0.35; }

  /* --- Glowing range labels --- */
  .fire-label {
    display: inline-block;
    font-weight: 600;
  }
  .fire-dimmed { opacity: 0.2; }
  .fire-active { animation: glow-pulse 2s ease-in-out infinite alternate; }

  .fire-1.fire-active { color: #ffcc80; --glow: 255, 180, 100; }
  .fire-2.fire-active { color: #ff9a5c; --glow: 255, 140, 60; }
  .fire-3.fire-active { color: #ff6e40; --glow: 255, 90, 30; }
  .fire-4.fire-active { color: #f44336; --glow: 230, 40, 20; }
  .fire-5.fire-active { color: #c62828; --glow: 180, 20, 10; }

  @keyframes glow-pulse {
    0%   { text-shadow: 0 0 8px rgba(var(--glow), 0.4); }
    100% { text-shadow: 0 0 8px rgba(var(--glow), 0.85),
                        0 0 16px rgba(var(--glow), 0.3); }
  }

</style>
