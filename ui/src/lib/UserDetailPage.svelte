<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { selection, clearSelection, selectWorker } from "../stores/selection.svelte";
  import {
    formatHashrate,
    formatDifficulty,
    formatAgo,
    detectHardware,
    isOpenSource,
    explorerBaseFor,
    connectionOf,
  } from "../format";
  import type { Worker, StratumClient } from "../types";

  const address = $derived(selection.user);

  // Workers belonging to this user. ckpool keys workers by full
  // workername (e.g. <btc>.label) and tracks user = the BTC string.
  const myWorkers = $derived.by<Worker[]>(() => {
    if (!address) return [];
    return (snap.data?.workers ?? []).filter((w) => w.user === address);
  });

  // Live stratum clients for those workers. ckpool's client.address
  // is the SOURCE IP, never the BTC, so we cannot filter on it. The
  // join key is workername, match the BTC bare or any "btc.label".
  const myClients = $derived.by<StratumClient[]>(() => {
    if (!address) return [];
    const prefix = address + ".";
    return (snap.data?.clients ?? []).filter(
      (c) => c.workername === address || c.workername.startsWith(prefix),
    );
  });

  // Per-worker rows joining client + worker. Worker's idle flag is
  // separate from a client being absent: a worker is "online" only
  // when a stratum_instance currently exists for it.
  const workerRows = $derived.by(() => {
    const clientByWorker = new Map<string, StratumClient>();
    for (const c of myClients) {
      const wname = c.workername || `${address}.unnamed`;
      clientByWorker.set(wname, c);
    }
    const seen = new Set<string>();
    type Row = {
      worker: string;
      hardware: string;
      openSource: boolean;
      tls: boolean;
      tlsLabel: string;
      sourceIp: string;
      hashrate1m: number;
      hashrate1h: number;
      diff: number;
      bestSession: number;
      bestEver: number;
      lastShare: number;
      online: boolean;
      idle: boolean;
    };
    const rows: Row[] = [];
    for (const w of myWorkers) {
      const c = clientByWorker.get(w.worker);
      seen.add(w.worker);
      rows.push({
        worker: w.worker,
        hardware: c ? detectHardware(c.useragent) : "offline",
        openSource: c ? isOpenSource(c.useragent) : false,
        tls: !!c && connectionOf(c.server, snap.data?.stratum_servers).tls,
        tlsLabel: c
          ? connectionOf(c.server, snap.data?.stratum_servers).label
          : "",
        sourceIp: c?.address ?? "",
        hashrate1m: c ? c.dsps1 * 2 ** 32 : 0,
        hashrate1h: c ? c.dsps60 * 2 ** 32 : 0,
        diff: c?.diff ?? w.mindiff,
        bestSession: c?.bestdiff ?? 0,
        bestEver: w.bestever,
        lastShare: c?.lastshare ?? w.lastshare,
        online: !!c,
        idle: !!(c?.idle ?? w.idle),
      });
    }
    // A live client without a worker_instance entry yet (rare,
    // can happen briefly right after a new miner connects).
    for (const c of myClients) {
      const wname = c.workername || `${address}.unnamed`;
      if (seen.has(wname)) continue;
      rows.push({
        worker: wname,
        hardware: detectHardware(c.useragent),
        openSource: isOpenSource(c.useragent),
        tls: connectionOf(c.server, snap.data?.stratum_servers).tls,
        tlsLabel: connectionOf(c.server, snap.data?.stratum_servers).label,
        sourceIp: c.address,
        hashrate1m: c.dsps1 * 2 ** 32,
        hashrate1h: c.dsps60 * 2 ** 32,
        diff: c.diff,
        bestSession: c.bestdiff,
        bestEver: 0,
        lastShare: c.lastshare,
        online: true,
        idle: c.idle,
      });
    }
    rows.sort((a, b) => b.hashrate1h - a.hashrate1h);
    return rows;
  });

  // Per-user totals: hashrate is summed across LIVE CLIENTS only,
  // not derived from user.dsps* (which decay slowly and would show
  // residual hashrate for a freshly-disconnected worker). Best ever
  // is the lifetime persistent counter from the user's workers.
  const totals = $derived.by(() => {
    const onlineCount = myClients.length;
    const hs1m = myClients.reduce((s, c) => s + c.dsps1 * 2 ** 32, 0);
    const hs5m = myClients.reduce((s, c) => s + c.dsps5 * 2 ** 32, 0);
    const hs1h = myClients.reduce((s, c) => s + c.dsps60 * 2 ** 32, 0);
    const hs24h = myClients.reduce((s, c) => s + c.dsps1440 * 2 ** 32, 0);
    const bestEver = myWorkers.reduce(
      (m, w) => Math.max(m, w.bestever || 0),
      0,
    );
    const bestSession = myClients.reduce(
      (m, c) => Math.max(m, c.bestdiff || 0),
      0,
    );
    const lastShare = Math.max(
      0,
      ...myWorkers.map((w) => w.lastshare),
      ...myClients.map((c) => c.lastshare),
    );
    return {
      hs1m, hs5m, hs1h, hs24h,
      bestEver,
      bestSession,
      onlineWorkers: onlineCount,
      totalWorkers: myWorkers.length,
      lastShare,
    };
  });


  const explorerBase = $derived(
    explorerBaseFor(snap.data?.chain?.chain, snap.data?.mempool_base_url),
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
    <div class="stat-label">User</div>
    <h2 class="addr mono">{address}</h2>
    {#if address}
      <a
        class="explorer-link"
        href="{explorerBase}/address/{address}"
        target="_blank"
        rel="noopener noreferrer"
      >View on mempool.space &rarr;</a>
    {/if}
  </header>

  <section class="totals">
    <div class="card">
      <div class="stat-label">Hashrate</div>
      <div class="stat-value">{formatHashrate(totals.hs1m)}</div>
      <div class="stat-sub">
        5m {formatHashrate(totals.hs5m)} ·
        1h {formatHashrate(totals.hs1h)} ·
        24h {formatHashrate(totals.hs24h)}
      </div>
    </div>
    <div class="card">
      <div class="stat-label">Workers</div>
      <div class="stat-value">{totals.onlineWorkers} / {totals.totalWorkers}</div>
      <div class="stat-sub">online / total seen</div>
    </div>
    <div class="card">
      <div class="stat-label">Best (session)</div>
      <div class="stat-value">{formatDifficulty(totals.bestSession)}</div>
      <div class="stat-sub">resets on disconnect or solve</div>
    </div>
    <div class="card">
      <div class="stat-label">Best (ever)</div>
      <div class="stat-value">{formatDifficulty(totals.bestEver)}</div>
      <div class="stat-sub">
        {#if totals.lastShare}
          last share {formatAgo(totals.lastShare)}
        {:else}
          no shares yet
        {/if}
      </div>
    </div>
  </section>


  <section class="card">
    <h3>Workers</h3>
    {#if workerRows.length === 0}
      <div class="empty">No worker data yet for this address.</div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Worker</th>
              <th>Hardware</th>
              <th>Status</th>
              <th class="num">Hashrate (1m)</th>
              <th class="num">Hashrate (1h)</th>
              <th class="num">Diff</th>
              <th class="num">Best (session)</th>
              <th class="num">Best (ever)</th>
              <th class="num">Last share</th>
            </tr>
          </thead>
          <tbody>
            {#each workerRows as r (r.worker)}
              <tr class:offline={!r.online} class:idle={r.idle}>
                <td>
                  <div class="wname mono">
                    <button
                      type="button"
                      class="worker-btn"
                      onclick={() => selectWorker(r.worker)}
                      title="View per-worker stats"
                    >{r.worker}</button>
                    {#if r.tls}
                      <span class="badge tls" title={r.tlsLabel}>
                        <svg viewBox="0 0 16 16" aria-hidden="true">
                          <path d="M8 1a3.5 3.5 0 0 0-3.5 3.5V7H4a1.5 1.5 0 0 0-1.5 1.5v5A1.5 1.5 0 0 0 4 15h8a1.5 1.5 0 0 0 1.5-1.5v-5A1.5 1.5 0 0 0 12 7h-.5V4.5A3.5 3.5 0 0 0 8 1Zm2 6H6V4.5a2 2 0 1 1 4 0V7Z"/>
                        </svg>
                        <span>TLS</span>
                      </span>
                    {/if}
                  </div>
                  {#if r.sourceIp}
                    <div class="src-ip mono">{r.sourceIp}</div>
                  {/if}
                </td>
                <td>
                  {r.hardware}
                  {#if r.openSource}
                    <span class="oss" title="Open source hardware and firmware">★</span>
                  {/if}
                </td>
                <td>
                  {#if !r.online}
                    <span class="status offline">offline</span>
                  {:else if r.idle}
                    <span class="status idle">idle</span>
                  {:else}
                    <span class="status online">online</span>
                  {/if}
                </td>
                <td class="num">{formatHashrate(r.hashrate1m)}</td>
                <td class="num">{formatHashrate(r.hashrate1h)}</td>
                <td class="num">{formatDifficulty(r.diff)}</td>
                <td class="num">{formatDifficulty(r.bestSession)}</td>
                <td class="num">{formatDifficulty(r.bestEver)}</td>
                <td class="num">{formatAgo(r.lastShare)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
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
  .addr {
    margin: 0;
    font-size: 1.35rem;
    font-weight: 600;
    word-break: break-all;
    line-height: 1.25;
  }
  .explorer-link {
    color: var(--accent);
    font-size: 0.9em;
    text-decoration: none;
    border-bottom: 1px dashed var(--accent-dim);
    align-self: flex-start;
    padding-bottom: 1px;
  }
  .explorer-link:hover {
    color: #ff9a5f;
    border-bottom-color: var(--accent);
    text-decoration: none;
  }
  .totals {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1rem;
  }
  @media (max-width: 900px) {
    .totals {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
  @media (max-width: 520px) {
    .totals {
      grid-template-columns: 1fr;
    }
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
  .table-wrap {
    overflow-x: auto;
  }
  .wname {
    font-size: 0.95em;
    display: flex;
    align-items: center;
    gap: 0.4em;
    flex-wrap: wrap;
  }
  .worker-btn {
    font: inherit;
    color: inherit;
    background: transparent;
    border: none;
    padding: 0;
    cursor: pointer;
    text-align: left;
    border-bottom: 1px dashed transparent;
    transition: color 0.15s, border-bottom-color 0.15s;
  }
  .worker-btn:hover,
  .worker-btn:focus-visible {
    color: var(--accent);
    border-bottom-color: var(--accent);
    outline: none;
  }
  .src-ip {
    color: var(--fg-dim);
    font-size: 0.75em;
    margin-top: 0.2em;
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
    vertical-align: middle;
  }
  .badge.tls svg {
    width: 0.9em;
    height: 0.9em;
    fill: currentColor;
    flex-shrink: 0;
  }
  .oss {
    color: var(--accent);
    font-size: 0.95em;
    margin-left: 0.3em;
    cursor: help;
  }
  .status {
    font-size: 0.78em;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.1em 0.55em;
    border-radius: 999px;
    border: 1px solid var(--border);
  }
  .status.online {
    color: var(--good);
    border-color: #2f5c48;
    background: #0f2419;
  }
  .status.idle {
    color: var(--warn);
    border-color: #5c502f;
    background: #241f10;
  }
  .status.offline {
    color: var(--fg-dim);
    background: var(--bg-alt);
  }
  tr.offline {
    opacity: 0.55;
  }
  tr.idle td {
    color: var(--fg-dim);
  }

  /* Mobile: hide Hardware, Status, Diff, Best(session) columns */
  @media (max-width: 768px) {
    table :global(th:nth-child(2)),
    table :global(td:nth-child(2)),
    table :global(th:nth-child(3)),
    table :global(td:nth-child(3)),
    table :global(th:nth-child(6)),
    table :global(td:nth-child(6)) {
      display: none;
    }
  }
  @media (max-width: 480px) {
    table :global(th:nth-child(5)),
    table :global(td:nth-child(5)),
    table :global(th:nth-child(7)),
    table :global(td:nth-child(7)) {
      display: none;
    }
    .addr {
      font-size: 1rem;
    }
  }
</style>
