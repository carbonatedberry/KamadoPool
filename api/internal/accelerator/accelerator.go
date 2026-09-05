// Package accelerator manages transaction priority boosting via
// bitcoind's prioritisetransaction RPC.
package accelerator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/kamadopool/kamado-api/internal/bitcoind"
	"github.com/kamadopool/kamado-api/internal/store"
)

// Service manages the lifecycle of boosted transactions.
type Service struct {
	RPC   *bitcoind.RPC
	Store *store.BlockStore
	Log   *slog.Logger
}

func NewService(rpc *bitcoind.RPC, st *store.BlockStore, log *slog.Logger) *Service {
	return &Service{RPC: rpc, Store: st, Log: log}
}

// MaxFeerateVB is the hard cap to prevent accidental extreme boosts.
const MaxFeerateVB = 2000.0

// AccelerateResult is returned on a successful boost.
type AccelerateResult struct {
	Txid            string  `json:"txid"`
	OriginalFeerate float64 `json:"original_feerate"`
	BoostedFeerate  float64 `json:"boosted_feerate"`
	FeeDelta        int64   `json:"fee_delta"`
	Vsize           int64   `json:"vsize"`
	// Actual revenue impact: coinbasevalue_before - coinbasevalue_after.
	// Zero means the mempool isn't full (no tx was displaced).
	// Negative means getblocktemplate failed (couldn't measure).
	FeeLostSats     int64   `json:"fee_lost_sats"`
	FeeLostError    string  `json:"fee_lost_error,omitempty"`
}

// marginalFeeLost estimates the fee revenue lost by inserting a boosted
// transaction into the template. It finds the lowest fee-rate transaction
// in the current template (the one that would be displaced) and returns
// the difference: displaced_fee - boosted_tx_real_fee. If the boosted tx
// is already in the template or the template has room, returns 0.
func marginalFeeLost(tpl *bitcoind.BlockTemplate, txid string, txVsize int64) int64 {
	if len(tpl.Transactions) == 0 {
		return 0
	}

	// Check if the tx is already in the template (nothing displaced).
	for _, tx := range tpl.Transactions {
		if tx.Txid == txid {
			return 0
		}
	}

	// Find the marginal transaction: lowest fee-rate in the template.
	var marginal *bitcoind.TemplateTx
	var marginalRate float64 = math.MaxFloat64
	for i := range tpl.Transactions {
		tx := &tpl.Transactions[i]
		// Weight to vsize: ceil(weight/4)
		vsize := (tx.Weight + 3) / 4
		if vsize <= 0 {
			continue
		}
		rate := float64(tx.Fee) / float64(vsize)
		if rate < marginalRate {
			marginalRate = rate
			marginal = tx
		}
	}

	if marginal == nil {
		return 0
	}

	// The displaced tx's fee is the revenue we lose. We don't add the
	// boosted tx's real fee back because the pool never had it, the tx
	// wasn't in the template before the boost.
	lost := marginal.Fee
	if lost < 0 {
		lost = 0
	}
	return lost
}

// Accelerate boosts a transaction to the target feerate (sat/vB).
func (s *Service) Accelerate(ctx context.Context, txid string, targetFeerateVB float64) (*AccelerateResult, error) {
	if targetFeerateVB > MaxFeerateVB {
		return nil, fmt.Errorf("target feerate %.1f exceeds maximum %d sat/vB", targetFeerateVB, int(MaxFeerateVB))
	}

	entry, err := s.RPC.GetMempoolEntry(ctx, txid)
	if err != nil {
		return nil, fmt.Errorf("getmempoolentry: %w", err)
	}

	// Use base fee (real, not priority-adjusted) to compute current feerate.
	baseFeeSats := entry.Fees.Base * 1e8
	currentFeerate := baseFeeSats / float64(entry.Vsize)

	// Delta is relative to the modified fee (which includes any prior boosts).
	modifiedFeeSats := entry.Fees.Modified * 1e8
	targetFeeSats := targetFeerateVB * float64(entry.Vsize)
	delta := int64(math.Ceil(targetFeeSats - modifiedFeeSats))

	if delta <= 0 {
		return nil, fmt.Errorf("target feerate %.2f sat/vB is not higher than current effective %.2f sat/vB",
			targetFeerateVB, modifiedFeeSats/float64(entry.Vsize))
	}

	// Estimate revenue impact from the pre-boost template. The boosted tx
	// will displace the marginal (lowest fee-rate) transaction in the
	// template. We compute this from the single template snapshot to avoid
	// a race with new mempool arrivals between two getblocktemplate calls.
	var feeLost int64
	var tplErr string
	tpl, err := s.RPC.GetBlockTemplate(ctx)
	if err != nil {
		tplErr = fmt.Sprintf("getblocktemplate: %s", bitcoind.RPCErrorMessage(err))
		s.Log.Warn("accelerate: getblocktemplate failed", "err", err)
	} else {
		feeLost = marginalFeeLost(tpl, txid, entry.Vsize)
		s.Log.Info("accelerate: fee impact estimated",
			"marginal_fee_lost", feeLost, "boosted_tx_fee", int64(baseFeeSats))
	}

	if err := s.RPC.PrioritiseTransaction(ctx, txid, delta); err != nil {
		return nil, fmt.Errorf("prioritisetransaction: %w", err)
	}

	rec := store.BoostedTx{
		Txid:            txid,
		OriginalFeerate: currentFeerate,
		BoostedFeerate:  targetFeerateVB,
		FeeDelta:        delta,
		Vsize:           entry.Vsize,
		BoostedAt:       time.Now().Unix(),
	}
	if s.Store != nil {
		if err := s.Store.InsertBoostedTx(rec); err != nil {
			s.Log.Warn("failed to persist boosted tx", "txid", txid, "err", err)
		}
	}

	s.Log.Info("tx accelerated", "txid", txid, "delta_sats", delta,
		"original_rate", currentFeerate, "target_rate", targetFeerateVB,
		"fee_lost_sats", feeLost)

	return &AccelerateResult{
		Txid:            txid,
		OriginalFeerate: currentFeerate,
		BoostedFeerate:  targetFeerateVB,
		FeeDelta:        delta,
		Vsize:           entry.Vsize,
		FeeLostSats:     feeLost,
		FeeLostError:    tplErr,
	}, nil
}

// Cancel reverses a previously applied priority boost.
func (s *Service) Cancel(ctx context.Context, txid string) error {
	if s.Store == nil {
		return fmt.Errorf("no store available")
	}
	rec, err := s.Store.GetBoostedTx(txid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("transaction %s is not in the boosted list", txid)
		}
		return err
	}

	// Reverse the delta.
	if err := s.RPC.PrioritiseTransaction(ctx, txid, -rec.FeeDelta); err != nil {
		// If tx is no longer in mempool, the reversal is a no-op at the
		// bitcoind level but we still remove our record.
		if !bitcoind.IsRPCError(err, -5) {
			return fmt.Errorf("prioritisetransaction (cancel): %w", err)
		}
	}

	if err := s.Store.RemoveBoostedTx(txid); err != nil {
		return fmt.Errorf("remove record: %w", err)
	}

	s.Log.Info("tx acceleration cancelled", "txid", txid, "reversed_delta", rec.FeeDelta)
	return nil
}

// MaxFeerate returns the highest real feerate (ignoring priority
// adjustments) in the mempool, doubled, capped at MaxFeerateVB.
// Uses fees.base so previously-boosted txs don't skew the result.
func (s *Service) MaxFeerate(ctx context.Context) (float64, error) {
	pool, err := s.RPC.GetRawMempoolVerbose(ctx)
	if err != nil {
		return 0, fmt.Errorf("getrawmempool: %w", err)
	}
	var maxRate float64
	for _, entry := range pool {
		if entry.Vsize <= 0 {
			continue
		}
		// Use base fee (actual fee paid), not modified (includes priority boosts).
		rate := (entry.Fees.Base * 1e8) / float64(entry.Vsize)
		if rate > maxRate {
			maxRate = rate
		}
	}
	if maxRate == 0 {
		return 100, nil // sensible default if mempool is empty
	}
	result := math.Ceil(maxRate * 2)
	if result > MaxFeerateVB {
		result = MaxFeerateVB
	}
	return result, nil
}

// List returns all currently tracked boosted transactions.
func (s *Service) List() ([]store.BoostedTx, error) {
	if s.Store == nil {
		return nil, nil
	}
	return s.Store.ListBoostedTxs()
}

// Cleanup periodically checks if boosted txs have left the mempool
// (confirmed or evicted) and removes them from tracking. Run as a
// background goroutine.
func (s *Service) Cleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupOnce(ctx)
		}
	}
}

func (s *Service) cleanupOnce(ctx context.Context) {
	if s.Store == nil {
		return
	}
	txs, err := s.Store.ListBoostedTxs()
	if err != nil {
		s.Log.Warn("accelerator cleanup: list failed", "err", err)
		return
	}
	for _, tx := range txs {
		_, err := s.RPC.GetMempoolEntry(ctx, tx.Txid)
		if err != nil && bitcoind.IsRPCError(err, -5) {
			// Tx no longer in mempool, confirmed or dropped.
			if err := s.Store.RemoveBoostedTx(tx.Txid); err != nil {
				s.Log.Warn("accelerator cleanup: remove failed", "txid", tx.Txid, "err", err)
			} else {
				s.Log.Info("accelerator: tx left mempool, removed from tracking", "txid", tx.Txid)
			}
		}
	}
}
