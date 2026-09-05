<script lang="ts">
  import { untrack } from "svelte";
  import { snap } from "../stores/snapshot.svelte";
  import { selectBestShare } from "../stores/selection.svelte";
  import {
    formatHashrate,
    formatUptime,
    formatDifficulty,
    formatWork,
    expectedBlockSeconds,
    formatDuration,
  } from "../format";

  const data = $derived(snap.data!);
  const height = $derived(data.chain?.blocks ?? 0);

  const poolShare = $derived.by(() => {
    const net = data.network_hashrate_hs;
    if (!net || net <= 0) return 0;
    return data.hashrate_hs_1m / net;
  });

  const expected = $derived.by(() => {
    const diff = data.chain?.difficulty ?? 0;
    return expectedBlockSeconds(data.hashrate_hs_1h, data.network_hashrate_hs, diff);
  });

  // Round effort: share of the expected work-to-find-a-block that the
  // pool has already submitted, at the current network difficulty.
  // Matches ckpool's own accounted_diff_shares / network_diff * 100
  // (stratifier.c:8201).
  const roundEffort = $derived.by(() => {
    const d = data.chain?.difficulty ?? 0;
    if (!d || d <= 0) return 0;
    return (data.cumulative_shares / d) * 100;
  });

  // Blocks remaining + ETA until the retarget. The predicted adjustment
  // percent comes from the backend (measured between block timestamps
  // from the epoch's first block), as does the epoch's observed mean
  // block time, projecting the ETA at the pace the network is actually
  // keeping rather than a flat 600s, which is what mempool.space shows.
  const retarget = $derived.by(() => {
    if (!height) return { remaining: 2016, eta: "", progress: 0 };
    const inEpoch = height % 2016;
    const remaining = 2016 - inEpoch;
    const avgBlockSec = data.epoch_avg_block_seconds || 600;
    const etaSec = remaining * avgBlockSec;
    const progress = (inEpoch / 2016) * 100;
    return { remaining, eta: formatDuration(etaSec), progress };
  });

  const diffPct = $derived(data.next_difficulty_percent ?? 0);
  const diffPctLabel = $derived(
    diffPct === 0
      ? "-"
      : (diffPct > 0 ? "+" : "") + diffPct.toFixed(2) + "%",
  );

  const totalHashes = $derived(data.cumulative_shares * 2 ** 32);

  // Block update latency (ZMQ → mining.notify)
  const latencyAvg = $derived(data.latency_avg_ms ?? 0);
  const latencyLast = $derived(data.latency_last_ms ?? 0);
  const latencyCount = $derived(data.latency_count ?? 0);
  const staleWork = $derived(data.stale_work_hashes ?? 0);

  // Luck: best_share / cumulative_work * 100%.
  // 100% = exactly expected, >100% = lucky, <100% = unlucky.
  const luckPct = $derived.by(() => {
    const best = data.best_diff;
    const work = data.cumulative_shares;
    if (!best || !work || work <= 0) return 0;
    return (best / work) * 100;
  });
  const luckLabel = $derived.by(() => {
    if (!data.best_diff || !data.cumulative_shares) return "-";
    if (luckPct >= 1000) return `${(luckPct / 100).toFixed(1)}x`;
    return luckPct.toFixed(0) + "%";
  });

  // Block-height flash on network-wide new block.
  let prevHeight = $state(0);
  let heightFlash = $state(false);
  $effect(() => {
    const h = height;
    const prev = untrack(() => prevHeight);
    if (h > 0 && prev > 0 && h !== prev) {
      heightFlash = true;
      setTimeout(() => { heightFlash = false; }, 1800);
    }
    prevHeight = h;
  });

  // New best share glow, derived from the persisted acked_best_diff.
  // Glows whenever best_diff > acked_best_diff. Survives restarts and
  // works even if multiple new bests are found before the user opens the UI.
  //
  // Local override: buttons set localAcked immediately so the UI reacts
  // in the same frame. The override holds until the server's snapshot
  // reflects the change (next WS push after the POST is processed).
  let localAcked = $state<number | null>(null);
  const effectiveAcked = $derived(
    localAcked !== null ? localAcked : (data.acked_best_diff ?? 0),
  );
  $effect(() => {
    const server = data.acked_best_diff ?? 0;
    if (localAcked !== null && server === localAcked) {
      localAcked = null;
    }
  });

  const bestGlow = $derived(
    (data.best_diff ?? 0) > 0 && (data.best_diff ?? 0) > effectiveAcked,
  );
  const bestPrevValue = $derived(effectiveAcked);

  function ackBest(): void {
    localAcked = data.best_diff ?? 0;
    fetch("/api/admin/ack-best", { method: "POST" });
  }

</script>

<section class="grid-5">
  <!-- Row 1 -->
  <div class="card">
    <div class="stat-label">Hashrate</div>
    <div class="stat-value">{formatHashrate(data.hashrate_hs_1m)}</div>
    <div class="stat-sub">
      5m {formatHashrate(data.hashrate_hs_5m)} · 1h {formatHashrate(data.hashrate_hs_1h)}
      · 24h {formatHashrate(data.hashrate_hs_24h)}
    </div>
  </div>

  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="card best-card"
    class:best-glow={bestGlow}
  >
    {#if bestGlow}
      <div class="best-tag">New best share! Previous: {formatDifficulty(bestPrevValue)}</div>
    {/if}
    <div class="shimmer" aria-hidden="true"></div>
    <div class="sparkles" aria-hidden="true">
      {#each {length: 6} as _, i}
        <span class="spark" style="--i:{i}"></span>
      {/each}
    </div>
    <div class="best-inner">
      <div class="stat-label">Best share</div>
      <div class="stat-value">{formatDifficulty(data.best_diff)}</div>
      <div
        class="luck-sub"
        class:lucky={luckPct > 100}
        class:unlucky={luckPct > 0 && luckPct < 100}
        title="Luck = best share / total work. 100% means your best share matches the work done. Above 100% is lucky (found a harder share than expected), below is unlucky."
      >
        <span class="luck-value">{luckLabel}</span>
        <span class="luck-label">luck</span>
      </div>
      {#if bestGlow}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="ack-btn" onclick={ackBest} title="Dismiss the new best share notification">Nice</div>
      {/if}
      {#if data.best_diff > 0}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="inspect-btn" onclick={selectBestShare} title="Inspect best share hash and leading zeros">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="14" height="14">
            <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
          </svg>
          Inspect
        </div>
      {/if}
    </div>
  </div>

  <div
    class="card"
    title="Time from receiving a new block via ZMQ to pushing mining.notify to all miners. Wasted work = hashes spent mining the old block during this window."
  >
    <div class="stat-label">Block latency</div>
    {#if latencyCount > 0}
      <div class="stat-value">{latencyAvg}<span class="unit">ms</span></div>
      <div class="stat-sub">
        last {latencyLast}ms ·
        <span class="wasted">{formatWork(staleWork)} wasted</span>
        <span class="stat-dim">({latencyCount} blocks)</span>
      </div>
    {:else}
      <div class="stat-value">-</div>
      <div class="stat-sub">waiting for the next block</div>
    {/if}
  </div>

  <div class="card">
    <div class="stat-label">Network</div>
    <div class="stat-value">{formatHashrate(data.network_hashrate_hs)}</div>
    <div class="stat-sub">pool share {(poolShare * 1e9).toFixed(2)} ppb</div>
  </div>

  <div class="card">
    <div class="stat-label">Difficulty</div>
    <div class="stat-value">{formatDifficulty(data.chain?.difficulty ?? 0)}</div>
    <div class="stat-sub">network target</div>
  </div>

  <!-- Row 2 -->
  <div class="card height-card" class:new-block={heightFlash}>
    <div class="stat-label">Block height</div>
    <div class="stat-value">{height ? height.toLocaleString() : "-"}</div>
    <div class="stat-sub">
      {data.chain?.chain === "main" ? "mainnet" : data.chain?.chain ?? "-"}
      {#if data.peer_count > 0}
        <span class="peers" title="{data.peers_in} inbound · {data.peers_out} outbound">&nbsp;· {data.peer_count} peers</span>
      {/if}
    </div>
  </div>

  <div class="card">
    <div class="stat-label">Block reward</div>
    <div class="stat-value" title="{data.next_block_reward_btc ? Math.round(data.next_block_reward_btc * 1e8).toLocaleString() + ' sats' : ''}">
      {data.next_block_reward_btc ? data.next_block_reward_btc.toFixed(4) : "-"}
      <span class="unit">BTC</span>
    </div>
    <div class="stat-sub">subsidy + fees (next block)</div>
  </div>

  <div class="card">
    <div class="stat-label">Total work</div>
    <div class="stat-value">{formatWork(totalHashes)}</div>
    <div class="stat-sub">
      round effort {roundEffort.toFixed(1)}% of current difficulty
    </div>
  </div>

  <div class="card">
    <div class="stat-label">Expected block</div>
    <div class="stat-value">{formatDuration(expected)}</div>
    <div class="stat-sub">uptime {formatUptime(data.uptime_seconds)}</div>
  </div>

  <div class="card">
    <div class="stat-label">Next adjustment</div>
    <div
      class="stat-value adj"
      class:up={diffPct > 0}
      class:down={diffPct < 0}
    >{diffPctLabel}</div>
    <div class="stat-sub">
      <div class="retarget-bar">
        <div class="retarget-fill" style="width:{retarget.progress}%"></div>
      </div>
      <span>{retarget.remaining} blocks · ~{retarget.eta}</span>
    </div>
  </div>

</section>

<style>
  .grid-5 {
    display: grid;
    grid-template-columns: repeat(5, minmax(0, 1fr));
    gap: 1rem;
  }
  @media (max-width: 1100px) {
    .grid-5 {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }
  @media (max-width: 720px) {
    .grid-5 {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
  @media (max-width: 480px) {
    .grid-5 {
      grid-template-columns: 1fr;
      gap: 0.75rem;
    }
  }

  .retarget-bar {
    height: 4px;
    background: var(--border);
    border-radius: 2px;
    overflow: hidden;
    margin: 0.4em 0 0.4em;
  }
  .retarget-fill {
    height: 100%;
    background: var(--accent);
    border-radius: 2px;
    transition: width 0.5s ease;
  }
  .unit {
    font-size: 0.65em;
    color: var(--fg-dim);
    font-weight: 400;
    margin-left: 0.15em;
  }
  .stat-dim {
    color: var(--fg-dim);
    font-size: 0.9em;
  }
  .wasted {
    color: var(--bad);
  }
  .adj.up {
    color: var(--good);
  }
  .adj.down {
    color: var(--bad);
  }

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

  /* Best share persistent glow */
  .best-card {
    position: relative;
    overflow: hidden;
    transition: border-color 0.4s, box-shadow 0.4s;
  }
  .best-inner {
    position: relative;
    z-index: 1;
  }
  .shimmer {
    position: absolute;
    inset: 0;
    pointer-events: none;
    opacity: 0;
    background: linear-gradient(
      105deg,
      transparent 20%,
      rgba(245, 196, 71, 0.08) 35%,
      rgba(255, 220, 80, 0.28) 50%,
      rgba(245, 196, 71, 0.08) 65%,
      transparent 80%
    );
  }
  .best-card.best-glow {
    padding-top: calc(1.25rem + 1.4em);
    border-color: rgba(245, 196, 71, 0.7);
    box-shadow:
      0 0 20px rgba(245, 196, 71, 0.35),
      0 0 40px rgba(245, 196, 71, 0.12),
      inset 0 0 12px rgba(245, 196, 71, 0.06);
    animation: best-border-pulse 2s ease-in-out infinite alternate;
  }
  @keyframes best-border-pulse {
    0%   { box-shadow: 0 0 16px rgba(245, 196, 71, 0.25), 0 0 30px rgba(245, 196, 71, 0.08); }
    100% { box-shadow: 0 0 24px rgba(245, 196, 71, 0.45), 0 0 50px rgba(245, 196, 71, 0.15); }
  }
  .best-card.best-glow .shimmer {
    opacity: 1;
    animation: best-shimmer 2s ease-in-out infinite;
  }
  @keyframes best-shimmer {
    0%   { transform: translateX(-100%); }
    100% { transform: translateX(100%); }
  }

  /* Floating sparkle dots */
  .sparkles {
    position: absolute;
    inset: 0;
    pointer-events: none;
    opacity: 0;
  }
  .best-card.best-glow .sparkles {
    opacity: 1;
  }
  .spark {
    position: absolute;
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: rgba(255, 220, 100, 0.9);
    box-shadow: 0 0 6px rgba(255, 220, 100, 0.6);
    animation: sparkle-float 3s ease-in-out infinite;
    animation-delay: calc(var(--i) * 0.5s);
  }
  .spark:nth-child(1) { left: 12%; top: 20%; }
  .spark:nth-child(2) { left: 85%; top: 35%; }
  .spark:nth-child(3) { left: 45%; top: 80%; }
  .spark:nth-child(4) { left: 70%; top: 15%; }
  .spark:nth-child(5) { left: 25%; top: 65%; }
  .spark:nth-child(6) { left: 90%; top: 75%; }
  @keyframes sparkle-float {
    0%, 100% { opacity: 0; transform: scale(0.5) translateY(0); }
    30%  { opacity: 1; transform: scale(1.2) translateY(-4px); }
    60%  { opacity: 0.6; transform: scale(0.8) translateY(2px); }
  }

  /* "NEW BEST SHARE!" tag */
  .best-tag {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    z-index: 2;
    padding: 0.25em 0.6em;
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    text-align: center;
    color: rgb(30, 22, 5);
    background: linear-gradient(90deg, rgba(245, 196, 71, 0.9), rgba(255, 220, 100, 0.95), rgba(245, 196, 71, 0.9));
    border-radius: 10px 10px 0 0;
  }

  /* "Nice" dismiss button */
  .ack-btn {
    display: block;
    width: fit-content;
    margin: 0.5em auto 0;
    padding: 0.25em 1em;
    font-size: 0.78rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: rgb(245, 196, 71);
    border: 1px solid rgba(245, 196, 71, 0.4);
    border-radius: 5px;
    cursor: pointer;
    transition: background 0.2s, border-color 0.2s, transform 0.15s;
  }
  .ack-btn:hover {
    background: rgba(245, 196, 71, 0.15);
    border-color: rgba(245, 196, 71, 0.7);
    transform: scale(1.05);
  }

  .inspect-btn {
    display: flex;
    align-items: center;
    gap: 0.3em;
    width: fit-content;
    margin: 0.4em auto 0;
    padding: 0.2em 0.7em;
    font-size: 0.72rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--fg-dim);
    border: 1px solid var(--border);
    border-radius: 5px;
    cursor: pointer;
    transition: color 0.2s, border-color 0.2s, background 0.2s;
  }
  .inspect-btn:hover {
    color: var(--accent);
    border-color: var(--accent);
    background: var(--bg-hover);
  }

  .height-card {
    transition: box-shadow 0.3s, border-color 0.3s;
  }
  .height-card.new-block {
    animation: height-flash 1.8s ease-out;
  }
  @keyframes height-flash {
    0% {
      border-color: var(--accent);
      box-shadow: 0 0 0 2px var(--accent), 0 0 32px rgba(255, 122, 58, 0.55);
      transform: scale(1.03);
    }
    40% {
      border-color: var(--accent);
      box-shadow: 0 0 0 1px var(--accent), 0 0 20px rgba(255, 122, 58, 0.35);
      transform: scale(1);
    }
    100% {
      border-color: var(--border);
      box-shadow: none;
      transform: scale(1);
    }
  }
</style>
