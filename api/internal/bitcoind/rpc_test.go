package bitcoind

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ---- isRetryable unit tests ----

func TestIsRetryable_TransportErrors(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("bitcoind rpc: connection refused"), true},
		{fmt.Errorf("bitcoind rpc: connection reset by peer"), true},
		{fmt.Errorf("bitcoind rpc: EOF"), true},
		{fmt.Errorf("bitcoind rpc: i/o timeout"), true},
		{fmt.Errorf("bitcoind rpc: deadline exceeded"), true},
		{fmt.Errorf("bitcoind rpc: broken pipe"), true},
		{fmt.Errorf("bitcoind rpc: 503: service unavailable"), true},
		{fmt.Errorf("bitcoind rpc: 502: bad gateway"), true},
	}
	for _, tc := range cases {
		if got := isRetryable(tc.err); got != tc.want {
			t.Errorf("isRetryable(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestIsRetryable_WarmupRPCError(t *testing.T) {
	warmup := &rpcError{Code: -28, Message: "Loading block index"}
	if !isRetryable(warmup) {
		t.Error("warmup error (-28) should be retryable")
	}

	msgOnly := &rpcError{Code: -1, Message: "Bitcoin is warming up"}
	if !isRetryable(msgOnly) {
		t.Error("'warming up' message should be retryable")
	}
}

func TestIsRetryable_SemanticRPCError(t *testing.T) {
	blockNotFound := &rpcError{Code: -5, Message: "Block not found"}
	if isRetryable(blockNotFound) {
		t.Error("block-not-found (-5) should NOT be retryable")
	}

	invalidParam := &rpcError{Code: -1, Message: "Invalid parameter"}
	if isRetryable(invalidParam) {
		t.Error("invalid-parameter should NOT be retryable")
	}
}

func TestIsRetryable_Nil(t *testing.T) {
	if isRetryable(nil) {
		t.Error("nil should not be retryable")
	}
}

// ---- Call retry integration tests ----

// jsonRPCServer starts an httptest server that counts requests and serves the
// provided response JSON. If failFirst > 0, the first failFirst requests
// return HTTP 503 before that.
func jsonRPCServer(t *testing.T, failFirst int, result any) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if int(n) <= failFirst {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestCall_SuccessOnFirstTry(t *testing.T) {
	srv, calls := jsonRPCServer(t, 0, "blockhash_abc")
	rpc := NewRPC(srv.URL, "u", "p", 5*time.Second)

	var out string
	if err := rpc.Call(context.Background(), "getblockhash", []any{100}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "blockhash_abc" {
		t.Errorf("result = %q, want blockhash_abc", out)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
}

func TestCall_RetryOnTransient503(t *testing.T) {
	srv, calls := jsonRPCServer(t, 2, "blockhash_xyz")
	rpc := NewRPC(srv.URL, "u", "p", 5*time.Second)

	var out string
	if err := rpc.Call(context.Background(), "getblockhash", []any{200}, &out); err != nil {
		t.Fatalf("Call failed after retries: %v", err)
	}
	if out != "blockhash_xyz" {
		t.Errorf("result = %q, want blockhash_xyz", out)
	}
	// First 2 calls fail, third succeeds.
	if n := atomic.LoadInt32(calls); n != 3 {
		t.Errorf("calls = %d, want 3", n)
	}
}

func TestCall_ExhaustsRetries(t *testing.T) {
	// Server always returns 503, all 3 attempts should fail.
	srv, calls := jsonRPCServer(t, 99, nil)
	rpc := NewRPC(srv.URL, "u", "p", 5*time.Second)

	var out string
	if err := rpc.Call(context.Background(), "getblockhash", []any{300}, &out); err == nil {
		t.Fatal("expected error after exhausted retries, got nil")
	}
	if n := atomic.LoadInt32(calls); n != 3 {
		t.Errorf("calls = %d, want 3 (maxAttempts)", n)
	}
}

func TestCall_NoRetryOnSemanticRPCError(t *testing.T) {
	// Server returns a semantic RPC error (block not found). Should not be
	// retried, the answer is definitive.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		json.NewEncoder(w).Encode(map[string]any{
			"result": nil,
			"error":  map[string]any{"code": -5, "message": "Block not found"},
		})
	}))
	t.Cleanup(srv.Close)

	rpc := NewRPC(srv.URL, "u", "p", 5*time.Second)
	var out string
	err := rpc.Call(context.Background(), "getblockhash", []any{999}, &out)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("calls = %d, want 1 (no retry on -5)", n)
	}
}

func TestCall_RetryOnWarmup(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"result": nil,
				"error":  map[string]any{"code": -28, "message": "Loading block index"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"result": "warmup_hash", "error": nil})
	}))
	t.Cleanup(srv.Close)

	rpc := NewRPC(srv.URL, "u", "p", 5*time.Second)
	var out string
	if err := rpc.Call(context.Background(), "getblockhash", []any{1}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "warmup_hash" {
		t.Errorf("result = %q, want warmup_hash", out)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("calls = %d, want 2", n)
	}
}

// ---- CoinbaseReward unit test ----

func TestCoinbaseReward(t *testing.T) {
	blk := &BlockVerbose2{
		Tx: []BlockTx{
			{
				Vout: []BlockVout{
					{Value: 3.125},
					{Value: 0.00042},
				},
			},
			// Non-coinbase tx; should be ignored.
			{Vout: []BlockVout{{Value: 99}}},
		},
	}
	want := 3.125 + 0.00042
	if got := blk.CoinbaseReward(); got != want {
		t.Errorf("CoinbaseReward = %v, want %v", got, want)
	}
}

func TestCoinbaseReward_EmptyBlock(t *testing.T) {
	blk := &BlockVerbose2{}
	if got := blk.CoinbaseReward(); got != 0 {
		t.Errorf("empty block CoinbaseReward = %v, want 0", got)
	}
}
