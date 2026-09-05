// Package state merges data from CKPool (via Unix socket), Bitcoin Core
// (via JSON-RPC), and future sources (ZMQ, log tailer) into a single
// thread-safe snapshot the HTTP layer can serve. The snapshot is refreshed
// on a ticker; readers get a copy without blocking the refresh.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamadopool/kamado-api/internal/bitcoind"
	"github.com/kamadopool/kamado-api/internal/ckpool"
	"github.com/kamadopool/kamado-api/internal/logmon"
	"github.com/kamadopool/kamado-api/internal/store"
	"github.com/kamadopool/kamado-api/internal/zmqmon"
)

// HashratePoint is a single timestamped hashrate sample for the 24h chart.
type HashratePoint struct {
	T int64   `json:"t"` // unix seconds
	V float64 `json:"v"` // H/s
}

// BestSharePow wraps the PoW reproduction record ckpool logs for a new
// best share (patch 0008) with the time we ingested it. Persisted as
// JSON in the kv store so the header of the best share survives
// restarts.
type BestSharePow struct {
	logmon.PowData
	SeenAt time.Time `json:"seen_at"`
}

// StratumServer describes one entry in ckpool's serverurl[] array. ckpool
// tags every client with the index of the bind it arrived on, but the index
// alone doesn't say what that bind *is*, that depends on how the deployment
// rendered ckpool.conf. Declaring the array lets the dashboard report the
// actual transport (plaintext, TLS with which certificate) instead of
// hardcoding a bind order.
type StratumServer struct {
	// Kind is the stable machine-readable tag the UI switches on:
	// "plain", "tls-local" (package-managed self-signed certificate) or
	// "tls-public" (CA-issued certificate for a public domain).
	Kind string `json:"kind"`
	// Label is the human-readable description shown on hover.
	Label string `json:"label"`
}

// Snapshot is the merged view served to the UI. All fields are safe to
// JSON-serialize directly.
type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`

	// CKPool-derived fields
	Pool    *ckpool.PoolStats      `json:"pool"`
	Uptime  int64                  `json:"uptime_seconds"`
	Users   []ckpool.User          `json:"users"`
	Workers []ckpool.Worker        `json:"workers"`
	Clients []ckpool.StratumClient `json:"clients"`

	// Derived/enriched fields
	HashrateHs    float64 `json:"hashrate_hs_1m"` // from PoolStats.DSPS1
	HashrateHs5m  float64 `json:"hashrate_hs_5m"`
	HashrateHs1h  float64 `json:"hashrate_hs_1h"`
	HashrateHs24h float64 `json:"hashrate_hs_24h"`

	// Best share difficulty ever seen across all workers.
	BestDiff float64 `json:"best_diff"`
	// Block header hash of the best share ever found (hex string).
	BestShareHash string `json:"best_share_hash,omitempty"`
	// Network difficulty at the time the best share was found.
	BestShareNetDiff float64 `json:"best_share_net_diff,omitempty"`
	// Full PoW reproduction data (raw block header, coinbase, merkle
	// branches) for the best share seen since ckpool patch 0008 was
	// deployed. May describe a lower-diff share than BestDiff when the
	// all-time best predates the patch, the UI compares hashes and
	// shows a disclaimer in that case.
	BestSharePow *BestSharePow `json:"best_share_pow,omitempty"`
	// Last best-diff value acknowledged by the user via the UI.
	// The frontend shows a "new best" glow when best_diff > this.
	AckedBestDiff float64 `json:"acked_best_diff"`

	// Cumulative work done by the pool across its entire lifetime, in
	// diff-1-normalized shares (multiply by 2^32 for total hashes).
	// Survives ckpool restarts via kv-store persistence.
	CumulativeShares float64 `json:"cumulative_shares"`

	// Next-block reward (subsidy + fees) from bitcoind getblocktemplate,
	// in BTC. Refreshed at most once per minute.
	NextBlockRewardBTC float64 `json:"next_block_reward_btc"`

	// Predicted percent change at the next difficulty retarget, clamped
	// to Bitcoin's consensus range of [-75, +300]. Derived from the
	// observed block interval since the current epoch started.
	NextDifficultyPercent float64 `json:"next_difficulty_percent"`

	// Observed mean seconds per block so far this epoch, measured
	// between block timestamps. Zero before the first block of an epoch.
	// The UI uses it to project the retarget ETA at the pace the network
	// is actually keeping rather than a flat 600s.
	EpochAvgBlockSeconds float64 `json:"epoch_avg_block_seconds,omitempty"`

	// Bitcoin Core fields
	Chain             *bitcoind.BlockchainInfo `json:"chain"`
	NetworkHashrateHs float64                  `json:"network_hashrate_hs"`

	// Recent found blocks (in-memory history; persisted in Phase 2b.5).
	RecentBlocks []BlockRecord `json:"recent_blocks,omitempty"`

	// 24-hour hashrate history, sampled once per minute (max 1440 points).
	HashrateHistory []HashratePoint `json:"hashrate_history,omitempty"`

	// Optional override for explorer links in the UI. Empty means use
	// mempool.space defaults. Set via MEMPOOL_BASE_URL env, surfaced by
	// the StartOS config "Block Explorer" -> "Custom URL".
	MempoolBaseURL string `json:"mempool_base_url,omitempty"`

	// Describes ckpool's serverurl[] array: entry N tells the UI what a
	// client with `server == N` is actually connected over. Set via the
	// STRATUM_SERVERS env by whoever renders ckpool.conf (the StartOS
	// wrapper). Empty when undeclared, in which case the UI falls back to
	// its historical "index 1 means TLS" assumption.
	StratumServers []StratumServer `json:"stratum_servers,omitempty"`

	// Counts of share-submit attempts ("Possible/Submitting block solve"
	// log lines) and confirmed solves ("Solved and confirmed block").
	// A growing gap means bitcoind is rejecting our submissions or
	// dropping the RPC, surface it in the UI as an alert.
	BlockSubmitAttempts   int64 `json:"block_submit_attempts"`
	BlockSubmitsConfirmed int64 `json:"block_submits_confirmed"`

	// Health diagnostics for /healthz and the UI status badge.
	// LastZMQEventAge is the seconds-since the last bitcoind hashblock
	// frame arrived; -1 means no event seen since startup. ZMQEnabled
	// is whether the user configured an endpoint at all.
	ZMQEnabled      bool    `json:"zmq_enabled"`
	LastZMQEventAge float64 `json:"last_zmq_event_age,omitempty"` // seconds; >=0
	HasLastZMQEvent bool    `json:"has_last_zmq_event"`
	TipChangedAge   float64 `json:"tip_changed_age"` // seconds since tip height last changed

	// Bitcoin Core peer connections.
	PeerCount int `json:"peer_count"`
	PeersIn   int `json:"peers_in"`
	PeersOut  int `json:"peers_out"`

	// Share counters: raw counts (1 submission = 1 share regardless of diff).
	// Session = since ckpool started; AllTime = persisted across restarts.
	SessionAccepted int64 `json:"session_accepted"`
	SessionRejected int64 `json:"session_rejected"`
	AllTimeAccepted int64 `json:"alltime_accepted"`
	AllTimeRejected int64 `json:"alltime_rejected"`

	// Share statistics: rejection reasons and difficulty distribution.
	// Session resets on ckpool restart; AllTime persisted across restarts.
	RejectReasons    map[string]int64 `json:"reject_reasons_session,omitempty"`
	RejectReasonsAll map[string]int64 `json:"reject_reasons_alltime,omitempty"`
	// Difficulty distribution buckets: [<1M, 1M-100M, 100M-1G, 1G-100G, 100G-1T, >1T]
	DiffDist    [6]int64 `json:"diff_dist_session"`
	DiffDistAll [6]int64 `json:"diff_dist_alltime"`
	// Average share difficulty (arithmetic mean of all accepted shares).
	AvgDiffSession float64 `json:"avg_diff_session"`
	AvgDiffAlltime float64 `json:"avg_diff_alltime"`

	// Block update latency diagnostics (ZMQ trigger → mining.notify).
	LatencyCount    int64   `json:"latency_count"`
	LatencyAvgMs    int64   `json:"latency_avg_ms"`
	LatencyLastMs   int64   `json:"latency_last_ms"`
	StaleWorkHashes float64 `json:"stale_work_hashes"`

	// Health
	CKPoolOK  bool   `json:"ckpool_ok"`
	BitcoinOK bool   `json:"bitcoin_ok"`
	LastError string `json:"last_error,omitempty"`
}

const (
	// Bitcoin's difficulty retarget window and its target block spacing.
	retargetInterval   = 2016
	targetBlockSeconds = 600

	maxHistoryPoints = 1440 // 24h at 1 sample/min

	// kvCumulativeWork is versioned: "cumulative_shares" (v1) accidentally
	// summed pool.shares (raw per-share count), which is meaningless for
	// hashrate math. "cumulative_work" (v2) sums pool.accepted, the
	// diff-1-normalized work, so cumulative_work * 2^32 is real hashes.
	// The old key is orphaned in the kv table on upgrade; harmless.
	kvCumulativeWork = "cumulative_work"

	kvSubmitAttempts   = "block_submit_attempts"
	kvSubmitsConfirmed = "block_submits_confirmed"

	kvLatencyCount    = "latency_count"
	kvLatencySumMs    = "latency_sum_ms"
	kvLatencyLastMs   = "latency_last_ms"
	kvStaleWorkHashes = "stale_work_hashes"

	kvAckedBestDiff    = "acked_best_diff"
	kvBestDiff         = "best_diff"
	kvBestShareHash    = "best_share_hash"
	kvBestShareNetDiff = "best_share_net_diff"
	kvBestSharePow     = "best_share_pow"

	kvAllTimeAccepted = "alltime_accepted"
	kvAllTimeRejected = "alltime_rejected"

	kvRejectReasonsAll = "reject_reasons_alltime"
	kvDiffDistAll      = "diff_dist_alltime"

	// reconcileInterval is how often we sweep recent blocks looking
	// for missing hash/reward enrichment and reorg-orphaned hashes.
	// 60s is fast enough to recover from a transient bitcoind hiccup
	// within a minute, slow enough to never load the RPC.
	reconcileInterval = 60 * time.Second

	// reconcileLookback caps how far back the reconcile loop looks.
	// Blocks older than this with empty hash are abandoned; hashes
	// older than this are assumed deep enough to never reorg.
	reconcileLookback = 24 * time.Hour
)

// Aggregator refreshes a Snapshot on a ticker.
type Aggregator struct {
	CK       *ckpool.Client
	RPC      *bitcoind.RPC
	Store    *store.BlockStore // optional; nil disables persistence
	Interval time.Duration
	Log      *slog.Logger

	// Static config that the UI consumes via the snapshot. Empty
	// MempoolBaseURL leaves the UI on its mempool.space defaults.
	MempoolBaseURL string

	// Declared meaning of each ckpool serverurl[] index; see StratumServer.
	// Nil leaves the UI on its index-1-means-TLS fallback.
	StratumServers []StratumServer

	// LogFilePath is the ckpool log path, used for one-time backfill
	// of the best share hash when upgrading from a version that didn't
	// capture it. Set by main before calling Run.
	LogFilePath string

	// OnRefresh, if set, is called (non-blocking) after each snapshot
	// refresh. Used by the WebSocket hub to push updates to clients.
	OnRefresh func(Snapshot)

	// KillCKPool, if set, is called when bitcoind has been unreachable
	// for several consecutive polls and no block submission is pending.
	// Killing ckpool disconnects miners so they can failover to other
	// pools instead of mining stale work.
	KillCKPool func() error

	mu     sync.RWMutex
	snap   Snapshot
	blocks []BlockRecord

	// 24h hashrate history ring buffer, sampled once per minute.
	hrHistory      []HashratePoint
	lastHRSampleAt time.Time

	// Cumulative pool work in diff-1-normalized shares, surviving
	// ckpool restarts. Maintained by re-reading pool.Shares each
	// refresh and integrating only the positive delta, a decrease
	// means ckpool restarted and its counter reset to 0, so we reset
	// our delta baseline without losing the accumulated total.
	//
	// hasPoolSharesBaseline guards against double-counting on api
	// restart: cumulativeShares is loaded from disk, and the first
	// post-load observation of pool.Shares only establishes the
	// baseline, the current ckpool counter was already accounted for
	// before we crashed.
	cumulativeShares      float64
	lastPoolShares        float64
	hasPoolSharesBaseline bool
	lastCumulativeSave    time.Time

	// Next-block reward cache; refreshed on its own cadence so we don't
	// hammer bitcoind with getblocktemplate on every poll tick.
	nextBlockReward   float64
	lastTemplateFetch time.Time

	// Retarget epoch-start timestamp cache. Updated when we cross a
	// new retarget boundary (every 2016 blocks); within an epoch we
	// just re-use the cached value and re-extrapolate each refresh.
	retargetEpoch     int64
	retargetStartUnix int64

	// Chain-tip block timestamp, refetched only when the tip hash
	// changes. The retarget estimate measures between block timestamps,
	// so it needs the tip's own time rather than wall-clock time.
	tipTimeHash string
	tipTimeUnix int64

	// ckFailStreak counts consecutive refreshes where CKPool returned an
	// error. The first failure after a streak of successes is logged at
	// DEBUG (likely a transient warm-up or lock hiccup); repeated failures
	// escalate to WARN.
	ckFailStreak int

	// btcFailStreak counts consecutive refreshes where bitcoind was
	// unreachable. After a threshold, if no block submission is pending,
	// we kill the ckpool process so miners can failover to other pools.
	btcFailStreak int
	ckpoolKilled  bool // true after we sent SIGTERM, reset on bitcoind recovery

	// readyOnce + ready closes the Ready() channel exactly once after
	// the first refresh completes. main blocks briefly on this so the
	// HTTP server doesn't serve a never-refreshed (all-zeros) snapshot.
	readyOnce sync.Once
	ready     chan struct{}

	// Submit-attempt vs confirmed counters. Persisted in kv so the
	// running gap survives restarts. Both are monotonic.
	blockSubmitAttempts   int64
	blockSubmitsConfirmed int64
	lastSubmitCountSave   time.Time

	// ZMQ diagnostics: timestamp of the last hashblock frame relayed
	// from zmqmon. Used by /healthz to flag stale subscriptions.
	zmqEnabled       bool
	lastZMQEventTime time.Time

	// Tip-change tracking: the height and time when we last saw the
	// chain tip advance. Used to distinguish "ZMQ stale" from "no
	// blocks on the network".
	lastTipHeight    int64
	lastTipChangedAt time.Time

	// All-time share counters (raw, not diff-weighted). Accumulated
	// using the same delta-integration pattern as cumulative_shares.
	allTimeAccepted       int64
	allTimeRejected       int64
	lastPoolAcceptedRaw   int64
	lastPoolRejectedRaw   int64
	hasShareCountBaseline bool
	lastShareCountSave    time.Time

	// Block update latency tracking (from ckpool patch 0005).
	// Persisted to kv store so stats accumulate across restarts.
	latencyCount    int64   // number of observations
	latencySumMs    int64   // sum of all latencies in ms
	latencyLastMs   int64   // most recent observation
	staleWorkHashes float64 // cumulative wasted hashes = sum(latency_s * hashrate_at_event)

	// notifierDone is signalled by IngestLatencyEvents when a latency
	// event arrives, meaning ckpool's notifier has finished its
	// getblocktemplate + mining.notify cycle. The Run loop uses this to
	// defer dashboard RPC calls until after the notifier is done,
	// avoiding resource contention on bitcoind.
	notifierDone chan struct{}

	// High-water mark for the best share difficulty ever seen.
	// Persisted to kv so it survives restarts and transient ckpool gaps.
	bestDiff float64
	// Block header hash of the best share ever found. Captured from the
	// ckpool log "Accepted client ... : <hash>" line for the share that
	// set the bestDiff record. Persisted alongside bestDiff.
	bestShareHash    string
	bestShareNetDiff float64 // network difficulty when best share was found

	// PoW reproduction data for the best share seen since ckpool patch
	// 0008 went live. Tracked as its own high-water mark (by SDiff)
	// because the all-time best share may predate the patch and have no
	// reconstructible header. Never mutated after assignment, so the
	// pointer is safe to hand out in snapshots.
	bestPow *BestSharePow
	// Set while a log rescan is running. The scan walks every line of a
	// log that can hold millions, so overlapping runs would multiply the
	// I/O for no benefit and give a caller a cheap way to load the box.
	rebuilding atomic.Bool

	// PoW data lines arrive on the share channel just before their
	// matching "Accepted client" line. Park them here keyed by header
	// hash until the accepted event confirms the share, so data from a
	// share that ends up rejected (e.g. below the client's vardiff
	// right after a ckpool restart) is never persisted.
	pendingPow map[string]*logmon.PowData

	// Last best-diff value acknowledged by the user in the UI.
	ackedBestDiff float64

	// Share statistics from log parsing.
	sessionRejectReasons map[string]int64
	alltimeRejectReasons map[string]int64
	sessionDiffDist      [6]int64
	alltimeDiffDist      [6]int64
	sessionDiffCount     int64   // total accepted shares (session)
	alltimeDiffCount     int64   // total accepted shares (alltime)
	sessionDiffSum       float64 // sum of share difficulties (session)
	alltimeDiffSum       float64 // sum of share difficulties (alltime)
	lastShareStatsSave   time.Time

	// loadOnce guards LoadPersisted so main can complete the restore
	// before starting the ingest goroutines, while Run stays safe to
	// call on its own.
	loadOnce sync.Once
	// statsLoaded reports that the alltime share statistics have been
	// read back from the store. Persisting before that would write the
	// zero-valued in-memory counters over the accumulated history, the
	// first share event triggers a save, so this is the difference
	// between a restart and permanent data loss.
	statsLoaded bool
}

func New(ck *ckpool.Client, rpc *bitcoind.RPC, interval time.Duration, log *slog.Logger) *Aggregator {
	return &Aggregator{
		CK:                   ck,
		RPC:                  rpc,
		Interval:             interval,
		Log:                  log,
		sessionRejectReasons: make(map[string]int64),
		alltimeRejectReasons: make(map[string]int64),
		pendingPow:           make(map[string]*logmon.PowData),
		ready:                make(chan struct{}),
		notifierDone:         make(chan struct{}, 1),
	}
}

// LoadPersisted restores block history and counters from the store.
// Run calls it, but callers should invoke it directly before starting
// any ingest goroutine: those goroutines mutate the same counters and
// persist them on their first event, so a restore still in flight would
// be overwritten by, and then read back as, near-empty state. Safe to
// call repeatedly; only the first call does the work.
func (a *Aggregator) LoadPersisted() {
	a.loadOnce.Do(func() {
		a.loadPersistedBlocks()
		a.loadPersistedState()
	})
}

// Ready returns a channel that's closed once the aggregator has
// completed its first refresh, used by main to delay HTTP serving
// until /api/snapshot reflects real state instead of zeros.
func (a *Aggregator) Ready() <-chan struct{} {
	return a.ready
}

func (a *Aggregator) markReady() {
	a.readyOnce.Do(func() { close(a.ready) })
}

// Run blocks until ctx is cancelled, refreshing the snapshot every Interval.
// It runs one immediate refresh at startup so readers don't see an empty
// snapshot after ctx launches the goroutine. Persisted block history is
// loaded from the store before the first refresh. If tipEvents is non-nil,
// each received tip triggers an immediate refresh outside the poll cadence.
func (a *Aggregator) Run(ctx context.Context, tipEvents <-chan zmqmon.TipEvent) {
	a.mu.Lock()
	a.zmqEnabled = tipEvents != nil
	a.mu.Unlock()
	a.LoadPersisted()
	a.refresh(ctx)
	t := time.NewTicker(a.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.refresh(ctx)
		case ev, ok := <-tipEvents:
			if !ok {
				tipEvents = nil
				continue
			}
			a.mu.Lock()
			a.lastZMQEventTime = ev.SeenAt
			a.mu.Unlock()
			// Wait for ckpool's notifier to finish (getblocktemplate +
			// mining.notify) before firing our dashboard RPCs, so we
			// don't compete for bitcoind RPC resources during the
			// latency-critical window. The latency log line signals
			// completion. Timeout after 5s in case the log line is
			// missed or ckpool is slow.
			a.Log.Debug("zmq tip, waiting for notifier", "hash", ev.Hash)
			select {
			case <-a.notifierDone:
				a.Log.Debug("notifier done, refreshing dashboard")
			case <-time.After(5 * time.Second):
				a.Log.Debug("notifier wait timed out, refreshing anyway")
			case <-ctx.Done():
				return
			}
			a.refresh(ctx)
		}
	}
}

// Snapshot returns a copy of the current snapshot.
func (a *Aggregator) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snap
}

func (a *Aggregator) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	next := Snapshot{
		GeneratedAt:    time.Now(),
		MempoolBaseURL: a.MempoolBaseURL,
		StratumServers: a.StratumServers,
	}

	// --- ckpool: poolstats, users, workers, clients, uptime ---
	if ps, err := a.CK.PoolStats(ctx); err == nil {
		next.Pool = ps
		next.HashrateHs = ckpool.DSPSToHashrate(ps.DSPS1)
		next.HashrateHs5m = ckpool.DSPSToHashrate(ps.DSPS5)
		next.HashrateHs1h = ckpool.DSPSToHashrate(ps.DSPS60)
		next.HashrateHs24h = ckpool.DSPSToHashrate(ps.DSPS1440)
		next.CKPoolOK = true
		a.ckFailStreak = 0
	} else {
		a.ckFailStreak++
		// First failure or two: likely transient (stratifier warm-up or
		// a lock hiccup during a reconnect). Only escalate to WARN after
		// three consecutive failures.
		if a.ckFailStreak >= 3 {
			a.Log.Warn("ckpool poolstats failed", "err", err, "streak", a.ckFailStreak)
		} else {
			a.Log.Debug("ckpool poolstats transient error", "err", err, "streak", a.ckFailStreak)
		}
		next.LastError = err.Error()
	}
	if u, err := a.CK.Uptime(ctx); err == nil {
		next.Uptime = u
	}
	if us, err := a.CK.Users(ctx); err == nil {
		next.Users = us
	}
	if ws, err := a.CK.Workers(ctx); err == nil {
		next.Workers = ws
	}
	if cs, err := a.CK.Clients(ctx); err == nil {
		next.Clients = cs
	}

	// --- bitcoind: chain + network hashrate ---
	if bi, err := a.RPC.GetBlockchainInfo(ctx); err == nil {
		next.Chain = bi
		next.BitcoinOK = true
		if a.btcFailStreak > 0 {
			a.Log.Info("bitcoind recovered", "after_failures", a.btcFailStreak)
		}
		a.btcFailStreak = 0
		a.ckpoolKilled = false
		// Track when the tip height last changed.
		if bi.Blocks != a.lastTipHeight {
			a.lastTipHeight = bi.Blocks
			a.lastTipChangedAt = time.Now()
		}
		if !a.lastTipChangedAt.IsZero() {
			next.TipChangedAge = time.Since(a.lastTipChangedAt).Seconds()
		}
		if nh, err := a.RPC.GetNetworkHashPS(ctx, -1, int(bi.Blocks)); err == nil {
			next.NetworkHashrateHs = nh
		}
	} else {
		a.btcFailStreak++
		a.Log.Warn("bitcoind getblockchaininfo failed", "err", err, "streak", a.btcFailStreak)
		if next.LastError == "" {
			next.LastError = err.Error()
		}
	}

	// --- bitcoind: peer connections ---
	if ni, err := a.RPC.GetNetworkInfo(ctx); err == nil {
		next.PeerCount = ni.Connections
		next.PeersIn = ni.ConnectionsIn
		next.PeersOut = ni.ConnectionsOut
	}

	// --- predicted difficulty adjustment ---
	// Project the retarget from the pace the epoch has kept so far,
	// measured strictly between block timestamps: from the epoch's first
	// block to the tip is exactly `inEpoch` inter-block intervals.
	//
	// Measuring to wall-clock time instead adds the partial, in-progress
	// interval to the elapsed span without adding an interval to divide
	// it by. That skews the estimate toward "difficulty will fall" by
	// however long it has been since the last block, which averages a
	// full 600s and, a handful of blocks into an epoch, is most of the
	// sample. It washes out only once enough blocks accumulate to dwarf
	// it, which is why the estimate used to settle down around mid-epoch.
	if next.Chain != nil && next.Chain.Blocks > 0 {
		height := next.Chain.Blocks
		epoch := height / retargetInterval
		inEpoch := height % retargetInterval
		if inEpoch > 0 {
			if a.retargetEpoch != epoch || a.retargetStartUnix == 0 {
				startHeight := epoch * retargetInterval
				lookupCtx, cancelLookup := context.WithTimeout(ctx, 3*time.Second)
				if hash, err := a.RPC.GetBlockHash(lookupCtx, startHeight); err == nil {
					if hdr, err := a.RPC.GetBlockHeader(lookupCtx, hash); err == nil {
						a.retargetEpoch = epoch
						a.retargetStartUnix = hdr.Time
					}
				}
				cancelLookup()
			}
			// Tip timestamp, refetched only when the tip actually moves.
			if next.Chain.BestBlockHash != "" && a.tipTimeHash != next.Chain.BestBlockHash {
				lookupCtx, cancelLookup := context.WithTimeout(ctx, 3*time.Second)
				if hdr, err := a.RPC.GetBlockHeader(lookupCtx, next.Chain.BestBlockHash); err == nil {
					a.tipTimeHash = next.Chain.BestBlockHash
					a.tipTimeUnix = hdr.Time
				}
				cancelLookup()
			}
			if a.retargetStartUnix > 0 && a.tipTimeUnix > 0 {
				elapsed := float64(a.tipTimeUnix - a.retargetStartUnix)
				next.NextDifficultyPercent = estimateRetargetPercent(inEpoch, elapsed)
				if elapsed > 0 {
					next.EpochAvgBlockSeconds = elapsed / float64(inEpoch)
				}
			}
		}
	}

	// Compute pool-wide best-ever share diff from workers, then clamp
	// to the persisted high-water mark so a ckpool restart or transient
	// socket timeout can never lower the value.
	for _, w := range next.Workers {
		d := w.BestEver
		if d == 0 {
			d = w.BestDiff
		}
		if d > next.BestDiff {
			next.BestDiff = d
		}
	}

	a.mu.Lock()

	// High-water mark: only increases, never decreases.
	if next.BestDiff > a.bestDiff {
		a.bestDiff = next.BestDiff
		if a.Store != nil {
			val := strconv.FormatFloat(a.bestDiff, 'f', -1, 64)
			_ = a.Store.SetKV(kvBestDiff, val)
		}
	}
	next.BestDiff = a.bestDiff
	next.BestShareHash = a.bestShareHash
	next.BestShareNetDiff = a.bestShareNetDiff
	next.BestSharePow = a.bestPow
	next.AckedBestDiff = a.ackedBestDiff
	now := time.Now()

	// --- cumulative work tracking ---
	// Integrate only positive deltas on pool.Accepted, which is ckpool's
	// accounted_diff_shares, the sum of each accepted share's
	// difficulty, in diff-1-normalized units. pool.Shares is the raw
	// per-share count and is useless for hashrate math. A decrease
	// means ckpool restarted (or its state reset); we reset the
	// baseline without touching the accumulated total. The first
	// observation after load only establishes the baseline, the
	// current counter was already integrated before we shut down.
	if next.Pool != nil {
		cur := float64(next.Pool.Accepted)
		if a.hasPoolSharesBaseline && cur >= a.lastPoolShares {
			a.cumulativeShares += cur - a.lastPoolShares
		}
		a.lastPoolShares = cur
		a.hasPoolSharesBaseline = true
	}
	next.CumulativeShares = a.cumulativeShares

	// Persist cumulative_shares at most once per minute.
	if a.Store != nil && now.Sub(a.lastCumulativeSave) >= time.Minute {
		val := strconv.FormatFloat(a.cumulativeShares, 'f', -1, 64)
		if err := a.Store.SetKV(kvCumulativeWork, val); err != nil {
			a.Log.Warn("cumulative_shares persist failed", "err", err)
		}
		a.lastCumulativeSave = now
	}

	// --- raw share count tracking (1 share = 1 submission) ---
	// pool.Shares = raw accepted count, pool.RejectCount = raw rejected count.
	// Delta-integrate the same way as cumulative work.
	if next.Pool != nil {
		curAcc := next.Pool.Shares
		curRej := next.Pool.RejectCount
		if a.hasShareCountBaseline {
			if curAcc >= a.lastPoolAcceptedRaw {
				a.allTimeAccepted += curAcc - a.lastPoolAcceptedRaw
			} else {
				// ckpool restarted, reset session share stats.
				a.resetSessionShareStats()
			}
			if curRej >= a.lastPoolRejectedRaw {
				a.allTimeRejected += curRej - a.lastPoolRejectedRaw
			}
		}
		a.lastPoolAcceptedRaw = curAcc
		a.lastPoolRejectedRaw = curRej
		a.hasShareCountBaseline = true
		next.SessionAccepted = curAcc
		next.SessionRejected = curRej
	}
	next.AllTimeAccepted = a.allTimeAccepted
	next.AllTimeRejected = a.allTimeRejected

	// Persist share counts at most once per minute.
	if a.Store != nil && now.Sub(a.lastShareCountSave) >= time.Minute {
		_ = a.Store.SetKV(kvAllTimeAccepted, strconv.FormatInt(a.allTimeAccepted, 10))
		_ = a.Store.SetKV(kvAllTimeRejected, strconv.FormatInt(a.allTimeRejected, 10))
		a.lastShareCountSave = now
	}

	// --- hashrate history sample (once per minute) ---
	if now.Sub(a.lastHRSampleAt) >= time.Minute {
		p := HashratePoint{T: now.Unix(), V: next.HashrateHs}
		a.hrHistory = append(a.hrHistory, p)
		if len(a.hrHistory) > maxHistoryPoints {
			a.hrHistory = a.hrHistory[len(a.hrHistory)-maxHistoryPoints:]
		}
		a.lastHRSampleAt = now
		if a.Store != nil {
			if err := a.Store.InsertHashrateSample(p.T, p.V); err != nil {
				a.Log.Warn("hashrate persist failed", "err", err)
			}
			// Keep the persisted window bounded to 24h + a small slack.
			cutoff := now.Add(-25 * time.Hour).Unix()
			_ = a.Store.PruneHashrateBefore(cutoff)
		}
	}
	if len(a.hrHistory) > 0 {
		next.HashrateHistory = make([]HashratePoint, len(a.hrHistory))
		copy(next.HashrateHistory, a.hrHistory)
	}

	// --- next-block reward (refreshed every ~15s so users see the
	// fee component tick up as mempool grows; bitcoind caches the
	// template internally, so the RPC is cheap even at this cadence).
	next.NextBlockRewardBTC = a.nextBlockReward
	needTemplate := now.Sub(a.lastTemplateFetch) >= 15*time.Second
	a.mu.Unlock()

	if needTemplate && a.RPC != nil && next.BitcoinOK {
		tplCtx, cancelTpl := context.WithTimeout(ctx, 5*time.Second)
		tpl, err := a.RPC.GetBlockTemplate(tplCtx)
		cancelTpl()
		if err == nil && tpl != nil {
			reward := float64(tpl.CoinbaseValue) / 1e8
			a.mu.Lock()
			a.nextBlockReward = reward
			a.lastTemplateFetch = now
			next.NextBlockRewardBTC = reward
			a.mu.Unlock()
		} else if err != nil {
			a.Log.Debug("getblocktemplate failed", "err", err)
		}
	}

	a.mu.Lock()
	// Attach current block history so /api/snapshot and WebSocket
	// pushes carry the same view.
	if len(a.blocks) > 0 {
		next.RecentBlocks = make([]BlockRecord, len(a.blocks))
		copy(next.RecentBlocks, a.blocks)
	}
	next.BlockSubmitAttempts = a.blockSubmitAttempts
	next.BlockSubmitsConfirmed = a.blockSubmitsConfirmed
	next.ZMQEnabled = a.zmqEnabled
	if !a.lastZMQEventTime.IsZero() {
		next.LastZMQEventAge = time.Since(a.lastZMQEventTime).Seconds()
		next.HasLastZMQEvent = true
	}
	next.LatencyCount = a.latencyCount
	if a.latencyCount > 0 {
		next.LatencyAvgMs = a.latencySumMs / a.latencyCount
	}
	next.LatencyLastMs = a.latencyLastMs
	next.StaleWorkHashes = a.staleWorkHashes

	// Share statistics (from log parsing).
	if len(a.sessionRejectReasons) > 0 {
		m := make(map[string]int64, len(a.sessionRejectReasons))
		for k, v := range a.sessionRejectReasons {
			m[k] = v
		}
		next.RejectReasons = m
	}
	if len(a.alltimeRejectReasons) > 0 {
		m := make(map[string]int64, len(a.alltimeRejectReasons))
		for k, v := range a.alltimeRejectReasons {
			m[k] = v
		}
		next.RejectReasonsAll = m
	}
	next.DiffDist = a.sessionDiffDist
	next.DiffDistAll = a.alltimeDiffDist
	if a.sessionDiffCount > 0 {
		next.AvgDiffSession = a.sessionDiffSum / float64(a.sessionDiffCount)
	}
	if a.alltimeDiffCount > 0 {
		next.AvgDiffAlltime = a.alltimeDiffSum / float64(a.alltimeDiffCount)
	}

	a.snap = next
	cb := a.OnRefresh
	pushed := next
	a.mu.Unlock()

	if cb != nil {
		cb(pushed)
	}
	a.markReady()

	// Kill ckpool when bitcoind is unreachable so miners can failover.
	// Conditions: 3+ consecutive failures, no pending block submission,
	// haven't already killed it, and a kill callback is configured.
	const btcFailThreshold = 3
	submitGap := next.BlockSubmitAttempts - next.BlockSubmitsConfirmed
	if a.btcFailStreak >= btcFailThreshold && !a.ckpoolKilled && a.KillCKPool != nil {
		if submitGap > 0 {
			a.Log.Warn("bitcoind down but block submission pending, keeping ckpool alive",
				"submit_gap", submitGap, "streak", a.btcFailStreak)
		} else {
			a.Log.Warn("bitcoind unreachable, killing ckpool so miners can failover",
				"streak", a.btcFailStreak)
			if err := a.KillCKPool(); err != nil {
				a.Log.Error("failed to kill ckpool", "err", err)
			} else {
				a.ckpoolKilled = true
			}
		}
	}
}

// AckBestDiff records the current best_diff as acknowledged so the UI
// stops showing the "new best share" glow until a higher one appears.
func (a *Aggregator) AckBestDiff() {
	a.mu.Lock()
	a.ackedBestDiff = a.snap.BestDiff
	a.mu.Unlock()
	if a.Store != nil {
		val := strconv.FormatFloat(a.snap.BestDiff, 'f', -1, 64)
		if err := a.Store.SetKV(kvAckedBestDiff, val); err != nil {
			a.Log.Warn("acked_best_diff persist failed", "err", err)
		}
	}
}

// ResetAckedBestDiff clears the acknowledged best diff so the "new best
// share" glow reappears. Used by the debug/test UI button.
func (a *Aggregator) ResetAckedBestDiff() {
	a.mu.Lock()
	a.ackedBestDiff = 0
	a.mu.Unlock()
	if a.Store != nil {
		_ = a.Store.SetKV(kvAckedBestDiff, "0")
	}
}

// estimateRetargetPercent projects the next difficulty adjustment from
// the pace of the current epoch: inEpoch inter-block intervals took
// elapsed seconds, both measured between block timestamps. Returns the
// percentage change, clamped to Bitcoin's consensus range of
// [-75, +300].
func estimateRetargetPercent(inEpoch int64, elapsed float64) float64 {
	if inEpoch <= 0 || elapsed <= 0 {
		return 0
	}
	// Consensus budgets retargetInterval*600 seconds for an epoch but
	// spans them across retargetInterval-1 inter-block intervals, the
	// long-standing off-by-one, so on-target pace is fractionally above
	// 600s per block, and a perfectly on-time epoch retargets by ~0%
	// rather than a hair negative.
	targetPerInterval := float64(retargetInterval*targetBlockSeconds) / float64(retargetInterval-1)
	factor := (float64(inEpoch) * targetPerInterval) / elapsed
	if factor > 4 {
		factor = 4
	}
	if factor < 0.25 {
		factor = 0.25
	}
	return (factor - 1) * 100
}

// diffBucket returns the index [0..5] for a share difficulty:
//
//	0: < 1M    1: 1M–100M   2: 100M–1G
//	3: 1G–100G 4: 100G–1T   5: ≥ 1T
func diffBucket(d float64) int {
	switch {
	case d < 1e6:
		return 0
	case d < 1e8:
		return 1
	case d < 1e9:
		return 2
	case d < 1e11:
		return 3
	case d < 1e12:
		return 4
	default:
		return 5
	}
}

// DiffBucketLabels are the human-readable labels for each difficulty bucket.
var DiffBucketLabels = [6]string{"< 1M", "1M – 100M", "100M – 1G", "1G – 100G", "100G – 1T", "≥ 1T"}

// IngestShareEvents reads individual share events from the log tailer
// and maintains rejection-reason counts and difficulty-distribution
// histograms. Session counters reset when ckpool restarts (detected by
// the aggregator's existing pool.Shares decrease logic). Alltime
// counters are persisted to the kv store.
func (a *Aggregator) IngestShareEvents(ctx context.Context, events <-chan logmon.ShareEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			a.mu.Lock()
			if ev.Pow != nil {
				// PoW reproduction line (patch 0008). Park it until the
				// matching "Accepted client" event arrives; a bounded
				// reset guards against unmatched entries piling up.
				if len(a.pendingPow) >= 16 {
					a.pendingPow = make(map[string]*logmon.PowData)
				}
				a.pendingPow[ev.Pow.Hash] = ev.Pow
				a.mu.Unlock()
				continue
			}
			if ev.Rejected {
				reason := ev.Reason
				if reason == "" {
					reason = "Unknown"
				}
				a.sessionRejectReasons[reason]++
				a.alltimeRejectReasons[reason]++
			} else {
				bucket := diffBucket(ev.Diff)
				a.sessionDiffDist[bucket]++
				a.alltimeDiffDist[bucket]++
				a.sessionDiffCount++
				a.alltimeDiffCount++
				a.sessionDiffSum += ev.Diff
				a.alltimeDiffSum += ev.Diff
				// Promote parked PoW data once its share is confirmed
				// accepted and beats the best PoW record we hold. This
				// high-water mark is separate from bestDiff: the
				// all-time best share may predate ckpool patch 0008,
				// in which case this tracks the best share since the
				// patch went live and the UI discloses the difference.
				if pow, ok := a.pendingPow[ev.Hash]; ok {
					delete(a.pendingPow, ev.Hash)
					if a.bestPow == nil || pow.SDiff > a.bestPow.SDiff {
						a.bestPow = &BestSharePow{PowData: *pow, SeenAt: time.Now()}
						if a.Store != nil {
							if data, err := json.Marshal(a.bestPow); err == nil {
								_ = a.Store.SetKV(kvBestSharePow, string(data))
							}
						}
						a.Log.Info("best share PoW data captured",
							"sdiff", pow.SDiff, "height", pow.Height, "hash", pow.Hash)
					}
				}
				// Track hash of the best share from log parsing.
				if ev.Hash != "" && ev.Diff > a.bestDiff {
					a.bestDiff = ev.Diff
					a.bestShareHash = ev.Hash
					// Capture the current network difficulty at time of finding.
					if a.snap.Chain != nil && a.snap.Chain.Difficulty > 0 {
						a.bestShareNetDiff = a.snap.Chain.Difficulty
					}
					if a.Store != nil {
						_ = a.Store.SetKV(kvBestDiff, strconv.FormatFloat(ev.Diff, 'f', -1, 64))
						_ = a.Store.SetKV(kvBestShareHash, ev.Hash)
						if a.bestShareNetDiff > 0 {
							_ = a.Store.SetKV(kvBestShareNetDiff, strconv.FormatFloat(a.bestShareNetDiff, 'f', -1, 64))
						}
					}
				}
			}
			save := a.Store != nil && time.Since(a.lastShareStatsSave) >= time.Minute
			if save {
				a.lastShareStatsSave = time.Now()
			}
			a.mu.Unlock()
			if save {
				a.persistShareStats()
			}
		}
	}
}

// RebuildShareStatsFromLog recounts the all-time share statistics by
// scanning the whole ckpool log, and adopts the result when it accounts
// for more shares than the counters currently hold. Recovery for stored
// stats that were lost or truncated: the log is the only other record of
// per-share difficulty, and ckpool keeps its full history there.
//
// Deliberately never automatic, the scan walks every line of a log that
// can hold millions, and it was a long startup scan that made the race
// this repairs so easy to hit in the first place.
func (a *Aggregator) RebuildShareStatsFromLog() (adopted bool, scanned int64, err error) {
	if a.LogFilePath == "" {
		return false, 0, errors.New("no ckpool log path configured")
	}
	if !a.rebuilding.CompareAndSwap(false, true) {
		return false, 0, errors.New("a share statistics rebuild is already running")
	}
	defer a.rebuilding.Store(false)
	st, err := logmon.ScanShareStats(a.LogFilePath)
	if err != nil {
		return false, 0, err
	}

	var dist [6]int64
	for _, d := range st.Diffs {
		dist[diffBucket(d)]++
	}

	a.mu.Lock()
	current := a.alltimeDiffCount
	if st.Accepted <= current {
		a.mu.Unlock()
		a.Log.Info("share stats rebuild: log has no more shares than stored, keeping stored",
			"log_shares", st.Accepted, "stored_shares", current)
		return false, st.Accepted, nil
	}
	a.alltimeDiffDist = dist
	a.alltimeDiffCount = st.Accepted
	a.alltimeDiffSum = st.DiffSum
	a.alltimeRejectReasons = st.RejectReasons
	a.statsLoaded = true
	a.mu.Unlock()

	a.persistShareStats()
	a.Log.Warn("share stats rebuilt from ckpool log",
		"shares", st.Accepted, "was", current)
	return true, st.Accepted, nil
}

// ResetSessionShareStats clears session-level share statistics. Called
// when a ckpool restart is detected (pool.Shares decreases).
func (a *Aggregator) resetSessionShareStats() {
	a.sessionRejectReasons = make(map[string]int64)
	a.sessionDiffDist = [6]int64{}
	a.sessionDiffCount = 0
	a.sessionDiffSum = 0
}

func (a *Aggregator) persistShareStats() {
	if a.Store == nil {
		return
	}
	a.mu.RLock()
	loaded := a.statsLoaded
	a.mu.RUnlock()
	if !loaded {
		// Belt and braces alongside the load-before-ingest ordering in
		// main: writing here would replace the accumulated history with
		// whatever few shares have arrived since startup.
		return
	}
	a.mu.Lock()
	reasonsCopy := make(map[string]int64, len(a.alltimeRejectReasons))
	for k, v := range a.alltimeRejectReasons {
		reasonsCopy[k] = v
	}
	distCopy := a.alltimeDiffDist
	countCopy := a.alltimeDiffCount
	sumCopy := a.alltimeDiffSum
	a.mu.Unlock()

	if data, err := json.Marshal(reasonsCopy); err == nil {
		_ = a.Store.SetKV(kvRejectReasonsAll, string(data))
	}
	type distPersist struct {
		Buckets [6]int64 `json:"b"`
		Count   int64    `json:"n"`
		Sum     float64  `json:"s"`
	}
	if data, err := json.Marshal(distPersist{Buckets: distCopy, Count: countCopy, Sum: sumCopy}); err == nil {
		_ = a.Store.SetKV(kvDiffDistAll, string(data))
	}
}

// loadPersistedState restores cumulative work and hashrate history from
// the store so they survive process restarts. Safe to call with a nil
// Store, becomes a no-op.
func (a *Aggregator) loadPersistedState() {
	if a.Store == nil {
		return
	}

	if v, err := a.Store.GetKV(kvCumulativeWork); err != nil {
		a.Log.Warn("cumulative_shares load failed", "err", err)
	} else if v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil {
			a.mu.Lock()
			a.cumulativeShares = f
			a.mu.Unlock()
			a.Log.Info("cumulative_shares loaded", "value", f)
		}
	}

	if v, err := a.Store.GetKV(kvSubmitAttempts); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			a.mu.Lock()
			a.blockSubmitAttempts = n
			a.mu.Unlock()
		}
	}
	if v, err := a.Store.GetKV(kvSubmitsConfirmed); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			a.mu.Lock()
			a.blockSubmitsConfirmed = n
			a.mu.Unlock()
		}
	}

	// Restore all-time share counts.
	if v, err := a.Store.GetKV(kvAllTimeAccepted); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			a.mu.Lock()
			a.allTimeAccepted = n
			a.mu.Unlock()
		}
	}
	if v, err := a.Store.GetKV(kvAllTimeRejected); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			a.mu.Lock()
			a.allTimeRejected = n
			a.mu.Unlock()
		}
	}

	// Restore best diff high-water mark and its block header hash.
	if v, err := a.Store.GetKV(kvBestDiff); err == nil && v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil {
			a.mu.Lock()
			a.bestDiff = f
			a.mu.Unlock()
		}
	}
	if v, err := a.Store.GetKV(kvBestShareHash); err == nil && v != "" {
		a.mu.Lock()
		a.bestShareHash = v
		a.mu.Unlock()
	}
	if v, err := a.Store.GetKV(kvBestShareNetDiff); err == nil && v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil {
			a.mu.Lock()
			a.bestShareNetDiff = f
			a.mu.Unlock()
		}
	}
	if v, err := a.Store.GetKV(kvBestSharePow); err == nil && v != "" {
		var pow BestSharePow
		if uerr := json.Unmarshal([]byte(v), &pow); uerr == nil && pow.Header != "" {
			a.mu.Lock()
			a.bestPow = &pow
			a.mu.Unlock()
		} else if uerr != nil {
			a.Log.Warn("best_share_pow load failed", "err", uerr)
		}
	}

	// One-time backfill: if we have no hash (pre-upgrade), scan the
	// ckpool log for the highest-difficulty accepted share and use its hash.
	a.mu.RLock()
	needsBackfill := a.bestShareHash == "" && a.LogFilePath != ""
	a.mu.RUnlock()
	if needsBackfill {
		if logDiff, h := logmon.FindBestShareHash(a.LogFilePath); h != "" {
			a.mu.Lock()
			a.bestShareHash = h
			// The log may contain the true best, adopt it if higher.
			if logDiff > a.bestDiff {
				a.bestDiff = logDiff
				if a.Store != nil {
					_ = a.Store.SetKV(kvBestDiff, strconv.FormatFloat(logDiff, 'f', -1, 64))
				}
			}
			a.mu.Unlock()
			if a.Store != nil {
				_ = a.Store.SetKV(kvBestShareHash, h)
			}
			a.Log.Info("backfilled best share hash from log", "diff", logDiff, "hash", h)
		} else {
			a.Log.Info("best share hash backfill: no accepted shares found in log")
		}
	}

	// One-time backfill: if we hold no PoW record but the log already
	// contains confirmed "Best share PoW data" lines (fresh database, or
	// events dropped under load), adopt the best of them. Without this
	// the PoW inspector would wait for a share beating ckpool's
	// in-process best, which after a long ckpool uptime can take
	// arbitrarily long.
	a.mu.RLock()
	needsPowBackfill := a.bestPow == nil && a.LogFilePath != ""
	a.mu.RUnlock()
	if needsPowBackfill {
		pow, at, seen, unparseable, err := logmon.FindBestPowData(a.LogFilePath)
		switch {
		case err != nil:
			a.Log.Warn("best share PoW backfill: log scan failed", "err", err)
		case pow == nil:
			// Deliberately loud: "no record" is indistinguishable from a
			// broken pipeline without the counts.
			a.Log.Info("best share PoW backfill: no confirmed record adopted",
				"pow_lines_seen", seen, "unparseable", unparseable)
		default:
			if at.IsZero() {
				at = time.Now()
			}
			best := &BestSharePow{PowData: *pow, SeenAt: at}
			a.mu.Lock()
			a.bestPow = best
			a.mu.Unlock()
			if data, jerr := json.Marshal(best); jerr == nil {
				_ = a.Store.SetKV(kvBestSharePow, string(data))
			}
			a.Log.Info("backfilled best share PoW data from log",
				"sdiff", pow.SDiff, "hash", pow.Hash, "pow_lines_seen", seen)
		}
	}

	// Restore acknowledged best diff.
	if v, err := a.Store.GetKV(kvAckedBestDiff); err == nil && v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil {
			a.mu.Lock()
			a.ackedBestDiff = f
			a.mu.Unlock()
		}
	}

	// Restore latency stats.
	a.mu.Lock()
	if v, err := a.Store.GetKV(kvLatencyCount); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			a.latencyCount = n
		}
	}
	if v, err := a.Store.GetKV(kvLatencySumMs); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			a.latencySumMs = n
		}
	}
	if v, err := a.Store.GetKV(kvLatencyLastMs); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			a.latencyLastMs = n
		}
	}
	if v, err := a.Store.GetKV(kvStaleWorkHashes); err == nil && v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil {
			a.staleWorkHashes = f
		}
	}
	if a.latencyCount > 0 {
		a.Log.Info("latency stats loaded", "count", a.latencyCount,
			"avg_ms", a.latencySumMs/a.latencyCount, "last_ms", a.latencyLastMs)
	}
	a.mu.Unlock()

	// Restore share statistics.
	if v, err := a.Store.GetKV(kvRejectReasonsAll); err == nil && v != "" {
		var m map[string]int64
		if jerr := json.Unmarshal([]byte(v), &m); jerr == nil {
			a.mu.Lock()
			a.alltimeRejectReasons = m
			a.mu.Unlock()
		}
	}
	if v, err := a.Store.GetKV(kvDiffDistAll); err == nil && v != "" {
		var dp struct {
			Buckets [6]int64 `json:"b"`
			Count   int64    `json:"n"`
			Sum     float64  `json:"s"`
		}
		if jerr := json.Unmarshal([]byte(v), &dp); jerr == nil {
			a.mu.Lock()
			a.alltimeDiffDist = dp.Buckets
			a.alltimeDiffCount = dp.Count
			a.alltimeDiffSum = dp.Sum
			a.mu.Unlock()
		}
	}

	// From here the alltime counters reflect the store, so persisting
	// them can no longer erase history.
	a.mu.Lock()
	a.statsLoaded = true
	loadedShares := a.alltimeDiffCount
	a.mu.Unlock()
	a.Log.Info("share stats loaded", "alltime_shares", loadedShares)

	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	if samples, err := a.Store.HashrateSince(cutoff); err != nil {
		a.Log.Warn("hashrate history load failed", "err", err)
	} else if len(samples) > 0 {
		hist := make([]HashratePoint, 0, len(samples))
		for _, s := range samples {
			hist = append(hist, HashratePoint{T: s.T, V: s.V})
		}
		a.mu.Lock()
		a.hrHistory = hist
		a.lastHRSampleAt = time.Unix(hist[len(hist)-1].T, 0)
		a.mu.Unlock()
		a.Log.Info("hashrate history loaded", "count", len(hist))
	}
}
