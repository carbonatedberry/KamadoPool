<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { formatDifficulty } from "../format";
  import type { StratumClient } from "../types";

  // "Best (session)" = best diff in the current stratum TCP session,
  // i.e. only ckpool's stratum_instance.best_diff. Resets on miner
  // disconnect (the instance is freed) and on pool restart (in-memory
  // state, not persisted). worker.best_diff is a separate persistent
  // counter that survives both events, so we deliberately don't fall
  // back to it, that fallback was the bug. Offline workers have no
  // current session, so their session-best is 0.
  const rows = $derived.by(() => {
    const ws = snap.data?.workers ?? [];
    const cs = snap.data?.clients ?? [];
    const clientByWorker = new Map<string, StratumClient>();
    for (const c of cs) {
      const wname = c.workername || `${c.address}.unnamed`;
      clientByWorker.set(wname, c);
    }
    const enriched = ws.map((w) => {
      const c = clientByWorker.get(w.worker);
      return {
        worker: w.worker,
        sessionBest: c?.bestdiff ?? 0,
        bestEver: w.bestever || w.bestdiff,
      };
    });
    enriched.sort((a, b) => b.bestEver - a.bestEver);
    return enriched.slice(0, 10);
  });
</script>

<section class="card">
  <h2>Best shares leaderboard</h2>
  {#if rows.length === 0}
    <div class="empty">No shares submitted yet.</div>
  {:else}
    <table>
      <thead>
        <tr>
          <th>#</th>
          <th>Worker</th>
          <th class="num">Best (session)</th>
          <th class="num">Best (ever)</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as r, i (r.worker)}
          <tr>
            <td>{i + 1}</td>
            <td class="mono">{r.worker}</td>
            <td class="num">{formatDifficulty(r.sessionBest)}</td>
            <td class="num">{formatDifficulty(r.bestEver)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<style>
  h2 {
    margin: 0 0 1rem;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .empty {
    color: var(--fg-dim);
    padding: 0.5rem 0;
  }

  /* Mobile: hide session best column, truncate worker names */
  @media (max-width: 480px) {
    table :global(th:nth-child(3)),
    table :global(td:nth-child(3)) {
      display: none;
    }
    td.mono {
      max-width: 140px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
</style>
