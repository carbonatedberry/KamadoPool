<script lang="ts">
  import { clearSelection, selectBestSharePow } from "../stores/selection.svelte";
  import { snap } from "../stores/snapshot.svelte";
  import { formatDifficulty } from "../format";

  const data = $derived(snap.data!);
  const hash = $derived(data.best_share_hash ?? "");
  const diff = $derived(data.best_diff ?? 0);
  const currentNetDiff = $derived(data.chain?.difficulty ?? 0);
  // Network difficulty at the time the best share was found.
  // Legacy fallback: hardcoded 136.6T for pre-upgrade shares.
  const foundNetDiff = $derived(data.best_share_net_diff || 136_597_951_737_045);
  const isLegacyNetDiff = $derived(!data.best_share_net_diff);

  // Toggle: "found" = difficulty at time of finding, "current" = live network diff
  let diffView: "found" | "current" = $state("found");
  const netDiff = $derived(diffView === "found" ? foundNetDiff : currentNetDiff);

  // --- Target computation ---
  // Bitcoin's difficulty-1 target is 0x00000000FFFF << 208.
  // Target for difficulty D = diff1_target / D.
  // We work in hex strings (64 chars = 256 bits) so we can do a
  // character-by-character comparison with the share hash.
  //
  // For display we only need ~20 hex chars (the significant prefix).
  // We compute this via BigInt arithmetic for full precision.
  const diff1Target = BigInt("0x00000000FFFF0000000000000000000000000000000000000000000000000000");

  function targetHex(d: number): string {
    if (!d || d <= 0) return "f".repeat(64);
    // Scale difficulty to integer: multiply by 2^48 to preserve precision,
    // then divide. target = diff1_target / D
    // = diff1_target * 2^48 / (D * 2^48)
    const scale = BigInt(1) << BigInt(48);
    const dScaled = BigInt(Math.round(d * Number(scale)));
    if (dScaled === BigInt(0)) return "f".repeat(64);
    const t = (diff1Target * scale) / dScaled;
    let s = t.toString(16);
    // Pad to 64 hex chars.
    while (s.length < 64) s = "0" + s;
    if (s.length > 64) s = s.slice(0, 64);
    return s;
  }

  const target = $derived(targetHex(netDiff));

  // --- Per-character comparison ---
  // Walk hex chars left-to-right. For each position:
  //   green  = share char is lower than target char (share is winning here)
  //   yellow = share char is higher than target char (share fails here, needs to be lower)
  //   white  = chars are equal (keep going) OR position is past the decided point
  //
  // Once we hit a position where they differ, all subsequent chars are decided:
  //   - if share was lower → the rest is green (share already won)
  //   - if share was higher → the rest is white (doesn't matter, already lost)
  type CharInfo = { c: string; cls: string };

  // Per-char hex coloring matching the binary approach:
  //   - Positions within the network's required leading-zero hex chars:
  //     green if '0' (correct), yellow if non-zero (problem char)
  //   - Boundary nibble (first non-zero target char): compare against target
  //   - Positions past the boundary: white
  const hashChars = $derived.by((): CharInfo[] => {
    if (!hash) return [];
    return hash.split("").map((c, i) => {
      const hVal = parseInt(c, 16);
      const tVal = parseInt(target[i] ?? "f", 16);
      if (i < networkHexZeros) {
        // Must be zero for a valid block
        return { c, cls: hVal === 0 ? "z-have" : "z-need" };
      }
      if (i === networkHexZeros) {
        // Boundary char, must be <= target char
        return { c, cls: hVal <= tVal ? "z-have" : "z-need" };
      }
      // Past the significant zone
      return { c, cls: "z-rest" };
    });
  });

  // Target row: color the significant zone (leading zeros + boundary)
  // to align visually with the hash row.
  const targetChars = $derived.by((): CharInfo[] => {
    return target.split("").map((c, i) => {
      if (i <= networkHexZeros) return { c, cls: "z-have" };
      return { c, cls: "z-rest" };
    });
  });

  // Count leading zero bits in the hash (not just hex zeros, count
  // the actual zero bits in the first non-zero nibble too).
  function leadingZeroBits(h: string): number {
    let bits = 0;
    for (const c of h) {
      const v = parseInt(c, 16);
      if (v === 0) { bits += 4; continue; }
      if (v < 2) bits += 3;
      else if (v < 4) bits += 2;
      else if (v < 8) bits += 1;
      break;
    }
    return bits;
  }

  function requiredZeroBits(d: number): number {
    if (!d || d <= 0) return 0;
    return leadingZeroBits(targetHex(d));
  }

  const shareZeroBits = $derived(hash ? leadingZeroBits(hash) : requiredZeroBits(diff));
  const networkZeroBits = $derived(requiredZeroBits(netDiff));

  // How many leading hex zeros the share has / network requires (for display).
  function leadingHexZeros(h: string): number {
    let n = 0;
    for (const c of h) { if (c === "0") n++; else break; }
    return n;
  }
  const shareHexZeros = $derived(hash ? leadingHexZeros(hash) : leadingHexZeros(targetHex(diff)));
  const networkHexZeros = $derived(leadingHexZeros(target));

  // Progress: linear ratio of share difficulty to network difficulty.
  const progressPct = $derived(
    netDiff > 0 && diff > 0
      ? Math.min(100, (diff / netDiff) * 100)
      : 0,
  );

  // How many times harder the network target is.
  const diffRatio = $derived(netDiff > 0 && diff > 0 ? netDiff / diff : 0);

  // Hex → binary lookup.
  const hexToBin: Record<string, string> = {
    "0": "0000", "1": "0001", "2": "0010", "3": "0011",
    "4": "0100", "5": "0101", "6": "0110", "7": "0111",
    "8": "1000", "9": "1001", "a": "1010", "b": "1011",
    "c": "1100", "d": "1101", "e": "1110", "f": "1111",
  };

  function hexToBits(h: string): string {
    return h.split("").map(c => hexToBin[c.toLowerCase()] ?? "0000").join("");
  }

  // Per-bit coloring based on position relative to networkZeroBits:
  //   - Positions < networkZeroBits: green if '0' (correct), yellow if '1' (problem bit)
  //   - Positions >= networkZeroBits: white (past the required leading-zero zone)
  // This shows exactly which bits prevented the hash from being a valid block.
  const binaryChars = $derived.by(() => {
    if (!hash) return [];
    const hBits = hexToBits(hash);
    const out: CharInfo[] = [];
    for (let i = 0; i < hBits.length; i++) {
      if (i > 0 && i % 16 === 0) {
        out.push({ c: " ", cls: "z-sep" });
      }
      const hb = hBits[i];
      let cls: string;
      if (i < networkZeroBits) {
        // Within the zone that must be zero for a valid block
        cls = hb === "0" ? "z-have" : "z-need";
      } else {
        // Past the required zero zone, doesn't matter
        cls = "z-rest";
      }
      out.push({ c: hb, cls });
    }
    return out;
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
    <button
      type="button"
      class="pow-btn"
      onclick={selectBestSharePow}
      title="Show the block header and raw data that reproduce this share's proof of work"
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="14" height="14">
        <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>
        <polyline points="3.27 6.96 12 12.01 20.73 6.96"/>
        <line x1="12" y1="22.08" x2="12" y2="12"/>
      </svg>
      Show PoW
    </button>
  </nav>

  <header class="head">
    <div class="stat-label">Best Share Analysis</div>
    <h2>How close to a block?</h2>
  </header>

  <!-- Difficulty view toggle -->
  <section class="toggle-bar">
    <button
      type="button"
      class="toggle-btn"
      class:active={diffView === "found"}
      onclick={() => diffView = "found"}
    >
      At time of finding
    </button>
    <button
      type="button"
      class="toggle-btn"
      class:active={diffView === "current"}
      onclick={() => diffView = "current"}
    >
      Current network diff
    </button>
  </section>
  {#if diffView === "found" && isLegacyNetDiff}
    <div class="disclaimer">
      Network difficulty at time of finding was not recorded for this share. Using approximate value of 136.6T based on historical data.
    </div>
  {/if}
  {#if diffView === "current" && currentNetDiff !== foundNetDiff}
    <div class="disclaimer">
      Comparing against the <strong>current</strong> network difficulty ({formatDifficulty(currentNetDiff)}), which may differ from the difficulty when this share was found{isLegacyNetDiff ? "" : ` (${formatDifficulty(foundNetDiff)})`}.
    </div>
  {/if}

  <!-- Explanation -->
  <section class="card explainer">
    <p>
      To mine a block, you must find a hash whose <strong>numeric value</strong>
      is less than the network's target. It's not just about leading zeros,
      the entire hash must be smaller than the target. Think of it like a lottery:
      you need to roll a number below a threshold, and the threshold gets lower as
      difficulty rises.
    </p>
    <p>
      Your best share has difficulty <strong>{formatDifficulty(diff)}</strong>,
      producing a hash with <strong>{shareZeroBits}</strong> leading zero bits.
      The current network difficulty of <strong>{formatDifficulty(netDiff)}</strong>
      requires a hash with at least <strong>{networkZeroBits}</strong> leading zero bits.
      {#if diffRatio <= 1}
        Your share meets the network target, this would be a valid block!
      {:else}
        The network target is <strong>{diffRatio.toFixed(1)}x</strong> harder than your
        best share.
      {/if}
    </p>
  </section>

  <!-- Difficulty comparison cards -->
  <section class="totals">
    <div class="card">
      <div class="stat-label">Your Best Share</div>
      <div class="stat-value">{formatDifficulty(diff)}</div>
      <div class="stat-sub">{shareZeroBits} leading zero bits ({shareHexZeros} hex zeros){!hash ? " est." : ""}</div>
    </div>
    <div class="card">
      <div class="stat-label">Network Target</div>
      <div class="stat-value">{formatDifficulty(netDiff)}</div>
      <div class="stat-sub">{networkZeroBits} leading zero bits ({networkHexZeros} hex zeros)</div>
    </div>
    <div class="card">
      <div class="stat-label">Gap</div>
      <div class="stat-value" class:complete={diffRatio <= 1}>
        {diffRatio <= 1 ? "Block!" : diffRatio < 1000 ? diffRatio.toFixed(1) + "x" : formatDifficulty(diffRatio)}
      </div>
      <div class="stat-sub">
        {#if diffRatio <= 1}
          This share satisfies the network target
        {:else}
          {networkZeroBits - shareZeroBits} more leading zero {networkZeroBits - shareZeroBits === 1 ? "bit" : "bits"} needed
        {/if}
      </div>
    </div>
  </section>

  <!-- Progress bar -->
  <section class="card">
    <h3>Progress to Network Target</h3>
    <div class="progress-row">
      <div class="progress-bar">
        <div
          class="progress-fill"
          class:full={progressPct >= 100}
          style="width:{Math.min(progressPct, 100)}%"
        ></div>
      </div>
      <span class="progress-label">{progressPct.toFixed(1)}%</span>
    </div>
    <div class="stat-sub">
      {formatDifficulty(diff)} / {formatDifficulty(netDiff)}
    </div>
  </section>

  {#if hash}
    <!-- Hex comparison -->
    <section class="card">
      <h3>Share Hash vs Network Target (hex)</h3>
      <div class="compare-row">
        <span class="compare-label">YOUR HASH</span>
        <div class="hash-vis mono">
          {#each hashChars as ch}<!--
            --><span class="hc {ch.cls}">{ch.c}</span><!--
          -->{/each}
        </div>
      </div>
      <div class="compare-row">
        <span class="compare-label">TARGET</span>
        <div class="hash-vis mono target-row">
          {#each targetChars as ch}<!--
            --><span class="hc {ch.cls}">{ch.c}</span><!--
          -->{/each}
        </div>
      </div>
      <div class="hash-legend">
        <span class="legend-item"><span class="swatch have"></span> Below target (good)</span>
        <span class="legend-item"><span class="swatch need"></span> Above target (too high)</span>
        <span class="legend-item"><span class="swatch rest"></span> Remaining</span>
      </div>
    </section>

    <!-- Binary hash -->
    <section class="card">
      <h3>Share Hash (binary)</h3>
      <div class="hash-vis binary mono">
        {#each binaryChars as ch}<!--
          -->{#if ch.cls === "z-sep"}<span class="sep"> </span>{:else}<span class="hc {ch.cls}">{ch.c}</span>{/if}<!--
        -->{/each}
      </div>
      <p class="bin-explain">
        Every additional leading zero bit makes the hash <strong>2x harder</strong> to find.
        The first {networkZeroBits} bits must all be zero for a valid block.
        <span class="z-have" style="font-weight:700">Green</span> bits are already correct (zero),
        <span class="z-need" style="font-weight:700">yellow</span> bits are the ones that
        prevented this share from being a valid block.
        {#if networkZeroBits > shareZeroBits}
          The gap of <strong>{networkZeroBits - shareZeroBits} bits</strong> means the target is roughly
          <strong>2<sup>{networkZeroBits - shareZeroBits}</sup> &asymp; {Math.round(Math.pow(2, networkZeroBits - shareZeroBits)).toLocaleString()}x</strong>
          harder, matching the {diffRatio.toFixed(1)}x difficulty ratio.
        {/if}
      </p>
    </section>
  {:else}
    <div class="card">
      <div class="empty">
        Block header hash will appear here once a share is accepted after deploying this version.
        The stats above are estimated from difficulty.
      </div>
    </div>
  {/if}
</section>

<style>
  .page {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }
  .crumbs {
    margin-bottom: 0.25rem;
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.75rem;
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
  .pow-btn {
    font: inherit;
    display: inline-flex;
    align-items: center;
    gap: 0.45em;
    color: var(--accent);
    background: transparent;
    border: 1px solid var(--accent-dim);
    border-radius: 6px;
    padding: 0.4em 0.85em;
    cursor: pointer;
  }
  .pow-btn:hover {
    border-color: var(--accent);
    background: var(--bg-hover);
    text-shadow: 0 0 8px rgba(255, 122, 58, 0.4);
  }

  /* Difficulty view toggle */
  .toggle-bar {
    display: flex;
    gap: 0;
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
    width: fit-content;
  }
  .toggle-btn {
    font: inherit;
    font-size: 0.85rem;
    padding: 0.5em 1em;
    border: none;
    background: transparent;
    color: var(--fg-dim);
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
  }
  .toggle-btn:not(:last-child) {
    border-right: 1px solid var(--border);
  }
  .toggle-btn.active {
    background: var(--accent);
    color: var(--bg);
    font-weight: 600;
  }
  .toggle-btn:hover:not(.active) {
    background: var(--bg-hover);
    color: var(--fg);
  }
  .disclaimer {
    font-size: 0.82rem;
    color: rgb(160, 195, 240);
    background: rgb(20, 35, 60);
    border: 1px solid rgb(60, 100, 170);
    border-radius: 6px;
    padding: 0.5em 0.85em;
    line-height: 1.5;
  }
  .disclaimer strong {
    color: rgb(200, 220, 250);
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

  /* Explanation */
  .explainer p {
    margin: 0 0 0.6em;
    line-height: 1.6;
    color: var(--fg-dim);
  }
  .explainer p:last-child {
    margin-bottom: 0;
  }
  .explainer strong {
    color: var(--fg);
  }

  .totals {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 1rem;
  }
  @media (max-width: 700px) {
    .totals { grid-template-columns: 1fr; }
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
  .complete {
    color: var(--good);
    text-shadow: 0 0 12px rgba(92, 224, 168, 0.4);
  }

  /* Progress bar */
  .progress-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .progress-bar {
    flex: 1;
    height: 20px;
    background: var(--border);
    border-radius: 6px;
    overflow: hidden;
  }
  .progress-fill {
    height: 100%;
    background: var(--accent);
    border-radius: 6px;
    transition: width 0.5s ease;
  }
  .progress-fill.full {
    background: var(--good);
    box-shadow: 0 0 12px rgba(92, 224, 168, 0.4);
  }
  .progress-label {
    font-size: 1rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    min-width: 4em;
    text-align: right;
  }

  /* Hash comparison rows */
  .compare-row {
    margin-bottom: 0.5rem;
  }
  .compare-row:last-of-type {
    margin-bottom: 0;
  }
  .compare-label {
    display: inline-block;
    font-size: 0.72rem;
    color: var(--fg-dim);
    letter-spacing: 0.05em;
    margin-bottom: 0.2rem;
  }
  .target-row {
    opacity: 1;
  }

  /* Hash visualization */
  .hash-vis {
    font-size: 1.1rem;
    line-height: 1.8;
    letter-spacing: 0.04em;
    word-break: break-all;
  }
  .hash-vis.binary {
    font-size: 1rem;
    line-height: 1.7;
    letter-spacing: 0.02em;
  }
  .hc {
    display: inline;
  }
  .z-have {
    color: var(--good);
    text-shadow: 0 0 6px rgba(92, 224, 168, 0.35);
    font-weight: 700;
  }
  .z-need {
    color: rgb(245, 196, 71);
    text-shadow: 0 0 6px rgba(245, 196, 71, 0.3);
    font-weight: 700;
  }
  .z-rest {
    color: var(--fg);
  }
  .sep {
    display: inline;
    user-select: none;
    width: 0.3em;
  }
  .bin-explain {
    margin: 0.75rem 0 0;
    line-height: 1.6;
    color: var(--fg-dim);
    font-size: 0.88rem;
  }
  .bin-explain strong {
    color: var(--fg);
  }

  /* Legend */
  .hash-legend {
    display: flex;
    gap: 1.2rem;
    margin-top: 0.75rem;
    font-size: 0.78em;
    color: var(--fg-dim);
    flex-wrap: wrap;
  }
  .legend-item {
    display: flex;
    align-items: center;
    gap: 0.4em;
  }
  .swatch {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
  .swatch.have { background: var(--good); }
  .swatch.need { background: rgb(245, 196, 71); }
  .swatch.rest { background: var(--fg); }

  @media (max-width: 480px) {
    .toggle-bar {
      width: 100%;
    }
    .toggle-btn {
      flex: 1;
      text-align: center;
      font-size: 0.78rem;
      padding: 0.5em 0.5em;
    }
    .hash-vis {
      font-size: 0.75rem;
      letter-spacing: 0.01em;
    }
    .hash-vis.binary {
      font-size: 0.6rem;
    }
    .totals {
      grid-template-columns: 1fr;
    }
  }
</style>
