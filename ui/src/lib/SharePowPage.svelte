<script lang="ts">
  import { selectBestShare } from "../stores/selection.svelte";
  import { snap } from "../stores/snapshot.svelte";
  import { formatDifficulty, explorerBaseFor } from "../format";
  import PowVerifyOverlay from "./PowVerifyOverlay.svelte";
  import ShareCard from "./ShareCard.svelte";
  import type { CheckResult } from "./PowVerifyOverlay.svelte";
  import { revHex } from "./sha256";

  const data = $derived(snap.data!);
  const pow = $derived(data.best_share_pow ?? null);

  // The all-time best share may predate ckpool patch 0008 (which logs
  // header data). When the hashes differ, this page is showing the best
  // share *since* the patch went live, disclose that.
  const isNewerThanAllTime = $derived(
    !!pow && !!data.best_share_hash && pow.hash !== data.best_share_hash,
  );

  const explorerBase = $derived(
    explorerBaseFor(data.chain?.chain, data.mempool_base_url),
  );

  function u32LE(hex8: string): number {
    return parseInt(revHex(hex8), 16) >>> 0;
  }

  // ── Header field decomposition ──────────────────────────────────────
  // 80 bytes: version(4) | prev block(32) | merkle root(32) | time(4) |
  // bits(4) | nonce(4). Offsets below are hex-char offsets (bytes × 2).
  type HeaderField = {
    key: string;
    name: string;
    bytes: string;
    raw: string;
    value: string;
    sub: string;
  };

  const fields = $derived.by((): HeaderField[] => {
    if (!pow) return [];
    const h = pow.header;
    const version = u32LE(h.slice(0, 8));
    const time = u32LE(h.slice(136, 144));
    const nonce = u32LE(h.slice(152, 160));
    return [
      {
        key: "version",
        name: "Version",
        bytes: "0–3",
        raw: h.slice(0, 8),
        value: "0x" + version.toString(16).padStart(8, "0"),
        sub: "BIP9 base + version-rolling bits set by the miner (AsicBoost)",
      },
      {
        key: "prev",
        name: "Previous block",
        bytes: "4–35",
        raw: h.slice(8, 72),
        value: revHex(h.slice(8, 72)),
        sub: `links to the chain tip this share was mining on (height ${(pow.height - 1).toLocaleString()})`,
      },
      {
        key: "merkle",
        name: "Merkle root",
        bytes: "36–67",
        raw: h.slice(72, 136),
        value: revHex(h.slice(72, 136)),
        sub: "commits to the coinbase and every transaction in the block",
      },
      {
        key: "time",
        name: "Time",
        bytes: "68–71",
        raw: h.slice(136, 144),
        value: new Date(time * 1000).toLocaleString(),
        sub: `unix ${time}, the miner may roll this forward slightly`,
      },
      {
        key: "bits",
        name: "Bits",
        bytes: "72–75",
        raw: h.slice(144, 152),
        value: "0x" + revHex(h.slice(144, 152)),
        sub: "compact encoding of the network target",
      },
      {
        key: "nonce",
        name: "Nonce",
        bytes: "76–79",
        raw: h.slice(152, 160),
        value: nonce.toLocaleString(),
        sub: `0x${nonce.toString(16).padStart(8, "0")}, the value the miner iterated`,
      },
    ];
  });

  // ── Coinbase decomposition ──────────────────────────────────────────
  const cbSegments = $derived.by(() => {
    if (!pow) return [];
    const cb = pow.coinbase;
    const a = pow.cb1len * 2;
    const b = a + pow.enonce1.length;
    const c = b + pow.nonce2.length;
    return [
      { key: "cb1", name: "coinb1", hex: cb.slice(0, a), desc: "tx header, input, height, pool tag" },
      { key: "en1", name: "extranonce1", hex: cb.slice(a, b), desc: "assigned to the miner by the pool" },
      { key: "en2", name: "extranonce2", hex: cb.slice(b, c), desc: "chosen by the miner per work item" },
      { key: "cb2", name: "coinb2", hex: cb.slice(c), desc: "outputs paying the pool address" },
    ];
  });

  const branches = $derived(pow?.merklebranches ?? []);

  // ── Verification: handed to the full-screen overlay ─────────────────
  let showOverlay = $state(false);
  let showShare = $state(false);
  let checks: CheckResult[] = $state([]);
  let verified = $state(false);
  let finalHash = $state("");
  const allOk = $derived(verified && checks.every((c) => c.ok));

  function onVerifyComplete(result: CheckResult[], hash: string): void {
    checks = result;
    finalHash = hash;
    verified = true;
  }

  const finalHashChars = $derived.by(() => {
    let leading = true;
    return finalHash.split("").map((c) => {
      if (c !== "0") leading = false;
      return { c, zero: leading };
    });
  });

  // ── Copy helper (with non-secure-context fallback) ──────────────────
  let copiedKey = $state("");
  function copyText(key: string, text: string): void {
    const done = (): void => {
      copiedKey = key;
      setTimeout(() => {
        if (copiedKey === key) copiedKey = "";
      }, 1500);
    };
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(done, () => fallbackCopy(text, done));
    } else {
      fallbackCopy(text, done);
    }
  }
  function fallbackCopy(text: string, done: () => void): void {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      done();
    } finally {
      document.body.removeChild(ta);
    }
  }

  const cliSnippet = $derived(
    pow
      ? `python3 -c "import hashlib as H; h=bytes.fromhex('${pow.header}'); print(H.sha256(H.sha256(h).digest()).digest()[::-1].hex())"`
      : "",
  );

  const foundAt = $derived(pow ? new Date(pow.seen_at).toLocaleString() : "");

  function onKey(ev: KeyboardEvent): void {
    if (ev.key === "Escape" && !showOverlay && !showShare) selectBestShare();
  }
</script>

<svelte:window onkeydown={onKey} />

<section class="page">
  <nav class="crumbs">
    <button type="button" class="back" onclick={selectBestShare}>
      &larr; Best share analysis
    </button>
  </nav>

  <header class="head">
    <div class="stat-label">Best Share Proof of Work</div>
    <h2>The block header behind the share</h2>
  </header>

  {#if !pow}
    <div class="card">
      <div class="empty">
        <p>
          Header capture was not active when the current best share was found, and CKPool
          discards job data once work moves on, so the block header of that share can
          no longer be reconstructed.
        </p>
        <p>
          The pool now records the full header, coinbase, and merkle branches as shares come
          in. The next accepted share will populate this page, which then tracks the best
          share found since header capture was enabled, it does not wait for the
          all-time record to be beaten. Once a share does beat the all-time best, the two
          converge and this page shows the overall best share.
        </p>
      </div>
    </div>
  {:else}
    {#if isNewerThanAllTime}
      <div class="disclaimer">
        Showing the best share <strong>since header capture was enabled</strong>
        ({formatDifficulty(pow.sdiff)}, found {foundAt}). The all-time best share
        ({formatDifficulty(data.best_diff)}) predates this feature, and its block header
        was not recorded and cannot be reconstructed retroactively.
      </div>
    {/if}

    <!-- Summary -->
    <section class="totals">
      <div class="card">
        <div class="stat-label">Share Difficulty</div>
        <div class="stat-value">{formatDifficulty(pow.sdiff)}</div>
        <div class="stat-sub">network was {formatDifficulty(pow.netdiff)} at the time</div>
      </div>
      <div class="card">
        <div class="stat-label">Mining Block</div>
        <div class="stat-value">#{pow.height.toLocaleString()}</div>
        <div class="stat-sub">the height this share was trying to solve</div>
      </div>
      <div class="card">
        <div class="stat-label">Found By</div>
        <div class="stat-value worker-name mono">{pow.workername || "-"}</div>
        <div class="stat-sub">{foundAt}</div>
      </div>
    </section>

    <!-- Block header visualization -->
    <section class="card">
      <h3>Block Header, 80 bytes</h3>
      <p class="section-intro">
        This is the block this share was building, drawn as it exists on the wire. The
        header's six fields are serialized little-endian into 80 bytes; the
        <span class="seg-text-prev">previous block</span> field is what chains it to the
        current tip.
      </p>

      <div class="chain-row">
        <div class="ghost-block" aria-hidden="true">
          <div class="ghost-cap">BLOCK #{(pow.height - 1).toLocaleString()}</div>
          <div class="ghost-body">current chain tip</div>
        </div>
        <div class="chain-link" aria-hidden="true">
          <span class="chain-arrow">&larr;</span>
          <span class="chain-label mono">prev_hash</span>
        </div>

        <div class="block3d">
          <div class="block-cap">
            <span class="block-title">BLOCK #{pow.height.toLocaleString()}</span>
            <span class="block-cap-sub">header &middot; 80 bytes</span>
          </div>
          {#each fields as f (f.key)}
            <div class="slot">
              <div class="slot-stripe seg-{f.key}"></div>
              <div class="slot-main">
                <div class="slot-head">
                  <span class="slot-name">{f.name}</span>
                  <span class="slot-bytes">bytes {f.bytes}</span>
                </div>
                <div class="slot-value mono">
                  {#if f.key === "prev"}
                    <a href="{explorerBase}/block/{f.value}" target="_blank" rel="noopener">{f.value}</a>
                  {:else}
                    {f.value}
                  {/if}
                </div>
                <div class="slot-sub">{f.sub}</div>
              </div>
            </div>
          {/each}
          <div class="block-txs">
            + transactions, not part of the header; committed by the
            <span class="seg-text-merkle">merkle root</span> above
          </div>
        </div>
      </div>
    </section>

    <!-- Raw data & verification -->
    <section class="card">
      <h3>Raw Header, verify it yourself</h3>
      <p class="section-intro">
        Hash these 160 hex characters with double SHA-256 and reverse the result: you get the
        share hash. No trust in this dashboard required.
      </p>

      <div class="proof-note">
        <strong>This is what turns a claim into proof.</strong>
        A screenshot of a big difficulty number proves nothing, anyone can edit one, and
        no one can tell from a picture whether the work behind it was ever done. These 80 bytes
        can't be faked that way. They are the exact preimage of the share hash, so anyone can
        hash them and see for themselves both that the hash is genuine and that finding it took
        the work claimed. The header also commits to the
        <span class="seg-text-prev">previous block</span>, fixing when the work happened, and
        through the <span class="seg-text-merkle">merkle root</span> to the coinbase
        transaction, fixing who it pays, so the same proof can't be lifted from another
        miner's share or replayed from a different block. Publish this, and the claim stands on
        its own.
      </div>

      <div class="raw-block-wrap">
        <div class="raw-block mono">
          {#each fields as f (f.key)}<span class="seg-text-{f.key}">{f.raw}</span>{/each}
        </div>
        <button type="button" class="copy-btn" onclick={() => copyText("header", pow!.header)}>
          {copiedKey === "header" ? "Copied" : "Copy"}
        </button>
      </div>

      <div class="verify-row">
        <button
          type="button"
          class="verify-btn"
          class:ok={allOk}
          onclick={() => (showOverlay = true)}
        >
          {verified ? (allOk ? "Verified \u2713, watch again" : "Verification failed, replay") : "Verify in this browser"}
        </button>
        <button type="button" class="share-btn" onclick={() => (showShare = true)}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="15" height="15">
            <circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/>
            <line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
          </svg>
          Share card
        </button>
        <span class="verify-hint">
          takes over the screen and replays the whole verification, merkle proof,
          padding, every SHA-256 round, from the real computation, locally
        </span>
      </div>

      {#if verified}
        <div class="result-panel">
          <div class="anim-line">Computed block header hash</div>
          <div class="final-hash mono">
            {#each finalHashChars as ch}<span class:fz={ch.zero}>{ch.c}</span>{/each}
          </div>
          <div class="final-match" class:ok={finalHash === pow.hash} class:fail={finalHash !== pow.hash}>
            {finalHash === pow.hash
              ? "= the recorded share hash, proof of work is genuine"
              : "\u2260 the recorded share hash!"}
          </div>
          <ul class="check-list">
            {#each checks as c (c.name)}
              <li class:ok={c.ok} class:fail={!c.ok}>
                <span class="check-mark">{c.ok ? "\u2713" : "\u2717"}</span>
                <span>
                  {c.name}
                  <span class="check-detail mono">{c.detail}</span>
                </span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      <div class="cli-wrap">
        <div class="stat-label">Or from any terminal</div>
        <div class="raw-block-wrap">
          <div class="raw-block cli mono">{cliSnippet}</div>
          <button type="button" class="copy-btn" onclick={() => copyText("cli", cliSnippet)}>
            {copiedKey === "cli" ? "Copied" : "Copy"}
          </button>
        </div>
      </div>
    </section>

    <!-- Coinbase & merkle root -->
    <section class="card">
      <h3>Reproducing the Merkle Root</h3>
      <p class="section-intro">
        The header's merkle root commits to every transaction in the block, but reproducing
        it doesn't require the full transaction list, only the coinbase transaction
        this pool built and one branch hash per tree level. Each branch hash cryptographically
        summarizes the other transactions in its subtree, so the proof below is exactly as
        strong as storing all of them. (CKPool discards the full template once work moves on,
        which is also why the complete transaction set isn't shown.)
      </p>

      <div class="stat-label">Coinbase transaction ({pow.coinbase.length / 2} bytes)</div>
      <div class="raw-block-wrap">
        <div class="raw-block mono">
          {#each cbSegments as s (s.key)}<span class="cb-{s.key}">{s.hex}</span>{/each}
        </div>
        <button type="button" class="copy-btn" onclick={() => copyText("cb", pow!.coinbase)}>
          {copiedKey === "cb" ? "Copied" : "Copy"}
        </button>
      </div>
      <div class="cb-legend">
        {#each cbSegments as s (s.key)}
          <span class="legend-item">
            <span class="swatch cb-swatch-{s.key}"></span>
            <span class="legend-name">{s.name}</span>
            <span class="legend-desc">, {s.desc}</span>
          </span>
        {/each}
      </div>

      <div class="branches">
        <div class="stat-label">
          Merkle branches ({branches.length})
        </div>
        {#if branches.length === 0}
          <div class="stat-sub">
            None, the block template contained only the coinbase transaction, so the
            merkle root is simply the coinbase txid.
          </div>
        {:else}
          <ol class="branch-list mono">
            {#each branches as b, i (i)}
              <li>{b}</li>
            {/each}
          </ol>
          <div class="stat-sub">
            Fold upward: start with SHA-256d(coinbase), then for each branch compute
            SHA-256d(current&nbsp;&#8214;&nbsp;branch). The result is the merkle root in the
            header. The "Verify" animation above walks through exactly this.
          </div>
        {/if}
      </div>

      <div class="extranonce-note">
        <div class="stat-label">Stratum work identity</div>
        <div class="stat-sub mono">
          extranonce1 <span class="cb-en1">{pow.enonce1}</span> · extranonce2
          <span class="cb-en2">{pow.nonce2}</span>
        </div>
      </div>
    </section>
  {/if}
</section>

{#if showShare && pow}
  <ShareCard
    data={{ kind: "share", share: pow, chain: data.chain?.chain ?? "main" }}
    onclose={() => (showShare = false)}
  />
{/if}

{#if showOverlay && pow}
  <PowVerifyOverlay
    {pow}
    {fields}
    onclose={() => (showOverlay = false)}
    oncomplete={onVerifyComplete}
  />
{/if}

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
  .head h2 {
    margin: 0;
    font-size: 1.35rem;
    font-weight: 600;
    line-height: 1.25;
  }
  h3 {
    margin: 0 0 0.75rem;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .section-intro {
    margin: 0 0 1rem;
    line-height: 1.6;
    color: var(--fg-dim);
  }
  .empty {
    color: var(--fg-dim);
    padding: 0.5rem 0;
    line-height: 1.6;
  }
  .empty p {
    margin: 0 0 0.6em;
  }
  .empty p:last-child {
    margin-bottom: 0;
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

  .totals {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 1rem;
  }
  .worker-name {
    font-size: 1.05rem;
    word-break: break-all;
  }

  /* ── Header field colors (shared by block slots and hex text) ── */
  .seg-version { background: #7aa2f7; }
  .seg-prev { background: var(--accent); }
  .seg-merkle { background: var(--good); }
  .seg-time { background: var(--warn); }
  .seg-bits { background: #bb9af7; }
  .seg-nonce { background: var(--bad); }
  .seg-text-version { color: #7aa2f7; }
  .seg-text-prev { color: var(--accent); }
  .seg-text-merkle { color: var(--good); }
  .seg-text-time { color: var(--warn); }
  .seg-text-bits { color: #bb9af7; }
  .seg-text-nonce { color: var(--bad); }

  /* ── The block drawing ── */
  .chain-row {
    display: flex;
    align-items: center;
    gap: 0;
    margin-top: 1.75rem;
  }
  .ghost-block {
    flex-shrink: 0;
    width: 150px;
    border: 1px dashed var(--border);
    border-radius: 8px;
    opacity: 0.75;
    overflow: hidden;
  }
  .ghost-cap {
    padding: 0.4rem 0.6rem;
    font-size: 0.7rem;
    letter-spacing: 0.05em;
    color: var(--fg-dim);
    border-bottom: 1px dashed var(--border);
    background: var(--bg-alt);
  }
  .ghost-body {
    padding: 0.9rem 0.6rem;
    font-size: 0.75rem;
    color: var(--fg-dim);
    text-align: center;
  }
  .chain-link {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0 0.8rem;
    color: var(--accent);
  }
  .chain-arrow {
    font-size: 1.4rem;
    line-height: 1;
  }
  .chain-label {
    font-size: 0.65rem;
    color: var(--fg-dim);
    margin-top: 0.15rem;
  }

  /* The block itself: a face with a top and right edge for depth. */
  .block3d {
    position: relative;
    flex: 1;
    min-width: 0;
    background: linear-gradient(160deg, rgba(255, 122, 58, 0.06), rgba(21, 26, 36, 0.6) 45%);
    border: 1px solid var(--accent-dim);
    border-radius: 4px 10px 10px 10px;
    margin-top: 12px;
    margin-right: 12px;
  }
  .block3d::before {
    /* top face */
    content: "";
    position: absolute;
    top: -12px;
    left: 5px;
    width: calc(100% + 1px);
    height: 12px;
    background: rgba(255, 122, 58, 0.14);
    border: 1px solid var(--accent-dim);
    border-bottom: none;
    transform: skewX(-45deg);
  }
  .block3d::after {
    /* right face */
    content: "";
    position: absolute;
    top: -6px;
    right: -13px;
    width: 12px;
    height: calc(100% + 1px);
    background: rgba(255, 122, 58, 0.07);
    border: 1px solid var(--accent-dim);
    border-left: none;
    transform: skewY(-45deg);
  }
  .block-cap {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.55rem 1rem;
    border-bottom: 1px solid var(--accent-dim);
    background: rgba(255, 122, 58, 0.08);
    border-radius: 3px 9px 0 0;
  }
  .block-title {
    font-weight: 700;
    letter-spacing: 0.08em;
    font-size: 0.85rem;
    color: var(--accent);
  }
  .block-cap-sub {
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-dim);
  }
  .slot {
    display: flex;
    border-bottom: 1px solid var(--border);
  }
  .slot:last-of-type {
    border-bottom: none;
  }
  .slot-stripe {
    width: 5px;
    flex-shrink: 0;
    opacity: 0.9;
  }
  .slot-main {
    min-width: 0;
    padding: 0.5rem 1rem 0.55rem;
    flex: 1;
  }
  .slot-head {
    display: flex;
    align-items: baseline;
    gap: 0.6em;
  }
  .slot-name {
    font-weight: 600;
    font-size: 0.9rem;
  }
  .slot-bytes {
    color: var(--fg-dim);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .slot-value {
    font-size: 0.92rem;
    word-break: break-all;
    margin-top: 0.1rem;
  }
  .slot-value a {
    color: inherit;
  }
  .slot-value a:hover {
    color: var(--accent);
  }
  .slot-sub {
    color: var(--fg-dim);
    font-size: 0.78rem;
    margin-top: 0.1rem;
  }
  .block-txs {
    padding: 0.6rem 1rem;
    border-top: 1px dashed var(--border);
    color: var(--fg-dim);
    font-size: 0.8rem;
    background: rgba(21, 26, 36, 0.5);
    border-radius: 0 0 9px 9px;
  }

  /* Why the raw bytes matter */
  .proof-note {
    margin-bottom: 1rem;
    padding: 0.9rem 1.1rem;
    border-left: 3px solid var(--accent);
    border-radius: 0 8px 8px 0;
    background: rgba(255, 122, 58, 0.07);
    color: var(--fg-dim);
    line-height: 1.65;
    font-size: 0.92rem;
  }
  .proof-note strong {
    color: var(--fg);
  }

  /* ── Raw hex blocks ── */
  .raw-block-wrap {
    position: relative;
  }
  .raw-block {
    background: var(--bg-alt);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.85rem 1rem;
    padding-right: 4.5rem;
    word-break: break-all;
    line-height: 1.7;
    letter-spacing: 0.03em;
    user-select: all;
    font-size: 0.95rem;
  }
  .raw-block.cli {
    user-select: all;
    color: var(--fg-dim);
    font-size: 0.8rem;
  }
  .copy-btn {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    font: inherit;
    font-size: 0.75rem;
    color: var(--fg-dim);
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.25em 0.7em;
    cursor: pointer;
  }
  .copy-btn:hover {
    color: var(--fg);
    border-color: var(--accent);
  }

  .verify-row {
    display: flex;
    align-items: center;
    gap: 0.85rem;
    margin-top: 1rem;
    flex-wrap: wrap;
  }
  .verify-btn {
    font: inherit;
    font-weight: 600;
    color: var(--bg);
    background: var(--accent);
    border: none;
    border-radius: 6px;
    padding: 0.5em 1.1em;
    cursor: pointer;
  }
  .verify-btn:hover:not(:disabled) {
    filter: brightness(1.1);
  }
  .verify-btn:disabled {
    opacity: 0.75;
    cursor: default;
  }
  .verify-btn.ok {
    background: var(--good);
  }
  .share-btn {
    font: inherit;
    display: inline-flex;
    align-items: center;
    gap: 0.45em;
    font-weight: 600;
    color: var(--accent);
    background: transparent;
    border: 1px solid var(--accent-dim);
    border-radius: 6px;
    padding: 0.5em 1.1em;
    cursor: pointer;
  }
  .share-btn:hover {
    border-color: var(--accent);
    background: var(--bg-hover);
    text-shadow: 0 0 8px rgba(255, 122, 58, 0.4);
  }
  .verify-hint {
    color: var(--fg-dim);
    font-size: 0.8rem;
  }

  /* ── Verification animation panel ── */
  .anim-panel,
  .result-panel {
    position: relative;
    margin-top: 1rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 1rem 1.1rem;
    background: var(--bg-alt);
    animation: panel-in 0.25s ease-out;
    min-height: 220px;
  }
  .result-panel {
    min-height: 0;
  }
  @keyframes panel-in {
    from { opacity: 0; transform: translateY(4px); }
    to { opacity: 1; transform: translateY(0); }
  }
  .anim-controls {
    position: absolute;
    top: 0.6rem;
    right: 0.6rem;
    display: flex;
    gap: 0.4rem;
  }
  .ctl-btn {
    font: inherit;
    font-size: 0.72rem;
    color: var(--fg-dim);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.2em 0.6em;
    cursor: pointer;
  }
  .ctl-btn:hover {
    color: var(--fg);
    border-color: var(--accent);
  }
  .ctl-btn.active {
    color: var(--accent);
    border-color: var(--accent);
  }

  .chapter-line {
    display: flex;
    align-items: baseline;
    gap: 0.6em;
    margin-bottom: 0.35rem;
    padding-right: 11rem;
  }
  .chapter-num {
    color: var(--accent);
    font-weight: 700;
    font-size: 0.78rem;
    letter-spacing: 0.05em;
  }
  .chapter-title {
    font-weight: 600;
    font-size: 0.92rem;
  }
  .chapter-dots {
    display: flex;
    gap: 0.3rem;
    margin-bottom: 0.85rem;
  }
  .dot {
    width: 22px;
    height: 3px;
    border-radius: 2px;
    background: var(--border);
    transition: background 0.3s;
  }
  .dot.on {
    background: var(--accent);
  }
  .chapter-splash {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 110px;
    font-size: 1.15rem;
    font-weight: 600;
    color: var(--fg);
    text-align: center;
    animation: panel-in 0.3s ease-out;
  }

  .anim-line {
    font-size: 0.88rem;
    margin-bottom: 0.6rem;
    line-height: 1.5;
  }
  .anim-progress {
    height: 6px;
    background: var(--border);
    border-radius: 3px;
    overflow: hidden;
    margin-bottom: 0.5rem;
  }
  .anim-progress-fill {
    height: 100%;
    background: var(--accent);
    border-radius: 3px;
    transition: width 0.12s linear;
  }
  .anim-round {
    color: var(--fg-dim);
    font-size: 0.8rem;
    margin-bottom: 0.65rem;
    word-break: break-all;
  }
  .regs {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 0.45rem;
  }
  .reg {
    display: flex;
    align-items: center;
    gap: 0.5em;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.3rem 0.55rem;
    min-width: 0;
  }
  .reg.fresh {
    border-color: var(--accent-dim);
    box-shadow: 0 0 6px rgba(255, 122, 58, 0.18);
  }
  .reg-name {
    color: var(--accent);
    font-weight: 700;
    font-size: 0.8rem;
    min-width: 1.3em;
  }
  .reg-val {
    font-size: 0.82rem;
    color: var(--fg);
    letter-spacing: 0.04em;
    overflow: hidden;
    text-overflow: clip;
    white-space: nowrap;
  }
  .words {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 0.35rem;
  }
  .word {
    display: flex;
    align-items: center;
    gap: 0.45em;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.22rem 0.5rem;
    font-size: 0.78rem;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
  }
  .word-idx {
    color: var(--fg-dim);
    font-size: 0.68rem;
    min-width: 2.2em;
  }
  .anim-note {
    margin-top: 0.6rem;
    color: var(--fg-dim);
    font-size: 0.75rem;
    line-height: 1.5;
  }
  .anim-note.formula {
    letter-spacing: 0.02em;
    word-break: break-all;
  }
  .anim-digest {
    word-break: break-all;
    font-size: 0.92rem;
    line-height: 1.6;
    letter-spacing: 0.03em;
  }
  .anim-digest.dim {
    color: var(--fg-dim);
  }
  .anim-flip {
    color: var(--accent);
    font-size: 0.8rem;
    margin: 0.4rem 0;
  }

  /* Merkle fold grid */
  .fold-grid {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.25rem 0.7rem;
    align-items: baseline;
  }
  .fold-tag {
    color: var(--fg-dim);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    white-space: nowrap;
  }
  .fold-tag.good {
    color: var(--good);
  }
  .fold-hex {
    word-break: break-all;
    font-size: 0.8rem;
    line-height: 1.5;
  }
  .fold-hex.dim {
    color: var(--fg-dim);
  }
  .fold-hex.glow {
    color: var(--good);
    text-shadow: 0 0 6px rgba(92, 224, 168, 0.3);
  }

  /* Padding visualization */
  .pad-blocks {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .pad-block {
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
  }
  .pad-label {
    padding: 0.3rem 0.75rem;
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--fg-dim);
    background: var(--bg-card);
    border-bottom: 1px solid var(--border);
  }
  .pad-hex {
    padding: 0.5rem 0.75rem;
    word-break: break-all;
    font-size: 0.8rem;
    line-height: 1.6;
    letter-spacing: 0.03em;
  }
  .pad-seg {
    color: var(--fg-dim);
    opacity: 0.7;
  }
  .len-seg {
    color: var(--fg);
    text-decoration: underline dotted;
    text-underline-offset: 3px;
  }

  /* Target comparison */
  .target-rows {
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
  }
  .target-row {
    display: grid;
    grid-template-columns: 7.5em 1fr;
    gap: 0.7rem;
    align-items: baseline;
  }
  .target-tag {
    color: var(--fg-dim);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .target-hex {
    word-break: break-all;
    font-size: 0.82rem;
    line-height: 1.5;
  }
  .target-hex.dim {
    color: var(--fg-dim);
  }

  /* Final result */
  .final-hash {
    word-break: break-all;
    font-size: 1.05rem;
    line-height: 1.7;
    letter-spacing: 0.04em;
  }
  .fz {
    color: var(--good);
    font-weight: 700;
    text-shadow: 0 0 6px rgba(92, 224, 168, 0.35);
  }
  .final-match {
    margin: 0.5rem 0 0.9rem;
    font-size: 0.88rem;
    font-weight: 600;
  }
  .final-match.ok {
    color: var(--good);
  }
  .final-match.fail {
    color: var(--bad);
  }
  .check-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .check-list li {
    display: flex;
    gap: 0.6em;
    line-height: 1.5;
  }
  .check-mark {
    flex-shrink: 0;
    font-weight: 700;
  }
  .check-list li.ok .check-mark {
    color: var(--good);
  }
  .check-list li.fail .check-mark {
    color: var(--bad);
  }
  .check-detail {
    display: block;
    color: var(--fg-dim);
    font-size: 0.78rem;
    word-break: break-all;
  }
  .replay-btn {
    margin-top: 0.9rem;
    font: inherit;
    font-size: 0.78rem;
    color: var(--fg-dim);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.3em 0.8em;
    cursor: pointer;
  }
  .replay-btn:hover {
    color: var(--fg);
    border-color: var(--accent);
  }
  .cli-wrap {
    margin-top: 1.25rem;
  }

  /* ── Coinbase segments ── */
  .cb-cb1 { color: var(--fg-dim); }
  .cb-en1 { color: var(--accent); font-weight: 700; }
  .cb-en2 { color: var(--warn); font-weight: 700; }
  .cb-cb2 { color: var(--fg-dim); }
  .cb-swatch-cb1 { background: var(--fg-dim); }
  .cb-swatch-en1 { background: var(--accent); }
  .cb-swatch-en2 { background: var(--warn); }
  .cb-swatch-cb2 { background: var(--fg-dim); }
  .swatch {
    display: inline-block;
    width: 10px;
    height: 10px;
    border-radius: 3px;
    flex-shrink: 0;
  }
  .cb-legend {
    display: flex;
    gap: 0.4rem 1.25rem;
    margin-top: 0.75rem;
    flex-wrap: wrap;
    font-size: 0.82rem;
  }
  .legend-item {
    display: flex;
    align-items: center;
    gap: 0.45em;
  }
  .legend-name {
    font-weight: 600;
  }
  .legend-desc {
    color: var(--fg-dim);
  }

  .branches {
    margin-top: 1.25rem;
  }
  .branch-list {
    margin: 0.25rem 0 0.5rem;
    padding-left: 1.5rem;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    word-break: break-all;
    font-size: 0.85rem;
  }
  .branch-list li::marker {
    color: var(--fg-dim);
  }
  .extranonce-note {
    margin-top: 1.25rem;
  }

  @media (max-width: 700px) {
    .totals {
      grid-template-columns: 1fr;
    }
    .ghost-block,
    .chain-link {
      display: none;
    }
    .regs,
    .words {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .chapter-line {
      padding-right: 0;
      margin-top: 2rem;
    }
  }
  @media (max-width: 480px) {
    .raw-block {
      font-size: 0.72rem;
      padding-right: 1rem;
      padding-top: 2.2rem;
    }
    .slot-value {
      font-size: 0.75rem;
    }
    .final-hash,
    .anim-digest {
      font-size: 0.8rem;
    }
    .reg-val,
    .word {
      font-size: 0.72rem;
    }
    .target-row {
      grid-template-columns: 1fr;
      gap: 0.1rem;
    }
  }
</style>
