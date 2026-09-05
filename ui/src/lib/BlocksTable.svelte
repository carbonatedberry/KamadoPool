<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { formatAgo, formatDifficulty, explorerBaseFor } from "../format";
  import ShareCard from "./ShareCard.svelte";
  import type { BlockRecord } from "../types";

  const chainDisplayName: Record<string, string> = {
    main: "mainnet",
  };
  function displayChain(chain: string): string {
    return chainDisplayName[chain] ?? chain;
  }

  const blocks = $derived.by(() => {
    const b = snap.data?.recent_blocks ?? [];
    return [...b].reverse();
  });

  // Block whose share card is open, if any.
  let shareBlock: BlockRecord | null = $state(null);

  const explorerBase = $derived(
    explorerBaseFor(snap.data?.chain?.chain, snap.data?.mempool_base_url),
  );

  const currentChain = $derived(snap.data?.chain?.chain ?? "");

  function truncHash(hash: string): string {
    if (!hash || hash.length <= 16) return hash;
    return hash.slice(0, 8) + "…" + hash.slice(-8);
  }

  function minerLabel(miner: string | undefined): string {
    if (!miner) return "-";
    // Show address.worker truncated
    const dot = miner.indexOf(".");
    if (dot < 0) {
      // Just an address, truncate middle
      if (miner.length > 20) return miner.slice(0, 8) + "…" + miner.slice(-6);
      return miner;
    }
    const addr = miner.slice(0, dot);
    const worker = miner.slice(dot + 1);
    const shortAddr = addr.length > 12 ? addr.slice(0, 6) + "…" + addr.slice(-4) : addr;
    return shortAddr + "." + worker;
  }
</script>

<section class="card">
  <h2>Blocks found</h2>
  {#if blocks.length === 0}
    <div class="empty">No blocks found yet.</div>
  {:else}
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Height</th>
            <th>Miner</th>
            <th>Chain</th>
            <th>Hash</th>
            <th class="num">Reward</th>
            <th class="num">Winning share</th>
            <th class="num">When</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each blocks as b (b.height + "-" + b.found_at)}
            <tr class:orphaned={!!b.orphaned_at} class:other-chain={!!b.chain && !!currentChain && b.chain !== currentChain}>
              <td class="mono">
                {b.height}
                {#if b.orphaned_at}
                  <span class="orphan-tag" title="Reorged out of the canonical chain at {b.orphaned_at}">orphaned</span>
                {/if}
              </td>
              <td class="miner-cell" title={b.miner ?? ""}>{minerLabel(b.miner)}</td>
              <td>
                {#if b.chain && currentChain && b.chain !== currentChain}
                  <span class="chain-tag" title="Mined on {displayChain(b.chain)} - current node is on {displayChain(currentChain)}">{displayChain(b.chain)}</span>
                {:else if b.chain}
                  <span class="chain-current">{displayChain(b.chain)}</span>
                {:else}
                  <span class="chain-unknown">-</span>
                {/if}
              </td>
              <td class="hash-cell">
                {#if b.hash}
                  <a
                    class="hash mono"
                    href="{explorerBase}/block/{b.hash}"
                    target="_blank"
                    rel="noopener noreferrer"
                    title={b.hash}
                  >{truncHash(b.hash)}</a>
                {:else}
                  <span class="hash mono">-</span>
                {/if}
              </td>
              <td class="num">
                {#if b.reward_btc}
                  <span class="reward-num">{b.reward_btc.toFixed(4)}</span>
                  <span class="unit">BTC</span>
                {:else}
                  <span class="reward-num">-</span>
                {/if}
              </td>
              <td class="num">{b.share_diff ? formatDifficulty(b.share_diff) : "-"}</td>
              <td class="num">{formatAgo(new Date(b.found_at).getTime() / 1000)}</td>
              <td class="share-cell">
                <button
                  type="button"
                  class="share-block"
                  onclick={() => (shareBlock = b)}
                  title="Make a shareable card for this block"
                  aria-label="Share block {b.height}"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="14" height="14">
                    <circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/>
                    <line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
                  </svg>
                </button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

{#if shareBlock}
  <ShareCard
    data={{
      kind: "block",
      block: shareBlock,
      chain: shareBlock.chain || currentChain || "main",
      explorerBase,
    }}
    onclose={() => (shareBlock = null)}
  />
{/if}

<style>
  .share-cell {
    width: 1%;
    white-space: nowrap;
  }
  .share-block {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--fg-dim);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.3em 0.5em;
    cursor: pointer;
    transition: color 0.15s, border-color 0.15s;
  }
  .share-block:hover {
    color: var(--accent);
    border-color: var(--accent);
  }

  h2 {
    margin: 0 0 1rem;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .empty {
    color: var(--fg-dim);
    padding: 0.5rem 0;
  }
  .table-wrap {
    overflow-x: auto;
  }
  .miner-cell {
    font-size: 0.85em;
    color: var(--fg-dim);
    white-space: nowrap;
    max-width: 180px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .hash-cell {
    white-space: nowrap;
  }
  .hash {
    color: var(--fg-dim);
    font-size: 0.85em;
    line-height: 1.4;
  }
  a.hash {
    color: var(--fg-dim);
    text-decoration: none;
    border-bottom: 1px dashed transparent;
  }
  a.hash:hover {
    color: var(--accent);
    border-bottom-color: var(--accent);
  }
  .reward-num {
    font-variant-numeric: tabular-nums;
  }
  tr.orphaned td {
    color: var(--fg-dim);
    text-decoration: line-through;
    text-decoration-color: rgba(220, 80, 80, 0.55);
  }
  tr.orphaned .orphan-tag {
    display: inline-block;
    margin-left: 0.4em;
    padding: 0.05em 0.4em;
    border-radius: 3px;
    background: rgba(220, 80, 80, 0.15);
    color: rgb(220, 110, 110);
    font-size: 0.7em;
    font-weight: 600;
    text-decoration: none;
    vertical-align: middle;
  }
  .chain-tag {
    display: inline-block;
    padding: 0.05em 0.4em;
    border-radius: 3px;
    background: rgba(80, 160, 220, 0.15);
    color: rgb(100, 170, 220);
    font-size: 0.8em;
    font-weight: 600;
    text-decoration: none;
  }
  .chain-current {
    color: var(--fg-dim);
    font-size: 0.8em;
  }
  .chain-unknown {
    color: var(--fg-dim);
    font-size: 0.8em;
  }
  tr.other-chain td {
    opacity: 0.6;
  }
  .unit {
    color: var(--fg-dim);
    font-size: 0.75em;
    font-weight: 400;
    margin-left: 0.25em;
  }

  /* Mobile: hide Chain, Hash, Reward columns */
  @media (max-width: 768px) {
    table :global(th:nth-child(3)),
    table :global(td:nth-child(3)),
    table :global(th:nth-child(4)),
    table :global(td:nth-child(4)) {
      display: none;
    }
  }
  @media (max-width: 480px) {
    table :global(th:nth-child(5)),
    table :global(td:nth-child(5)),
    table :global(th:nth-child(6)),
    table :global(td:nth-child(6)) {
      display: none;
    }
  }
</style>
