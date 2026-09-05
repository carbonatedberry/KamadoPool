<script lang="ts">
  import { clearSelection } from "../stores/selection.svelte";
  import { snap } from "../stores/snapshot.svelte";
  import { formatHashrate } from "../format";

  type BoostedTx = {
    txid: string;
    original_feerate: number;
    boosted_feerate: number;
    fee_delta: number;
    vsize: number;
    boosted_at: number;
  };

  let txid = $state("");
  let feerateInput = $state("");
  let loading = $state(false);
  let error = $state("");
  let success = $state("");
  let feeLostSats = $state(0);
  let boostedList = $state<BoostedTx[]>([]);
  let loadingMax = $state(false);


  const feerate = $derived(parseFloat(feerateInput) || 0);
  const validTxid = $derived(/^[0-9a-fA-F]{64}$/.test(txid.trim()));
  const overCap = $derived(feerate > 2000);

  const explorerBase = $derived(
    snap.data?.mempool_base_url || "https://mempool.space"
  );

  const poolHashrate = $derived(snap.data?.hashrate_hs_5m ?? 0);
  const networkHashrate = $derived(snap.data?.network_hashrate_hs ?? 0);
  const poolShare = $derived(
    networkHashrate > 0 ? poolHashrate / networkHashrate : 0
  );

  async function fetchList() {
    try {
      const res = await fetch("/api/accelerate/list");
      if (res.ok) {
        boostedList = await res.json();
      }
    } catch { /* ignore */ }
  }

  async function handleBoost() {
    if (!validTxid || feerate <= 0 || overCap) return;
    loading = true;
    error = "";
    success = "";
    feeLostSats = 0;
    try {
      const res = await fetch("/api/accelerate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ txid: txid.trim().toLowerCase(), fee_rate_satvb: feerate }),
      });
      const data = await res.json();
      if (!res.ok) {
        error = data.error || `HTTP ${res.status}`;
      } else {
        feeLostSats = data.fee_lost_sats ?? 0;
        const from = data.original_feerate.toFixed(1);
        const to = data.boosted_feerate.toFixed(1);
        if (data.fee_lost_error) {
          success = `Boosted from ${from} to ${to} sat/vB. Revenue impact could not be measured: ${data.fee_lost_error}`;
        } else if (feeLostSats > 0) {
          success = `Boosted from ${from} to ${to} sat/vB. Revenue impact: -${feeLostSats.toLocaleString()} sats (${(feeLostSats / 1e8).toFixed(8)} BTC) per block mined.`;
        } else {
          success = `Boosted from ${from} to ${to} sat/vB. No revenue impact, the transaction was already in the template or the mempool fits in one block.`;
        }
        txid = "";
        feerateInput = "";
        fetchList();
      }
    } catch (e) {
      error = (e as Error).message;
    }
    loading = false;
  }

  async function handleMaxPriority() {
    loadingMax = true;
    error = "";
    try {
      const res = await fetch("/api/accelerate/max", { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        error = data.error || `HTTP ${res.status}`;
      } else {
        feerateInput = data.max_feerate_satvb.toFixed(1);
      }
    } catch (e) {
      error = (e as Error).message;
    }
    loadingMax = false;
  }

  async function handleCancel(cancelTxid: string) {
    error = "";
    try {
      const res = await fetch("/api/accelerate/cancel", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ txid: cancelTxid }),
      });
      const data = await res.json();
      if (!res.ok) {
        error = data.error || `HTTP ${res.status}`;
      } else {
        boostedList = boostedList.filter(t => t.txid !== cancelTxid);
      }
    } catch (e) {
      error = (e as Error).message;
    }
  }

  function fmtAgo(unix: number): string {
    const diff = Math.floor(Date.now() / 1000 - unix);
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
  }

  function onKey(ev: KeyboardEvent): void {
    if (ev.key === "Escape") clearSelection();
  }

  $effect(() => { fetchList(); });
  $effect(() => {
    const id = setInterval(fetchList, 15000);
    return () => clearInterval(id);
  });
</script>

<svelte:window onkeydown={onKey} />

<section class="page">
  <nav class="crumbs">
    <button type="button" class="back" onclick={clearSelection}>
      &larr; Back to dashboard
    </button>
  </nav>

  <header class="accel-header">
    <div class="flame-border" aria-hidden="true"></div>
    <div class="header-content">
      <h2 class="title">
        <svg class="rocket" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/>
          <path d="M12 15l-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/>
          <path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0"/>
          <path d="M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/>
        </svg>
        Transaction Accelerator
      </h2>
      <p class="explainer">
        Boost a transaction's priority in your next block template using
        <code>prioritisetransaction</code>. When your pool finds a block,
        the boosted transaction will be included regardless of its real feerate.
      </p>
      <div class="info-box">
        <p>
          <strong>How it works:</strong> The boosted transaction takes the place of
          the lowest-feerate transaction at the bottom of the block template. If the
          mempool isn't full (all transactions already fit), there is <em>zero cost</em>.
        </p>
        <p>
          <strong>Revenue impact:</strong> The server inspects the current block template to find
          the marginal transaction, the lowest fee-rate tx that would be displaced by the
          boosted one. Its fee is the revenue you sacrifice per block mined, shown after each boost.
        </p>
        <p>
          Your pool: {formatHashrate(poolHashrate)} /
          {formatHashrate(networkHashrate)} network
          ({poolShare > 0 ? `${(poolShare * 100).toFixed(6)}%` : "-"} of blocks).
        </p>
      </div>
    </div>
  </header>

  <section class="card boost-form">
    <h3>Boost a Transaction</h3>

    <div class="field">
      <label for="txid-input">Transaction ID</label>
      <input
        id="txid-input"
        type="text"
        class="mono"
        placeholder="64-character hex txid"
        bind:value={txid}
        maxlength={64}
        spellcheck={false}
      />
      {#if txid && !validTxid}
        <span class="field-error">Must be exactly 64 hex characters</span>
      {/if}
    </div>

    <div class="field">
      <label for="feerate-input">Target feerate (sat/vB), max 2,000</label>
      <div class="feerate-row">
        <input
          id="feerate-input"
          type="number"
          min="1"
          max="2000"
          step="0.1"
          placeholder="e.g. 50"
          bind:value={feerateInput}
        />
        <button
          type="button"
          class="max-btn"
          onclick={handleMaxPriority}
          disabled={loadingMax}
          title="Set feerate to 2x the highest in mempool (capped at 2000)"
        >
          {#if loadingMax}
            <span class="spinner small"></span> Loading...
          {:else}
            Prioritize above all
          {/if}
        </button>
      </div>
      {#if overCap}
        <span class="field-error">Cannot exceed 2,000 sat/vB</span>
      {/if}
    </div>

    <button
      type="button"
      class="boost-btn"
      onclick={handleBoost}
      disabled={loading || !validTxid || feerate <= 0 || overCap}
    >
      {#if loading}
        <span class="spinner"></span> Boosting...
      {:else}
        Boost Transaction
      {/if}
    </button>

    {#if error}
      <div class="msg error">
        <span>{error}</span>
        <button type="button" class="dismiss" onclick={() => error = ""}>&times;</button>
      </div>
    {/if}
    {#if success}
      <div class="msg success">
        <span>{success}</span>
        <button type="button" class="dismiss" onclick={() => success = ""}>&times;</button>
      </div>
    {/if}
  </section>

  <section class="card">
    <h3>Boosted Transactions {#if boostedList.length > 0}<span class="count">{boostedList.length}</span>{/if}</h3>
    {#if boostedList.length === 0}
      <div class="empty">No transactions currently boosted. They are automatically removed when confirmed or dropped from the mempool.</div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Transaction</th>
              <th class="num">Original</th>
              <th class="num">Boosted to</th>
              <th class="num">Delta</th>
              <th class="num">vsize</th>
              <th>When</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each boostedList as tx (tx.txid)}
              <tr>
                <td>
                  <a
                    class="txid-link mono"
                    href="{explorerBase}/tx/{tx.txid}"
                    target="_blank"
                    rel="noopener noreferrer"
                    title={tx.txid}
                  >{tx.txid.slice(0, 10)}...{tx.txid.slice(-10)}</a>
                </td>
                <td class="num">{tx.original_feerate.toFixed(1)}<span class="unit"> sat/vB</span></td>
                <td class="num boost-rate">{tx.boosted_feerate.toFixed(1)}<span class="unit"> sat/vB</span></td>
                <td class="num delta">{tx.fee_delta.toLocaleString()}<span class="unit"> sats</span></td>
                <td class="num">{tx.vsize.toLocaleString()}</td>
                <td>{fmtAgo(tx.boosted_at)}</td>
                <td>
                  <button
                    type="button"
                    class="cancel-btn"
                    onclick={() => handleCancel(tx.txid)}
                    title="Reverse the priority boost"
                  >Cancel</button>
                </td>
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
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 1.5rem;
  }
  .crumbs {
    margin-bottom: 0.25rem;
  }
  .back {
    font: inherit;
    font-size: 1rem;
    color: var(--fg-dim);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.5em 1em;
    cursor: pointer;
  }
  .back:hover {
    color: var(--fg);
    border-color: var(--accent);
    background: var(--bg-hover);
  }

  /* ── Header with animated flame border ── */
  .accel-header {
    position: relative;
    border-radius: 14px;
    padding: 2px;
    background: linear-gradient(135deg, #ff6a00, #ff2d2d, #ff6a00, #ffb347);
    background-size: 300% 300%;
    animation: flame-gradient 4s ease infinite;
  }
  @keyframes flame-gradient {
    0% { background-position: 0% 50%; }
    50% { background-position: 100% 50%; }
    100% { background-position: 0% 50%; }
  }
  .header-content {
    background: var(--bg-card);
    border-radius: 12px;
    padding: 2rem;
  }
  .title {
    display: flex;
    align-items: center;
    gap: 0.5em;
    margin: 0 0 1rem;
    font-size: 1.8rem;
    font-weight: 700;
  }
  .rocket {
    width: 1.3em;
    height: 1.3em;
    color: var(--accent);
  }
  .explainer {
    margin: 0 0 1.25rem;
    color: var(--fg-dim);
    line-height: 1.6;
    font-size: 1.1rem;
  }
  .explainer code {
    background: var(--bg-alt);
    padding: 0.15em 0.4em;
    border-radius: 4px;
    font-size: 0.9em;
    color: var(--fg);
  }
  .info-box {
    background: rgba(255, 180, 71, 0.06);
    border: 1px solid rgba(255, 180, 71, 0.2);
    border-radius: 10px;
    padding: 1.25rem 1.5rem;
    font-size: 1rem;
    line-height: 1.6;
    color: var(--fg-dim);
  }
  .info-box p {
    margin: 0 0 0.75rem;
  }
  .info-box p:last-child {
    margin-bottom: 0;
  }
  .info-box strong {
    color: var(--fg);
  }
  .info-box em {
    color: var(--good);
    font-style: normal;
    font-weight: 600;
  }

  /* ── Boost form ── */
  .boost-form {
    position: relative;
    overflow: hidden;
    padding: 2rem;
  }
  .boost-form h3 {
    position: relative;
    z-index: 1;
    margin: 0 0 1.5rem;
    font-size: 1.3rem;
    font-weight: 700;
  }

  .field {
    margin-bottom: 1.5rem;
  }
  .field label {
    display: block;
    font-size: 0.95rem;
    font-weight: 500;
    color: var(--fg-dim);
    margin-bottom: 0.5em;
  }
  .field input {
    width: 100%;
    padding: 0.75em 1em;
    font: inherit;
    font-size: 1.1rem;
    background: var(--bg-alt);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--fg);
    outline: none;
    transition: border-color 0.15s;
  }
  .field input:focus {
    border-color: var(--accent);
  }
  .field-error {
    display: block;
    color: var(--bad);
    font-size: 0.9rem;
    margin-top: 0.4em;
  }
  .feerate-row {
    display: flex;
    gap: 0.75rem;
    align-items: stretch;
  }
  .feerate-row input {
    flex: 1;
  }
  .max-btn {
    display: inline-flex;
    align-items: center;
    gap: 0.5em;
    font: inherit;
    font-size: 0.95rem;
    font-weight: 600;
    white-space: nowrap;
    background: linear-gradient(135deg, #1a0a00, #2d1400);
    border: 1px solid rgba(255, 122, 58, 0.4);
    border-radius: 8px;
    padding: 0.6em 1.25em;
    color: var(--accent);
    cursor: pointer;
    transition: box-shadow 0.2s, border-color 0.2s;
  }
  .max-btn:hover:not(:disabled) {
    border-color: var(--accent);
    box-shadow: 0 0 14px rgba(255, 122, 58, 0.35);
  }
  .max-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .boost-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.5em;
    width: 100%;
    padding: 0.9em;
    font: inherit;
    font-size: 1.2rem;
    font-weight: 700;
    background: linear-gradient(135deg, #ff6a00, #ff2d2d);
    border: none;
    border-radius: 10px;
    color: #fff;
    cursor: pointer;
    transition: box-shadow 0.2s, transform 0.15s, opacity 0.2s;
  }
  .boost-btn:hover:not(:disabled) {
    box-shadow: 0 4px 24px rgba(255, 106, 0, 0.45), 0 0 50px rgba(255, 45, 45, 0.15);
    transform: translateY(-1px);
  }
  .boost-btn:active:not(:disabled) {
    transform: translateY(0);
  }
  .boost-btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }
  .spinner {
    display: inline-block;
    width: 1.1em;
    height: 1.1em;
    border: 2.5px solid rgba(255,255,255,0.3);
    border-top-color: #fff;
    border-radius: 50%;
    animation: spin 0.6s linear infinite;
  }
  .spinner.small {
    width: 0.9em;
    height: 0.9em;
    border-width: 2px;
    border-color: rgba(255, 122, 58, 0.3);
    border-top-color: var(--accent);
  }
  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  /* ── Messages ── */
  .msg {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
    margin-top: 1.25rem;
    padding: 1rem 1.25rem;
    border-radius: 8px;
    font-size: 1.05rem;
    line-height: 1.5;
  }
  .msg span {
    flex: 1;
  }
  .msg.error {
    background: rgba(255, 107, 107, 0.1);
    border: 1px solid rgba(255, 107, 107, 0.3);
    color: var(--bad);
  }
  .msg.success {
    background: rgba(92, 224, 168, 0.08);
    border: 1px solid rgba(92, 224, 168, 0.3);
    color: var(--good);
  }
  .dismiss {
    flex-shrink: 0;
    background: none;
    border: none;
    color: inherit;
    font-size: 1.4rem;
    line-height: 1;
    cursor: pointer;
    opacity: 0.6;
    padding: 0 0.2em;
    transition: opacity 0.15s;
  }
  .dismiss:hover {
    opacity: 1;
  }

  /* ── Table ── */
  h3 {
    margin: 0 0 1.25rem;
    font-size: 1.2rem;
    font-weight: 700;
  }
  .count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 1.6em;
    height: 1.6em;
    font-size: 0.65em;
    font-weight: 700;
    background: var(--accent);
    color: #000;
    border-radius: 999px;
    margin-left: 0.4em;
    vertical-align: middle;
  }
  .empty {
    color: var(--fg-dim);
    padding: 1.25rem 0;
    font-size: 1.05rem;
  }
  .table-wrap {
    overflow-x: auto;
    font-size: 1.05rem;
  }
  .txid-link {
    color: var(--accent);
    text-decoration: none;
    font-size: 1rem;
    border-bottom: 1px dashed transparent;
  }
  .txid-link:hover {
    color: #ff9a5f;
    border-bottom-color: var(--accent);
  }
  .boost-rate {
    color: var(--accent);
    font-weight: 600;
  }
  .delta {
    color: var(--bad);
  }
  .unit {
    color: var(--fg-dim);
    font-size: 0.85em;
    font-weight: 400;
  }
  .cancel-btn {
    font: inherit;
    font-size: 0.9rem;
    font-weight: 600;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.35em 0.8em;
    color: var(--fg-dim);
    cursor: pointer;
    transition: color 0.15s, border-color 0.15s;
  }
  .cancel-btn:hover {
    color: var(--bad);
    border-color: var(--bad);
  }

  @media (max-width: 600px) {
    .header-content {
      padding: 1.25rem;
    }
    .boost-form {
      padding: 1.25rem;
    }
    .title {
      font-size: 1.4rem;
    }
    .feerate-row {
      flex-direction: column;
    }
  }
</style>
