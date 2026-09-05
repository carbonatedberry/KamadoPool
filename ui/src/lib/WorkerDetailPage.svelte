<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { selection, clearSelection, selectUser } from "../stores/selection.svelte";
  import {
    formatHashrate,
    formatDifficulty,
    formatWork,
    formatAgo,
    detectHardware,
    isOpenSource,
    btcAddressOf,
    connectionOf,
  } from "../format";
  import type { Worker, StratumClient } from "../types";

  const workername = $derived(selection.worker);
  const btcAddress = $derived(workername ? btcAddressOf(workername) : "");

  const worker = $derived.by<Worker | null>(() => {
    if (!workername) return null;
    return (snap.data?.workers ?? []).find((w) => w.worker === workername) ?? null;
  });

  const client = $derived.by<StratumClient | null>(() => {
    if (!workername) return null;
    return (snap.data?.clients ?? []).find((c) => c.workername === workername) ?? null;
  });

  const online = $derived(!!client);
  const idle = $derived(client?.idle ?? worker?.idle ?? false);
  const hardware = $derived(client ? detectHardware(client.useragent) : "offline");
  const openSource = $derived(client ? isOpenSource(client.useragent) : false);
  const conn = $derived(
    client
      ? connectionOf(client.server, snap.data?.stratum_servers)
      : { tls: false, label: "" },
  );
  const tls = $derived(conn.tls);

  const hs1m = $derived(client ? client.dsps1 * 2 ** 32 : 0);
  const hs5m = $derived(client ? client.dsps5 * 2 ** 32 : 0);
  const hs1h = $derived(client ? client.dsps60 * 2 ** 32 : 0);
  const hs24h = $derived(client ? client.dsps1440 * 2 ** 32 : 0);

  const bestEver = $derived(worker?.bestever ?? 0);
  const bestSession = $derived(client?.bestdiff ?? 0);
  const lastShare = $derived(client?.lastshare ?? worker?.lastshare ?? 0);

  // Per-worker cumulative work (difficulty shares accepted)
  const workerShares = $derived(worker?.shares ?? 0);
  const workerHashes = $derived(workerShares * 2 ** 32);

  // Per-worker luck: bestever / cumulative shares * 100
  const luckPct = $derived.by(() => {
    if (!bestEver || !workerShares || workerShares <= 0) return 0;
    return (bestEver / workerShares) * 100;
  });
  const luckLabel = $derived.by(() => {
    if (!bestEver || !workerShares) return "\u2014";
    if (luckPct >= 1000) return `${(luckPct / 100).toFixed(1)}x`;
    return luckPct.toFixed(0) + "%";
  });

  // Share of pool's total work contributed by this worker
  const poolShares = $derived(snap.data?.cumulative_shares ?? 0);
  const workShare = $derived.by(() => {
    if (!workerShares || !poolShares || poolShares <= 0) return 0;
    return (workerShares / poolShares) * 100;
  });


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
    <div class="stat-label">Worker</div>
    <h2 class="wname mono">{workername}</h2>
    <div class="head-meta">
      <button
        type="button"
        class="addr-btn mono"
        onclick={() => selectUser(btcAddress)}
        title="View per-user stats"
      >{btcAddress}</button>
      {#if tls}
        <span class="badge tls" title={conn.label}>
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path d="M8 1a3.5 3.5 0 0 0-3.5 3.5V7H4a1.5 1.5 0 0 0-1.5 1.5v5A1.5 1.5 0 0 0 4 15h8a1.5 1.5 0 0 0 1.5-1.5v-5A1.5 1.5 0 0 0 12 7h-.5V4.5A3.5 3.5 0 0 0 8 1Zm2 6H6V4.5a2 2 0 1 1 4 0V7Z"/>
          </svg>
          <span>TLS</span>
        </span>
      {/if}
      {#if openSource}
        <span class="oss" title="Open source hardware and firmware">&#9733;</span>
      {/if}
    </div>
  </header>

  <section class="stats">
    <div class="card">
      <div class="stat-label">Hashrate</div>
      <div class="stat-value">{formatHashrate(hs1m)}</div>
      <div class="stat-sub">
        5m {formatHashrate(hs5m)} ·
        1h {formatHashrate(hs1h)} ·
        24h {formatHashrate(hs24h)}
      </div>
    </div>

    <div class="card">
      <div class="stat-label">Status</div>
      <div class="stat-value">
        {#if !online}
          <span class="status-text offline">Offline</span>
        {:else if idle}
          <span class="status-text idle">Idle</span>
        {:else}
          <span class="status-text online">Online</span>
        {/if}
      </div>
      <div class="stat-sub">
        {hardware}
        {#if client?.address}
          · {client.address}
        {/if}
      </div>
    </div>

    <div class="card">
      <div class="stat-label">Best share</div>
      <div class="stat-value">{formatDifficulty(bestEver)}</div>
      <div
        class="luck-sub"
        class:lucky={luckPct > 100}
        class:unlucky={luckPct > 0 && luckPct < 100}
        title="Luck = best share / total work for this worker. 100% means your best share matches the work done. Above 100% is lucky, below is unlucky."
      >
        <span class="luck-value">{luckLabel}</span>
        <span class="luck-label">luck</span>
      </div>
    </div>

    <div class="card">
      <div class="stat-label">Total work</div>
      <div class="stat-value">{formatWork(workerHashes)}</div>
      <div class="stat-sub">
        {workShare.toFixed(1)}% of pool ·
        last share {formatAgo(lastShare)}
      </div>
    </div>
  </section>

  <section class="card">
    <h3>Session</h3>
    {#if !online}
      <div class="empty">Worker is offline. Last seen {formatAgo(lastShare)}.</div>
    {:else}
      <div class="detail-grid">
        <div class="detail-item">
          <span class="detail-label">Session best</span>
          <span class="detail-value">{formatDifficulty(bestSession)}</span>
        </div>
        <div class="detail-item">
          <span class="detail-label">Current diff</span>
          <span class="detail-value">{formatDifficulty(client?.diff ?? 0)}</span>
        </div>
        <div class="detail-item">
          <span class="detail-label">Min diff</span>
          <span class="detail-value">{formatDifficulty(worker?.mindiff ?? 0)}</span>
        </div>
        {#if client?.useragent}
          <div class="detail-item wide">
            <span class="detail-label">User agent</span>
            <span class="detail-value mono ua">{client.useragent}</span>
          </div>
        {/if}
      </div>
    {/if}
  </section>
</section>

<style>
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
  .wname {
    margin: 0;
    font-size: 1.35rem;
    font-weight: 600;
    word-break: break-all;
    line-height: 1.25;
  }
  .head-meta {
    display: flex;
    align-items: center;
    gap: 0.6em;
    flex-wrap: wrap;
  }
  .addr-btn {
    color: var(--accent);
    font: inherit;
    font-size: 0.9em;
    background: transparent;
    border: none;
    padding: 0;
    cursor: pointer;
    text-decoration: none;
    border-bottom: 1px dashed var(--accent-dim);
  }
  .addr-btn:hover {
    color: #ff9a5f;
    border-bottom-color: var(--accent);
  }
  .badge.tls {
    display: inline-flex;
    align-items: center;
    gap: 0.3em;
    font-size: 0.72em;
    font-weight: 600;
    letter-spacing: 0.04em;
    color: var(--good);
    border: 1px solid #3a7558;
    background: #112e22;
    padding: 0.15em 0.5em;
    border-radius: 4px;
    text-transform: uppercase;
    line-height: 1;
  }
  .badge.tls svg {
    width: 0.9em;
    height: 0.9em;
    fill: currentColor;
    flex-shrink: 0;
  }
  .oss {
    color: var(--accent);
    font-size: 1.1em;
    cursor: help;
  }
  .stats {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1rem;
  }
  @media (max-width: 900px) {
    .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
  @media (max-width: 520px) {
    .stats { grid-template-columns: 1fr; }
  }
  .status-text {
    text-transform: uppercase;
    letter-spacing: 0.03em;
  }
  .status-text.online { color: var(--good); }
  .status-text.idle { color: var(--warn, #e6c455); }
  .status-text.offline { color: var(--fg-dim); }

  .luck-sub {
    display: flex;
    align-items: baseline;
    gap: 0.4em;
    margin-top: 0.45em;
    cursor: help;
  }
  .luck-value {
    font-size: 1.15rem;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    letter-spacing: -0.01em;
  }
  .luck-label {
    color: var(--fg-dim);
    font-size: 0.85rem;
    font-weight: 400;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .luck-sub.lucky .luck-value {
    color: var(--good);
    text-shadow: 0 0 10px rgba(92, 224, 168, 0.35);
  }
  .luck-sub.unlucky .luck-value {
    color: var(--bad);
    text-shadow: 0 0 10px rgba(255, 107, 107, 0.25);
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
  .detail-grid {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 1rem 1.5rem;
  }
  .detail-item.wide {
    grid-column: 1 / -1;
  }
  @media (max-width: 600px) {
    .detail-grid { grid-template-columns: 1fr; }
  }
  .detail-label {
    display: block;
    color: var(--fg-dim);
    font-size: 0.82em;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 0.3em;
  }
  .detail-value {
    font-size: 1.05rem;
    font-weight: 500;
  }
  .ua {
    font-size: 0.85rem;
    word-break: break-all;
  }

  @media (max-width: 480px) {
    .wname {
      font-size: 1rem;
    }
    .detail-grid {
      grid-template-columns: 1fr 1fr;
    }
  }
</style>
