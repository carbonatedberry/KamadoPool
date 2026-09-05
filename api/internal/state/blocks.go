package state

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/kamadopool/kamado-api/internal/logmon"
	"github.com/kamadopool/kamado-api/internal/store"
)

// BlockRecord is a found block, merged from a logmon event with bitcoind
// data if available. Hash and Reward are populated best-effort via the
// RPC lookup scheduled right after the log line is seen; the reconcile
// loop fills any holes later. OrphanedAt is set if a periodic chain
// check finds the recorded hash no longer matches the canonical block
// at this height (i.e. the network reorged us out).
type BlockRecord struct {
	Height     int64      `json:"height"`
	Hash       string     `json:"hash,omitempty"`
	RewardBT   float64    `json:"reward_btc,omitempty"`
	FoundAt    time.Time  `json:"found_at"`
	Source     string     `json:"source"` // "logmon" for now; "zmq" later
	ShareDiff  float64    `json:"share_diff,omitempty"`
	OrphanedAt *time.Time `json:"orphaned_at,omitempty"`
	Chain      string     `json:"chain,omitempty"` // "main", "test", "signet"
	Miner      string     `json:"miner,omitempty"` // workername (address.worker) who found the block
}

// timePtr returns a pointer to t if non-zero, nil otherwise.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// inferOtherChain returns the most likely chain name for a block that
// doesn't belong to currentChain. Since the pool only supports mainnet
// and testnet4, the inference is unambiguous. Note: Bitcoin Core reports
// testnet4 as "testnet4" (not "test" which is the deprecated testnet3).
func inferOtherChain(currentChain string) string {
	switch currentChain {
	case "main":
		return "testnet4"
	case "testnet4":
		return "main"
	default:
		return "other"
	}
}

// IngestAttemptEvents counts "Possible/Submitting block solve" log
// lines so we can compare attempts vs confirmations in the snapshot.
// A growing gap means bitcoind is rejecting our submissions or the
// RPC is failing. Persists the running counter so it survives restarts.
func (a *Aggregator) IngestAttemptEvents(ctx context.Context, events <-chan logmon.AttemptEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			a.mu.Lock()
			a.blockSubmitAttempts++
			n := a.blockSubmitAttempts
			save := a.Store != nil && time.Since(a.lastSubmitCountSave) >= 30*time.Second
			if save {
				a.lastSubmitCountSave = time.Now()
			}
			a.mu.Unlock()
			a.Log.Info("logmon: submit attempt", "share_diff", ev.ShareDiff, "attempts", n)
			if save {
				if err := a.Store.SetKV(kvSubmitAttempts, strconv.FormatInt(n, 10)); err != nil {
					a.Log.Warn("submit attempts persist failed", "err", err)
				}
			}
		}
	}
}

// IngestLatencyEvents reads block-update latency events from the tailer
// and accumulates stats for the dashboard. Stats are persisted to the
// kv store so they survive restarts.
func (a *Aggregator) IngestLatencyEvents(ctx context.Context, events <-chan logmon.LatencyEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			a.mu.Lock()
			a.latencyCount++
			a.latencySumMs += ev.LatencyMs
			a.latencyLastMs = ev.LatencyMs
			// Wasted hashes = latency in seconds × current 5m hashrate.
			hs5m := a.snap.HashrateHs5m
			a.staleWorkHashes += (float64(ev.LatencyMs) / 1000.0) * hs5m
			count, sum, last, stale := a.latencyCount, a.latencySumMs, a.latencyLastMs, a.staleWorkHashes
			a.mu.Unlock()
			// Signal the Run loop that ckpool's notifier is done so
			// dashboard RPC calls can proceed without competing.
			select {
			case a.notifierDone <- struct{}{}:
			default:
			}
			a.Log.Info("logmon: block update latency", "ms", ev.LatencyMs, "count", count)
			// Blocks are infrequent (~10min); persist every event.
			if a.Store != nil {
				_ = a.Store.SetKV(kvLatencyCount, strconv.FormatInt(count, 10))
				_ = a.Store.SetKV(kvLatencySumMs, strconv.FormatInt(sum, 10))
				_ = a.Store.SetKV(kvLatencyLastMs, strconv.FormatInt(last, 10))
				_ = a.Store.SetKV(kvStaleWorkHashes, strconv.FormatFloat(stale, 'f', -1, 64))
			}
		}
	}
}

// ResetLatency zeroes the block-update latency counters both in memory
// and in the persistent kv store.
func (a *Aggregator) ResetLatency() {
	a.mu.Lock()
	a.latencyCount = 0
	a.latencySumMs = 0
	a.latencyLastMs = 0
	a.staleWorkHashes = 0
	a.mu.Unlock()
	if a.Store != nil {
		_ = a.Store.SetKV(kvLatencyCount, "0")
		_ = a.Store.SetKV(kvLatencySumMs, "0")
		_ = a.Store.SetKV(kvLatencyLastMs, "0")
		_ = a.Store.SetKV(kvStaleWorkHashes, "0")
	}
	a.Log.Info("block latency stats reset")
}

// IngestBlockEvents reads block events from the tailer and appends them
// to the snapshot's block history. Runs until ctx is cancelled.
func (a *Aggregator) IngestBlockEvents(ctx context.Context, events <-chan logmon.BlockEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			// Stamp the chain name at the moment the block is seen so
			// the reconcile loop can skip it if the node switches networks.
			a.mu.RLock()
			currentChain := ""
			if a.snap.Chain != nil {
				currentChain = a.snap.Chain.Chain
			}
			a.mu.RUnlock()
			rec := BlockRecord{
				Height:    ev.Height,
				FoundAt:   ev.SeenAt,
				Source:    "logmon",
				ShareDiff: ev.ShareDiff,
				Chain:     currentChain,
			}
			// Best-effort enrich with hash + coinbase reward + miner via bitcoind.
			// We look up the hash from height, then fetch the full block
			// (verbosity 2) to sum the coinbase outputs and extract the
			// payout address. Both are fire-and-forget, if bitcoind is
			// down we still record the block with whatever we have.
			if a.RPC != nil {
				lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if hash, err := a.RPC.GetBlockHash(lookupCtx, ev.Height); err == nil {
					rec.Hash = hash
					if blk, err := a.RPC.GetBlock(lookupCtx, hash); err == nil {
						rec.RewardBT = blk.CoinbaseReward()
						rec.Miner = a.minerFromCoinbase(blk.CoinbaseAddress())
					} else {
						a.Log.Warn("bitcoind getblock failed", "hash", hash, "err", err)
					}
				} else {
					a.Log.Warn("bitcoind getblockhash failed", "height", ev.Height, "err", err)
				}
				cancel()
			}
			isNew := true
			if a.Store != nil {
				inserted, err := a.Store.InsertBlock(store.Block{
					Height:    rec.Height,
					Hash:      rec.Hash,
					RewardBT:  rec.RewardBT,
					FoundAt:   rec.FoundAt,
					Source:    rec.Source,
					ShareDiff: rec.ShareDiff,
					Chain:     rec.Chain,
					Miner:     rec.Miner,
				})
				if err != nil {
					a.Log.Warn("block persist failed", "height", rec.Height, "err", err)
				} else if !inserted {
					a.Log.Warn("block at this height already recorded, duplicate or pre-existing entry, not counting as new",
						"height", rec.Height, "hash", rec.Hash)
					isNew = false
				}
			}
			if !isNew {
				continue
			}
			a.mu.Lock()
			a.blockSubmitsConfirmed++
			confirmed := a.blockSubmitsConfirmed
			a.mu.Unlock()
			if a.Store != nil {
				if err := a.Store.SetKV(kvSubmitsConfirmed, strconv.FormatInt(confirmed, 10)); err != nil {
					a.Log.Warn("confirmed count persist failed", "err", err)
				}
			}
			pushed := a.appendBlock(rec)
			a.Log.Info("block recorded", "height", rec.Height, "hash", rec.Hash, "confirmed", confirmed)
			// Push immediately so WebSocket clients see the solve
			// without waiting for the next poll tick.
			if a.OnRefresh != nil {
				a.OnRefresh(pushed)
			}
		}
	}
}

// ReconcileBlocks runs until ctx is cancelled, periodically:
//   1. Filling in missing hash/reward for blocks where the initial RPC
//      lookup failed (bitcoind hadn't indexed yet, or was down).
//   2. Comparing each non-orphaned hash against the canonical block at
//      its height; a mismatch means the network reorged us out and we
//      mark the row orphaned so the UI can render it accordingly.
// Both checks are bounded to the last reconcileLookback, so the cost
// stays constant regardless of total history size.
func (a *Aggregator) ReconcileBlocks(ctx context.Context) {
	if a.Store == nil || a.RPC == nil {
		return
	}
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reconcileOnce(ctx)
		}
	}
}

func (a *Aggregator) reconcileOnce(ctx context.Context) {
	since := time.Now().Add(-reconcileLookback)

	// We need the current chain for both enrichment and reorg detection.
	// If the first refresh hasn't completed yet, currentChain is "" and
	// any chain comparison would be meaningless, skip until the next tick.
	a.mu.RLock()
	currentChain := ""
	if a.snap.Chain != nil {
		currentChain = a.snap.Chain.Chain
	}
	a.mu.RUnlock()
	if currentChain == "" {
		return
	}

	// Pass 1: enrichment. Fetch hash + reward for any missing-data rows.
	// Skip blocks from a different chain, enriching a testnet block
	// against mainnet RPC would overwrite its hash with the wrong value.
	missing, err := a.Store.BlocksNeedingEnrichment(since)
	if err != nil {
		a.Log.Warn("reconcile: load missing failed", "err", err)
	}
	enrichedAny := false
	for _, b := range missing {
		if b.Chain != "" && b.Chain != currentChain {
			continue
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		hash := b.Hash
		reward := b.RewardBT
		miner := b.Miner
		if hash == "" {
			if h, herr := a.RPC.GetBlockHash(lookupCtx, b.Height); herr == nil {
				hash = h
			} else {
				cancel()
				continue
			}
		}
		if (reward == 0 || miner == "") && hash != "" {
			if blk, berr := a.RPC.GetBlock(lookupCtx, hash); berr == nil {
				if reward == 0 {
					reward = blk.CoinbaseReward()
				}
				if miner == "" {
					miner = a.minerFromCoinbase(blk.CoinbaseAddress())
				}
			}
		}
		cancel()
		if hash != b.Hash || reward != b.RewardBT || miner != b.Miner {
			if err := a.Store.UpdateEnrichment(b.Height, hash, reward, miner); err != nil {
				a.Log.Warn("reconcile: update enrichment failed", "height", b.Height, "err", err)
				continue
			}
			a.Log.Info("reconcile: enriched block", "height", b.Height, "hash", hash, "reward", reward, "miner", miner)
			enrichedAny = true
		}
	}

	recent, err := a.Store.Recent(64)
	if err != nil {
		a.Log.Warn("reconcile: load recent failed", "err", err)
		return
	}

	// Pass 2: reorg detection + cross-network identification.
	//
	// For each non-orphaned block within the lookback that has a hash:
	//   1. If its Chain field already marks it as a different network, skip.
	//   2. Compare our hash against the canonical hash at that height.
	//   3. On mismatch, call GetBlock(our_hash) to check if the block
	//      exists anywhere on the current chain (even if reorged out).
	//      - If GetBlock succeeds: it's a genuine reorg → mark orphaned.
	//      - If GetBlock fails ("Block not found"): the block doesn't
	//        exist on this network at all → it's from a different chain.
	//        Stamp it and leave it non-orphaned.
	//
	// This handles both stamped blocks (Chain="test") and legacy blocks
	// (Chain="") that were recorded before the chain-stamp feature.
	orphanedAny := false
	stampedAny := false
	now := time.Now()
	for _, b := range recent {
		if b.Hash == "" || !b.OrphanedAt.IsZero() {
			continue
		}
		if b.FoundAt.Before(since) {
			continue
		}
		if b.Chain != "" && b.Chain != currentChain {
			continue // already known to be from a different network
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		canonical, herr := a.RPC.GetBlockHash(lookupCtx, b.Height)
		cancel()
		if herr != nil {
			continue // RPC transient error, retry next sweep
		}
		if canonical == b.Hash {
			continue // matches the canonical chain, all good
		}
		// Hash mismatch. Determine whether it's a real reorg or a
		// cross-network block by checking if our hash exists on this chain.
		// After a real reorg, bitcoind still has the stale block in its
		// store; a cross-network hash won't exist at all.
		lookupCtx2, cancel2 := context.WithTimeout(ctx, 3*time.Second)
		_, berr := a.RPC.GetBlock(lookupCtx2, b.Hash)
		cancel2()
		if berr != nil {
			// Block not found on this network → cross-network block.
			// Stamp its chain so future sweeps skip it immediately.
			otherChain := b.Chain
			if otherChain == "" {
				otherChain = inferOtherChain(currentChain)
			}
			if err := a.Store.StampChain(b.Height, otherChain); err != nil {
				a.Log.Warn("reconcile: stamp cross-network block failed", "height", b.Height, "err", err)
				continue
			}
			a.Log.Info("reconcile: block belongs to a different network, not a reorg",
				"height", b.Height, "hash", b.Hash, "block_chain", otherChain, "current_chain", currentChain)
			stampedAny = true
			continue
		}
		// Block exists on this network but isn't canonical → genuine reorg.
		if err := a.Store.MarkOrphaned(b.Height, now); err != nil {
			a.Log.Warn("reconcile: mark orphaned failed", "height", b.Height, "err", err)
			continue
		}
		a.Log.Warn("reconcile: block orphaned by reorg",
			"height", b.Height, "ours", b.Hash, "canonical", canonical)
		orphanedAny = true
	}

	// Pass 3: un-orphan blocks that were previously false-positived.
	// This covers two cases:
	//   a) Blocks with Chain != currentChain that got orphaned before this
	//      fix was deployed, un-orphan them now.
	//   b) Same-chain blocks whose hash now matches the canonical chain
	//      (e.g. the "reorg" reverted before we checked again).
	fixedAny := false
	for _, b := range recent {
		if b.OrphanedAt.IsZero() || b.Hash == "" {
			continue
		}
		if b.FoundAt.Before(since) {
			continue
		}
		if b.Chain != "" && b.Chain != currentChain {
			// Cross-network orphan from before the fix, un-orphan.
			if err := a.Store.UnmarkOrphaned(b.Height); err != nil {
				a.Log.Warn("reconcile: unmark cross-network orphan failed", "height", b.Height, "err", err)
				continue
			}
			a.Log.Info("reconcile: cleared false orphan (different network)",
				"height", b.Height, "block_chain", b.Chain, "current_chain", currentChain)
			fixedAny = true
			continue
		}
		// For same-chain or legacy blocks: verify via RPC.
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, berr := a.RPC.GetBlock(lookupCtx, b.Hash)
		cancel()
		if berr != nil {
			// Block doesn't exist on this chain, cross-network.
			otherChain := b.Chain
			if otherChain == "" || otherChain == currentChain {
				otherChain = inferOtherChain(currentChain)
			}
			if err := a.Store.UnmarkOrphanedStampChain(b.Height, otherChain); err != nil {
				a.Log.Warn("reconcile: unmark+stamp cross-network orphan failed", "height", b.Height, "err", err)
				continue
			}
			a.Log.Info("reconcile: cleared false orphan (block not on this network)",
				"height", b.Height, "hash", b.Hash, "stamped_chain", otherChain)
			fixedAny = true
			continue
		}
		// Block exists on this chain, check if it's canonical again.
		lookupCtx2, cancel2 := context.WithTimeout(ctx, 3*time.Second)
		canonical, herr := a.RPC.GetBlockHash(lookupCtx2, b.Height)
		cancel2()
		if herr != nil {
			continue
		}
		if canonical == b.Hash {
			if err := a.Store.UnmarkOrphaned(b.Height); err != nil {
				a.Log.Warn("reconcile: unmark reverted-reorg failed", "height", b.Height, "err", err)
				continue
			}
			a.Log.Info("reconcile: cleared orphan, hash matches canonical again",
				"height", b.Height, "hash", b.Hash)
			fixedAny = true
		}
	}

	// Pass 4: stamp chain on legacy/mis-stamped blocks that have a hash.
	// Handles Chain="" (never stamped), "other" (old fallback), and "test"
	// (wrong identifier, testnet4 reports as "testnet4", not "test").
	// Uses GetBlock(hash) to determine if the block belongs to the current
	// network or a different one. No lookback restriction, this is a
	// one-time migration for pre-existing rows.
	for _, b := range recent {
		if (b.Chain != "" && b.Chain != "other" && b.Chain != "test") || b.Hash == "" {
			continue // already properly stamped or no hash to check
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, berr := a.RPC.GetBlock(lookupCtx, b.Hash)
		cancel()
		if berr != nil {
			// Block not found on this network, infer the other chain.
			inferredChain := inferOtherChain(currentChain)
			if err := a.Store.StampChain(b.Height, inferredChain); err != nil {
				a.Log.Warn("reconcile: stamp legacy block failed", "height", b.Height, "err", err)
				continue
			}
			a.Log.Info("reconcile: stamped legacy block as other-network",
				"height", b.Height, "hash", b.Hash, "inferred_chain", inferredChain)
			stampedAny = true
		} else {
			// Block exists on this network
			if err := a.Store.StampChain(b.Height, currentChain); err != nil {
				a.Log.Warn("reconcile: stamp legacy block failed", "height", b.Height, "err", err)
				continue
			}
			a.Log.Info("reconcile: stamped legacy block as current chain",
				"height", b.Height, "chain", currentChain)
			stampedAny = true
		}
	}

	if enrichedAny || orphanedAny || stampedAny || fixedAny {
		a.loadPersistedBlocks()
		a.mu.RLock()
		snap := a.snap
		a.mu.RUnlock()
		if a.OnRefresh != nil && len(snap.RecentBlocks) > 0 {
			a.OnRefresh(snap)
		}
	}
}

// minerFromCoinbase matches a coinbase payout address to a full workername
// from the current worker list. In solo mode, stratum usernames are
// "address" or "address.label", and the coinbase pays the address portion.
// Returns the first matching workername (address.worker), or just the
// address if no worker match is found.
func (a *Aggregator) minerFromCoinbase(addr string) string {
	if addr == "" {
		return ""
	}
	a.mu.RLock()
	workers := a.snap.Workers
	a.mu.RUnlock()
	for _, w := range workers {
		// Worker.User is "address.workername" in ckpool. The address
		// portion is everything before the first dot.
		wAddr := w.User
		if dot := strings.IndexByte(wAddr, '.'); dot >= 0 {
			wAddr = wAddr[:dot]
		}
		if wAddr == addr {
			return w.User + "." + w.Worker
		}
	}
	return addr
}

// maxBlockHistory caps in-memory block history. Persistence comes in
// Phase 2b.5 via SQLite; for now recent blocks survive only this
// process's lifetime.
const maxBlockHistory = 256

// appendBlock records a new block and returns a copy of the current
// snapshot with the updated history attached, suitable for an immediate
// WebSocket broadcast.
func (a *Aggregator) appendBlock(rec BlockRecord) Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blocks = append(a.blocks, rec)
	if len(a.blocks) > maxBlockHistory {
		a.blocks = a.blocks[len(a.blocks)-maxBlockHistory:]
	}
	snap := a.snap
	snap.RecentBlocks = make([]BlockRecord, len(a.blocks))
	copy(snap.RecentBlocks, a.blocks)
	a.snap = snap
	return snap
}

// loadPersistedBlocks seeds a.blocks from the store so history survives
// restarts. Safe to call with a nil Store, becomes a no-op.
func (a *Aggregator) loadPersistedBlocks() {
	if a.Store == nil {
		return
	}
	rows, err := a.Store.Recent(maxBlockHistory)
	if err != nil {
		a.Log.Warn("block history load failed", "err", err)
		return
	}
	// Store returns newest-first; a.blocks is newest-last.
	recs := make([]BlockRecord, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		recs = append(recs, BlockRecord{
			Height:     r.Height,
			Hash:       r.Hash,
			RewardBT:   r.RewardBT,
			FoundAt:    r.FoundAt,
			Source:     r.Source,
			ShareDiff:  r.ShareDiff,
			OrphanedAt: timePtr(r.OrphanedAt),
			Chain:      r.Chain,
			Miner:      r.Miner,
		})
	}
	a.mu.Lock()
	a.blocks = recs
	// Surface refreshed history into the live snapshot so /api/snapshot
	// reflects the latest store state without waiting for the next
	// refresh tick (used by the reconcile loop).
	if a.snap.GeneratedAt.IsZero() {
		a.mu.Unlock()
		a.Log.Info("block history loaded", "count", len(recs))
		return
	}
	snap := a.snap
	if len(recs) > 0 {
		snap.RecentBlocks = make([]BlockRecord, len(recs))
		copy(snap.RecentBlocks, recs)
	} else {
		snap.RecentBlocks = nil
	}
	a.snap = snap
	a.mu.Unlock()
	a.Log.Info("block history loaded", "count", len(recs))
}

// Blocks returns a copy of the recent block history, newest last.
func (a *Aggregator) Blocks() []BlockRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]BlockRecord, len(a.blocks))
	copy(out, a.blocks)
	return out
}

// BlocksFromStore reads blocks directly from the DB (bypassing in-memory
// cache) for debugging discrepancies between memory and store.
func (a *Aggregator) BlocksFromStore() []BlockRecord {
	if a.Store == nil {
		return nil
	}
	rows, err := a.Store.Recent(maxBlockHistory)
	if err != nil {
		return nil
	}
	out := make([]BlockRecord, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		out = append(out, BlockRecord{
			Height:     r.Height,
			Hash:       r.Hash,
			RewardBT:   r.RewardBT,
			FoundAt:    r.FoundAt,
			Source:     r.Source,
			ShareDiff:  r.ShareDiff,
			OrphanedAt: timePtr(r.OrphanedAt),
			Chain:      r.Chain,
			Miner:      r.Miner,
		})
	}
	return out
}

