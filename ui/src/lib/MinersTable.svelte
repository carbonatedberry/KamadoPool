<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { selectUser, selectWorker } from "../stores/selection.svelte";
  import {
    formatHashrate,
    formatDifficulty,
    formatAgo,
    detectHardware,
    isOpenSource,
    btcAddressOf,
    connectionOf,
  } from "../format";
  import type { StratumClient, Worker } from "../types";

  type Row = {
    workerName: string;
    btcAddress: string;
    sourceIp: string;
    hardware: string;
    openSource: boolean;
    tls: boolean;
    tlsLabel: string;
    hashrate1m: number;
    hashrate1h: number;
    bestSession: number;
    bestEver: number;
    lastShare: number;
    difficulty: number;
    idle: boolean;
    online: boolean;
  };

  // The "address" field on a ckpool stratum_instance is the SOURCE IP
  // the connection arrived on (see connector.c:311, it's set with
  // inet_ntop). The miner's BTC payout address comes from the worker
  // name (which is the stratum username, format <btc>[.label]) or
  // from the joined worker_instance, which ckpool keys by full
  // workername with user = the BTC.
  //
  // ckpool's serverurl array gives us a second piece of info: which
  // bind the connection arrived on. connectionOf() maps that index to
  // the transport the deployment declared, so the lock badge can name
  // the certificate in play rather than just "encrypted".
  const rows = $derived.by<Row[]>(() => {
    const clients = snap.data?.clients ?? [];
    const workers = snap.data?.workers ?? [];
    const byWorker = new Map<string, Worker>();
    for (const w of workers) byWorker.set(w.worker, w);

    const seen = new Set<string>();
    const out: Row[] = [];

    for (const c of clients as StratumClient[]) {
      const wname = c.workername || `${c.address}.unnamed`;
      seen.add(wname);
      const w = byWorker.get(wname);
      const btcAddress = w?.user ?? btcAddressOf(wname);
      const conn = connectionOf(c.server, snap.data?.stratum_servers);
      out.push({
        workerName: wname,
        btcAddress,
        sourceIp: c.address,
        hardware: detectHardware(c.useragent),
        openSource: isOpenSource(c.useragent),
        tls: conn.tls,
        tlsLabel: conn.label,
        hashrate1m: c.dsps1 * 2 ** 32,
        hashrate1h: c.dsps60 * 2 ** 32,
        bestSession: c.bestdiff,
        bestEver: w?.bestever ?? 0,
        lastShare: c.lastshare,
        difficulty: c.diff,
        idle: c.idle,
        online: true,
      });
    }

    for (const w of workers) {
      if (seen.has(w.worker)) continue;
      out.push({
        workerName: w.worker,
        btcAddress: w.user,
        sourceIp: "",
        hardware: "offline",
        openSource: false,
        tls: false,
        tlsLabel: "",
        hashrate1m: 0,
        hashrate1h: 0,
        bestSession: 0,
        bestEver: w.bestever,
        lastShare: w.lastshare,
        difficulty: w.mindiff,
        idle: w.idle,
        online: false,
      });
    }

    out.sort((a, b) => b.hashrate1h - a.hashrate1h);
    return out;
  });
</script>

<section class="card">
  <div class="section-head">
    <h2>Miners</h2>
    <span class="head-meta">
      {rows.filter(r => r.online).length} online ·
      {rows.length} workers ·
      {new Set(rows.map(r => r.btcAddress)).size} users
    </span>
  </div>
  {#if rows.length === 0}
    <div class="empty">No miners connected.</div>
  {:else}
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Worker</th>
            <th>Hardware</th>
            <th class="num">Hashrate (1m)</th>
            <th class="num">Hashrate (1h)</th>
            <th class="num">Diff</th>
            <th class="num">Best (session)</th>
            <th class="num">Best (ever)</th>
            <th class="num">Last share</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as r (r.workerName)}
            <tr class:offline={!r.online} class:idle={r.idle}>
              <td>
                <div class="wname mono">
                  <button
                    type="button"
                    class="worker-btn"
                    onclick={() => selectWorker(r.workerName)}
                    title="View per-worker stats"
                  >{r.workerName}</button>
                  {#if r.tls}
                    <span class="badge tls" title={r.tlsLabel}>
                      <svg viewBox="0 0 16 16" aria-hidden="true">
                        <path d="M8 1a3.5 3.5 0 0 0-3.5 3.5V7H4a1.5 1.5 0 0 0-1.5 1.5v5A1.5 1.5 0 0 0 4 15h8a1.5 1.5 0 0 0 1.5-1.5v-5A1.5 1.5 0 0 0 12 7h-.5V4.5A3.5 3.5 0 0 0 8 1Zm2 6H6V4.5a2 2 0 1 1 4 0V7Z"/>
                      </svg>
                      <span>TLS</span>
                    </span>
                  {/if}
                </div>
                <button
                  type="button"
                  class="addr-btn mono"
                  onclick={() => selectUser(r.btcAddress)}
                  title="View per-user stats"
                >{r.btcAddress}</button>
                {#if r.sourceIp}
                  <div class="src-ip mono" title="Source IP">{r.sourceIp}</div>
                {/if}
              </td>
              <td>
                {r.hardware}
                {#if r.openSource}
                  <span class="oss" title="Open source hardware and firmware">★</span>
                {/if}
              </td>
              <td class="num">{formatHashrate(r.hashrate1m)}</td>
              <td class="num">{formatHashrate(r.hashrate1h)}</td>
              <td class="num">{formatDifficulty(r.difficulty)}</td>
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

<style>
  .section-head {
    display: flex;
    align-items: baseline;
    gap: 0.75em;
    margin-bottom: 1rem;
  }
  h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .head-meta {
    color: var(--fg-dim);
    font-size: 0.85em;
  }
  .table-wrap {
    overflow-x: auto;
  }
  .empty {
    color: var(--fg-dim);
    padding: 1rem 0;
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
  .addr-btn {
    display: inline-block;
    color: var(--accent);
    font-size: 0.82em;
    background: transparent;
    border: none;
    padding: 0;
    margin-top: 0.1em;
    cursor: pointer;
    text-align: left;
    text-decoration: none;
    border-bottom: 1px dashed var(--accent-dim);
    transition: color 0.15s, border-bottom-color 0.15s;
  }
  .addr-btn:hover,
  .addr-btn:focus-visible {
    color: #ff9a5f;
    border-bottom-color: var(--accent);
    background: transparent;
    outline: none;
  }
  .src-ip {
    color: var(--fg-dim);
    font-size: 0.72em;
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
  tr.offline {
    opacity: 0.4;
  }
  tr.idle td {
    color: var(--fg-dim);
  }

  /* Mobile: hide less critical columns */
  @media (max-width: 768px) {
    table :global(th:nth-child(2)),
    table :global(td:nth-child(2)),
    table :global(th:nth-child(5)),
    table :global(td:nth-child(5)),
    table :global(th:nth-child(6)),
    table :global(td:nth-child(6)) {
      display: none;
    }
  }
  @media (max-width: 480px) {
    table :global(th:nth-child(4)),
    table :global(td:nth-child(4)),
    table :global(th:nth-child(7)),
    table :global(td:nth-child(7)) {
      display: none;
    }
    .section-head {
      flex-direction: column;
      gap: 0.25em;
    }
  }
</style>
