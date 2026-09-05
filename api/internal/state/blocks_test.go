package state

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kamadopool/kamado-api/internal/bitcoind"
	"github.com/kamadopool/kamado-api/internal/logmon"
	"github.com/kamadopool/kamado-api/internal/store"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func openTempStore(t *testing.T) *store.BlockStore {
	t.Helper()
	f, err := os.CreateTemp("", "state_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	s, err := store.Open(f.Name())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// mockRPC starts an httptest server whose responses are determined by the
// supplied handler. The handler receives the RPC method name and the raw
// params JSON and returns the value to place in "result". Returning nil
// sends an empty result; returning a non-nil error (as rpcErrResp) sends
// an RPC error response.
type rpcErrResp struct {
	Code    int
	Message string
}

func mockRPC(t *testing.T, handler func(method string) (any, *rpcErrResp)) *bitcoind.RPC {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		result, rpcErr := handler(req.Method)
		if rpcErr != nil {
			json.NewEncoder(w).Encode(map[string]any{
				"result": nil,
				"error":  map[string]any{"code": rpcErr.Code, "message": rpcErr.Message},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	t.Cleanup(srv.Close)
	return bitcoind.NewRPC(srv.URL, "u", "p", 5*time.Second)
}

func newAgg(st *store.BlockStore, rpc *bitcoind.RPC) *Aggregator {
	return &Aggregator{
		Store: st,
		RPC:   rpc,
		Log:   discardLog(),
		ready: make(chan struct{}),
	}
}

// setChain sets the aggregator's snapshot chain without going through refresh.
func setChain(agg *Aggregator, chain string) {
	agg.mu.Lock()
	agg.snap.Chain = &bitcoind.BlockchainInfo{Chain: chain}
	agg.mu.Unlock()
}

// ---- reconcileOnce tests ----

// TestReconcileOnce_ChainFilter verifies that a block recorded on testnet
// is NOT orphaned when the node is now on mainnet, even if the canonical
// hash at that height on mainnet is different.
func TestReconcileOnce_ChainFilter_NoFalseOrphan(t *testing.T) {
	st := openTempStore(t)

	// Insert a testnet block with a known hash.
	_, err := st.InsertBlock(store.Block{
		Height:   100,
		Hash:     "testnet_hash_aaa",
		RewardBT: 50,
		FoundAt:  time.Now(),
		Source:   "test",
		Chain:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mock RPC: getblockhash returns a different mainnet hash; getblock
	// for our testnet hash returns "Block not found", it doesn't exist
	// on mainnet at all, confirming it's cross-network not a reorg.
	rpc := mockRPC(t, func(method string) (any, *rpcErrResp) {
		switch method {
		case "getblockhash":
			return "mainnet_hash_bbb", nil
		case "getblock":
			return nil, &rpcErrResp{Code: -5, Message: "Block not found"}
		}
		return nil, nil
	})

	agg := newAgg(st, rpc)
	setChain(agg, "main") // switched to mainnet

	agg.reconcileOnce(context.Background())

	blocks, err := st.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if b.Height == 100 && !b.OrphanedAt.IsZero() {
			t.Error("testnet block was falsely orphaned after switching to mainnet, chain filter broken")
		}
	}
}

// TestReconcileOnce_GenuineReorg verifies that when the canonical block at a
// recorded height has a different hash (real on-chain reorg), the block is
// marked orphaned.
func TestReconcileOnce_GenuineReorg(t *testing.T) {
	st := openTempStore(t)

	_, err := st.InsertBlock(store.Block{
		Height:   200,
		Hash:     "our_hash_aaaa",
		RewardBT: 3.125,
		FoundAt:  time.Now(),
		Source:   "test",
		Chain:    "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// getblockhash returns a different canonical hash (reorg happened).
	// getblock for our hash SUCCEEDS, the block still exists in bitcoind's
	// block store after a reorg, it's just no longer on the active chain.
	// This distinguishes a real reorg from a cross-network block.
	rpc := mockRPC(t, func(method string) (any, *rpcErrResp) {
		switch method {
		case "getblockhash":
			return "canonical_bbbb", nil
		case "getblock":
			// Block exists (reorged but still in store)
			return map[string]any{"hash": "our_hash_aaaa", "height": 200, "confirmations": -1}, nil
		}
		return nil, nil
	})

	agg := newAgg(st, rpc)
	setChain(agg, "main")

	agg.reconcileOnce(context.Background())

	blocks, err := st.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range blocks {
		if b.Height == 200 {
			found = true
			if b.OrphanedAt.IsZero() {
				t.Error("block at reorged height was not marked orphaned")
			}
		}
	}
	if !found {
		t.Error("block 200 not found in store after reconcile")
	}
}

// TestReconcileOnce_RPCError_NoOrphan verifies that a transient RPC error
// (getblockhash fails) does not cause a block to be orphaned. The next
// reconcile sweep will retry.
func TestReconcileOnce_RPCError_NoOrphan(t *testing.T) {
	st := openTempStore(t)

	st.InsertBlock(store.Block{
		Height:   300,
		Hash:     "our_hash_cccc",
		RewardBT: 3.125,
		FoundAt:  time.Now(),
		Source:   "test",
		Chain:    "main",
	})

	rpc := mockRPC(t, func(method string) (any, *rpcErrResp) {
		if method == "getblockhash" {
			return nil, &rpcErrResp{Code: -5, Message: "Block not found"}
		}
		return nil, nil
	})

	agg := newAgg(st, rpc)
	setChain(agg, "main")

	agg.reconcileOnce(context.Background())

	blocks, _ := st.Recent(10)
	for _, b := range blocks {
		if b.Height == 300 && !b.OrphanedAt.IsZero() {
			t.Error("block was orphaned on RPC error, should only orphan on definitive hash mismatch")
		}
	}
}

// TestReconcileOnce_LegacyBlock_GenuineReorg verifies that a legacy block
// (Chain == "") is still checked for reorgs on the current chain, preserving
// backward compatibility for installs that existed before chain was tracked.
func TestReconcileOnce_LegacyBlock_StillChecked(t *testing.T) {
	st := openTempStore(t)

	st.InsertBlock(store.Block{
		Height:   400,
		Hash:     "legacy_hash",
		RewardBT: 3.125,
		FoundAt:  time.Now(),
		Source:   "test",
		Chain:    "", // legacy, no chain recorded
	})

	// Canonical hash differs AND getblock succeeds (block exists on this
	// chain but was reorged out). Should still orphan legacy blocks.
	rpc := mockRPC(t, func(method string) (any, *rpcErrResp) {
		switch method {
		case "getblockhash":
			return "different_canonical", nil
		case "getblock":
			// Block exists in this chain's store, genuine reorg
			return map[string]any{"hash": "legacy_hash", "height": 400, "confirmations": -1}, nil
		}
		return nil, nil
	})

	agg := newAgg(st, rpc)
	setChain(agg, "main")

	agg.reconcileOnce(context.Background())

	blocks, _ := st.Recent(10)
	for _, b := range blocks {
		if b.Height == 400 && b.OrphanedAt.IsZero() {
			t.Error("legacy block (empty chain) should still be checked for reorgs")
		}
	}
}

// TestReconcileOnce_LegacyBlock_CrossNetwork verifies that a legacy block
// (Chain == "") whose hash doesn't exist on the current chain is identified
// as cross-network and stamped rather than orphaned.
func TestReconcileOnce_LegacyBlock_CrossNetwork(t *testing.T) {
	st := openTempStore(t)

	st.InsertBlock(store.Block{
		Height:   401,
		Hash:     "other_network_hash",
		RewardBT: 50,
		FoundAt:  time.Now(),
		Source:   "test",
		Chain:    "", // legacy, no chain recorded
	})

	// Canonical hash differs AND getblock fails (block doesn't exist on
	// this network at all). Should NOT orphan, should stamp as "other".
	rpc := mockRPC(t, func(method string) (any, *rpcErrResp) {
		switch method {
		case "getblockhash":
			return "mainnet_canonical", nil
		case "getblock":
			return nil, &rpcErrResp{Code: -5, Message: "Block not found"}
		}
		return nil, nil
	})

	agg := newAgg(st, rpc)
	setChain(agg, "main")

	agg.reconcileOnce(context.Background())

	blocks, _ := st.Recent(10)
	for _, b := range blocks {
		if b.Height == 401 {
			if !b.OrphanedAt.IsZero() {
				t.Error("cross-network legacy block was falsely orphaned")
			}
			if b.Chain != "testnet4" {
				t.Errorf("expected chain stamped as 'testnet4' (inferred from current=main), got %q", b.Chain)
			}
		}
	}
}

// ---- IngestBlockEvents tests ----

// TestIngestBlockEvents_NewBlock verifies that a fresh block event is
// persisted to the store and the confirmed counter increments.
func TestIngestBlockEvents_NewBlock(t *testing.T) {
	st := openTempStore(t)
	agg := newAgg(st, nil) // nil RPC skips enrichment
	setChain(agg, "main")

	events := make(chan logmon.BlockEvent, 1)

	done := make(chan struct{})
	agg.OnRefresh = func(_ Snapshot) { close(done) }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go agg.IngestBlockEvents(ctx, events)

	events <- logmon.BlockEvent{Height: 500, SeenAt: time.Now(), ShareDiff: 32768}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed out waiting for block to be processed")
	}

	blocks, _ := st.Recent(10)
	if len(blocks) == 0 {
		t.Fatal("block not persisted to store")
	}
	if blocks[0].Height != 500 {
		t.Errorf("Height = %d, want 500", blocks[0].Height)
	}
	if blocks[0].Chain != "main" {
		t.Errorf("Chain = %q, want main", blocks[0].Chain)
	}

	agg.mu.RLock()
	confirmed := agg.blockSubmitsConfirmed
	agg.mu.RUnlock()
	if confirmed != 1 {
		t.Errorf("blockSubmitsConfirmed = %d, want 1", confirmed)
	}
}

// TestIngestBlockEvents_Dedup verifies that sending the same block height
// twice does not double-count the confirmed counter or call OnRefresh twice.
func TestIngestBlockEvents_Dedup(t *testing.T) {
	st := openTempStore(t)

	// Pre-insert the block so the first ingest sees it as a duplicate.
	st.InsertBlock(store.Block{Height: 600, FoundAt: time.Now(), Chain: "main"})

	var refreshCount int
	var mu sync.Mutex

	agg := newAgg(st, nil)
	agg.OnRefresh = func(_ Snapshot) {
		mu.Lock()
		refreshCount++
		mu.Unlock()
	}
	setChain(agg, "main")

	events := make(chan logmon.BlockEvent, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		agg.IngestBlockEvents(ctx, events)
	}()

	events <- logmon.BlockEvent{Height: 600, SeenAt: time.Now()}
	close(events)
	wg.Wait()

	agg.mu.RLock()
	confirmed := agg.blockSubmitsConfirmed
	agg.mu.RUnlock()
	if confirmed != 0 {
		t.Errorf("blockSubmitsConfirmed = %d, want 0 for duplicate", confirmed)
	}

	mu.Lock()
	rc := refreshCount
	mu.Unlock()
	if rc != 0 {
		t.Errorf("OnRefresh called %d times, want 0 for duplicate block", rc)
	}
}

// TestIngestBlockEvents_ChainStamping verifies that the block record stored
// carries the chain name from the current snapshot at ingest time.
func TestIngestBlockEvents_ChainStamping(t *testing.T) {
	st := openTempStore(t)
	agg := newAgg(st, nil)
	setChain(agg, "test") // testnet at the time of the solve

	events := make(chan logmon.BlockEvent, 1)
	done := make(chan struct{})
	agg.OnRefresh = func(_ Snapshot) { close(done) }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go agg.IngestBlockEvents(ctx, events)

	events <- logmon.BlockEvent{Height: 700, SeenAt: time.Now()}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed out waiting for block to be processed")
	}

	blocks, _ := st.Recent(10)
	if len(blocks) == 0 {
		t.Fatal("block not found in store")
	}
	if blocks[0].Chain != "test" {
		t.Errorf("Chain = %q, want test, chain stamping broken", blocks[0].Chain)
	}
}
