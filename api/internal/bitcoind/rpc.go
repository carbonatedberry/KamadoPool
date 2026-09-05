// Package bitcoind provides a minimal Bitcoin Core JSON-RPC client.
// Only the methods kamado-api actually needs are implemented.
package bitcoind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type RPC struct {
	URL      string
	User     string
	Password string
	HTTP     *http.Client
}

func NewRPC(url, user, password string, timeout time.Duration) *RPC {
	return &RPC{
		URL:      url,
		User:     user,
		Password: password,
		HTTP:     &http.Client{Timeout: timeout},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	ID     string          `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("bitcoind rpc error %d: %s", e.Code, e.Message)
}

// Call performs a JSON-RPC request and unmarshals the result. Transient
// failures (transport errors, 502/503/504, or RPC errors with an
// "in warmup"/"loading"/"verifying" message that bitcoind returns
// during startup) are retried up to twice with a short backoff. RPC
// errors with semantic codes (e.g. block-not-found) are returned
// immediately because retrying won't change the answer.
func (c *RPC) Call(ctx context.Context, method string, params []any, out any) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := c.callOnce(ctx, method, params, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxAttempts || ctx.Err() != nil {
			return err
		}
		backoff := time.Duration(attempt) * 200 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return lastErr
}

func (c *RPC) callOnce(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "1.0",
		ID:      "kamado",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.User, c.Password)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("bitcoind rpc: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("bitcoind rpc: read body: %w", err)
	}
	// 5xx without a parseable body: surface as transport-level error
	// so isRetryable() can flag it.
	var rr rpcResponse
	if jerr := json.Unmarshal(raw, &rr); jerr != nil {
		if resp.StatusCode >= 500 {
			return fmt.Errorf("bitcoind rpc: %d: %s", resp.StatusCode, truncate(string(raw), 256))
		}
		return fmt.Errorf("bitcoind rpc: unmarshal (status %d): %w: %s", resp.StatusCode, jerr, truncate(string(raw), 256))
	}
	if rr.Error != nil {
		return rr.Error
	}
	if out != nil {
		return json.Unmarshal(rr.Result, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// isRetryable distinguishes "the RPC didn't reach a definitive answer
// yet" (transport-level errors, 5xx, bitcoind warm-up/loading) from
// "bitcoind answered, the answer is no" (RPC error with a code, e.g.
// -5 block not found). We retry the first kind and bubble the second.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// rpcError values come from bitcoind itself, check for warmup/
	// loading messages that mean "ask again in a moment".
	var rerr *rpcError
	if errors.As(err, &rerr) {
		// -28 is RPC_IN_WARMUP per bitcoin/src/rpc/protocol.h
		if rerr.Code == -28 {
			return true
		}
		msg := strings.ToLower(rerr.Message)
		if strings.Contains(msg, "warming up") ||
			strings.Contains(msg, "loading") ||
			strings.Contains(msg, "verifying") ||
			strings.Contains(msg, "rewinding") ||
			strings.Contains(msg, "still busy") {
			return true
		}
		return false
	}
	// Wrapped transport / 5xx / read errors all flow through fmt.Errorf
	// with the "bitcoind rpc:" prefix and no rpcError target.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "eof"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, " 502"), strings.Contains(msg, " 503"), strings.Contains(msg, " 504"):
		return true
	}
	return false
}

// ---- typed method wrappers ----

type BlockchainInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	BestBlockHash        string  `json:"bestblockhash"`
	Difficulty           float64 `json:"difficulty"`
	MedianTime           int64   `json:"mediantime"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
}

func (c *RPC) GetBlockchainInfo(ctx context.Context) (*BlockchainInfo, error) {
	var out BlockchainInfo
	if err := c.Call(ctx, "getblockchaininfo", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBlockHash returns the block hash at the given height.
func (c *RPC) GetBlockHash(ctx context.Context, height int64) (string, error) {
	var out string
	if err := c.Call(ctx, "getblockhash", []any{height}, &out); err != nil {
		return "", err
	}
	return out, nil
}

// BlockHeader is the subset of `getblockheader <hash>` we care about.
type BlockHeader struct {
	Hash   string `json:"hash"`
	Height int64  `json:"height"`
	Time   int64  `json:"time"`
}

// GetBlockHeader returns a block's header. Preferred over GetBlock when
// only header fields are needed: getblock at verbosity 2 expands every
// transaction in the block, which for a full block is megabytes of JSON.
func (c *RPC) GetBlockHeader(ctx context.Context, hash string) (*BlockHeader, error) {
	var out BlockHeader
	if err := c.Call(ctx, "getblockheader", []any{hash, true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BlockVerbose2 is the subset of `getblock <hash> 2` we care about.
// Verbosity 2 expands each tx into its full object so we can read the
// coinbase output values without a second getrawtransaction call.
type BlockVerbose2 struct {
	Hash          string  `json:"hash"`
	Height        int64   `json:"height"`
	Confirmations int64   `json:"confirmations"`
	Time          int64   `json:"time"`
	Tx            []BlockTx `json:"tx"`
}

type BlockTx struct {
	Txid string   `json:"txid"`
	Vout []BlockVout `json:"vout"`
}

type BlockVout struct {
	Value        float64          `json:"value"`
	N            int              `json:"n"`
	ScriptPubKey BlockScriptPubKey `json:"scriptPubKey"`
}

type BlockScriptPubKey struct {
	Address string `json:"address"`
}

// GetBlock returns a verbose-level-2 block decode for the given hash.
func (c *RPC) GetBlock(ctx context.Context, hash string) (*BlockVerbose2, error) {
	var out BlockVerbose2
	if err := c.Call(ctx, "getblock", []any{hash, 2}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CoinbaseReward sums all outputs of the first transaction in the block
// (the coinbase). In solo mining this is the full subsidy + fees the
// solving worker receives.
func (b *BlockVerbose2) CoinbaseReward() float64 {
	if len(b.Tx) == 0 {
		return 0
	}
	var total float64
	for _, vout := range b.Tx[0].Vout {
		total += vout.Value
	}
	return total
}

// CoinbaseAddress returns the address of the first coinbase output.
// In ckpool-solo mode this is the miner's BTC payout address.
func (b *BlockVerbose2) CoinbaseAddress() string {
	if len(b.Tx) == 0 || len(b.Tx[0].Vout) == 0 {
		return ""
	}
	return b.Tx[0].Vout[0].ScriptPubKey.Address
}

// NetworkHashPS returns the network hashrate at the given block height.
// `blocks` is a window (default 120). Pass -1 to use the default.
func (c *RPC) GetNetworkHashPS(ctx context.Context, blocks, height int) (float64, error) {
	var out float64
	if err := c.Call(ctx, "getnetworkhashps", []any{blocks, height}, &out); err != nil {
		return 0, err
	}
	return out, nil
}

// TemplateTx is one transaction inside a getblocktemplate result.
type TemplateTx struct {
	Txid   string `json:"txid"`
	Fee    int64  `json:"fee"`    // satoshis
	Weight int64  `json:"weight"` // weight units
}

// BlockTemplate is the subset of getblocktemplate we care about: the
// coinbase value (subsidy + fees) and height of the next block.
type BlockTemplate struct {
	CoinbaseValue int64        `json:"coinbasevalue"` // satoshis
	Height        int64        `json:"height"`
	Transactions  []TemplateTx `json:"transactions"`
}

// GetBlockTemplate fetches the next block template with segwit rules.
// The call is relatively expensive; rate-limit callers to ~1/min.
func (c *RPC) GetBlockTemplate(ctx context.Context) (*BlockTemplate, error) {
	var out BlockTemplate
	params := []any{map[string]any{"rules": []string{"segwit"}}}
	if err := c.Call(ctx, "getblocktemplate", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- mempool / priority methods ----

// MempoolEntryFees mirrors the "fees" object inside getmempoolentry.
type MempoolEntryFees struct {
	Base       float64 `json:"base"`
	Modified   float64 `json:"modified"`
	Ancestor   float64 `json:"ancestor"`
	Descendant float64 `json:"descendant"`
}

// MempoolEntry is the result of getmempoolentry <txid>.
type MempoolEntry struct {
	Vsize int64            `json:"vsize"`
	Fees  MempoolEntryFees `json:"fees"`
}

// GetMempoolEntry returns the mempool entry for a transaction.
// Returns rpcError code -5 if the tx is not in the mempool.
func (c *RPC) GetMempoolEntry(ctx context.Context, txid string) (*MempoolEntry, error) {
	var out MempoolEntry
	if err := c.Call(ctx, "getmempoolentry", []any{txid}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PrioritiseTransaction adjusts a transaction's apparent fee for block
// template selection. feeDelta is in satoshis (positive = boost,
// negative = de-prioritise).
func (c *RPC) PrioritiseTransaction(ctx context.Context, txid string, feeDelta int64) error {
	var ok bool
	if err := c.Call(ctx, "prioritisetransaction", []any{txid, 0, feeDelta}, &ok); err != nil {
		return err
	}
	return nil
}

// RawMempoolEntry is the verbose form of a mempool entry from getrawmempool true.
type RawMempoolEntry struct {
	Vsize int64            `json:"vsize"`
	Fees  MempoolEntryFees `json:"fees"`
}

// GetRawMempoolVerbose returns all mempool transactions with their details.
func (c *RPC) GetRawMempoolVerbose(ctx context.Context) (map[string]RawMempoolEntry, error) {
	var out map[string]RawMempoolEntry
	if err := c.Call(ctx, "getrawmempool", []any{true}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type NetworkInfo struct {
	Connections    int `json:"connections"`
	ConnectionsIn  int `json:"connections_in"`
	ConnectionsOut int `json:"connections_out"`
}

func (c *RPC) GetNetworkInfo(ctx context.Context) (*NetworkInfo, error) {
	var out NetworkInfo
	if err := c.Call(ctx, "getnetworkinfo", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IsRPCError checks if err is an rpcError with a specific code.
func IsRPCError(err error, code int) bool {
	var rerr *rpcError
	if errors.As(err, &rerr) {
		return rerr.Code == code
	}
	return false
}

// RPCErrorMessage extracts the message from an rpcError, or the error string.
func RPCErrorMessage(err error) string {
	var rerr *rpcError
	if errors.As(err, &rerr) {
		return rerr.Message
	}
	return err.Error()
}
