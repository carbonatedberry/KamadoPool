// Package httpapi serves the Kamado REST API. WebSocket push and static
// UI serving land in a follow-up commit.
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kamadopool/kamado-api/internal/accelerator"
	"github.com/kamadopool/kamado-api/internal/state"
	"github.com/kamadopool/kamado-api/internal/webui"
)

type Server struct {
	Agg *state.Aggregator
	Acc *accelerator.Service
	Hub *Hub
	Log *slog.Logger
}

func New(agg *state.Aggregator, acc *accelerator.Service, log *slog.Logger) *Server {
	return &Server{Agg: agg, Acc: acc, Hub: NewHub(), Log: log}
}

// Handler returns an http.Handler with all kamado routes mounted under
// /api and the embedded Svelte dashboard served under /. Unknown
// non-/api paths fall back to index.html for SPA-style routing.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/pool", s.pool)
	mux.HandleFunc("GET /api/users", s.users)
	mux.HandleFunc("GET /api/workers", s.workers)
	mux.HandleFunc("GET /api/clients", s.clients)
	mux.HandleFunc("GET /api/blocks", s.blocks)
	mux.HandleFunc("GET /api/snapshot", s.snapshot)
	// The WebSocket handshake is exempt from the same-origin policy, so
	// it is origin-checked like the mutating routes below.
	mux.HandleFunc("GET /api/ws", s.sameOrigin(s.handleWS))
	mux.HandleFunc("GET /api/admin/debug-blocks", s.debugBlocks)
	mux.HandleFunc("POST /api/admin/reset-latency", s.sameOrigin(s.resetLatency))
	mux.HandleFunc("POST /api/admin/ack-best", s.sameOrigin(s.ackBest))
	mux.HandleFunc("POST /api/admin/reset-ack-best", s.sameOrigin(s.resetAckBest))
	mux.HandleFunc("POST /api/admin/rebuild-share-stats", s.sameOrigin(s.rebuildShareStats))
	mux.HandleFunc("POST /api/accelerate", s.sameOrigin(s.accelerate))
	mux.HandleFunc("POST /api/accelerate/cancel", s.sameOrigin(s.accelerateCancel))
	mux.HandleFunc("POST /api/accelerate/max", s.sameOrigin(s.accelerateMax))
	mux.HandleFunc("GET /api/accelerate/list", s.accelerateList)
	mux.Handle("/", spaHandler(webui.FS()))
	return securityHeaders(mux)
}

// spaHandler serves static files from the embedded dist tree and
// falls back to index.html on 404 so client-side routing works. It
// refuses anything under /api to keep the contract with mux patterns
// explicit (those routes register their own handlers above).
func spaHandler(root fs.FS) http.Handler {
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Defence-in-depth, /api routes are matched by the mux first
		// with their GET patterns, but a bare POST /api/... would fall
		// through here. Return 404 so we don't accidentally shadow
		// API semantics with HTML.
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			http.NotFound(w, r)
			return
		}

		// Fast path: exact file exists in the embed.
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(root, clean); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		} else if !errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "webui: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// SPA fallback: serve index.html with a 200 so reloads on a
		// client-side route don't 404.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- handlers ---------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	snap := s.Agg.Snapshot()
	status := http.StatusOK

	// Submit-attempt gap: if we've tried to submit more blocks than have
	// been confirmed, surface the gap. A non-zero gap is a strong signal
	// even if everything else looks healthy.
	submitGap := snap.BlockSubmitAttempts - snap.BlockSubmitsConfirmed
	if submitGap < 0 {
		submitGap = 0
	}

	// ZMQ freshness: only meaningful if the operator enabled it. Stale
	// = no event in 30 minutes (typical mainnet block interval is 10
	// min, but spikes happen).
	zmqStale := false
	if snap.ZMQEnabled && snap.HasLastZMQEvent && snap.LastZMQEventAge > 1800 {
		zmqStale = true
	}

	overallOK := snap.CKPoolOK && snap.BitcoinOK && submitGap == 0 && !zmqStale
	if !snap.CKPoolOK || !snap.BitcoinOK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ok":                    overallOK,
		"ckpool":                snap.CKPoolOK,
		"bitcoin":               snap.BitcoinOK,
		"submit_attempts":       snap.BlockSubmitAttempts,
		"submits_confirmed":     snap.BlockSubmitsConfirmed,
		"submit_gap":            submitGap,
		"zmq_enabled":           snap.ZMQEnabled,
		"zmq_event_age_seconds": snap.LastZMQEventAge,
		"zmq_has_event":         snap.HasLastZMQEvent,
		"zmq_stale":             zmqStale,
		"last_error":            snap.LastError,
	})
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Agg.Snapshot())
}

func (s *Server) pool(w http.ResponseWriter, r *http.Request) {
	snap := s.Agg.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"pool":                snap.Pool,
		"uptime_seconds":      snap.Uptime,
		"hashrate_hs_1m":      snap.HashrateHs,
		"hashrate_hs_5m":      snap.HashrateHs5m,
		"hashrate_hs_1h":      snap.HashrateHs1h,
		"hashrate_hs_24h":     snap.HashrateHs24h,
		"chain":               snap.Chain,
		"network_hashrate_hs": snap.NetworkHashrateHs,
	})
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Agg.Snapshot().Users)
}

func (s *Server) workers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Agg.Snapshot().Workers)
}

func (s *Server) clients(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Agg.Snapshot().Clients)
}

func (s *Server) blocks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Agg.Blocks())
}

// debugBlocks exposes raw block state from both the in-memory snapshot
// (what the UI sees) and the DB (ground truth) for troubleshooting.
func (s *Server) debugBlocks(w http.ResponseWriter, r *http.Request) {
	type row struct {
		Height     int64   `json:"height"`
		Hash       string  `json:"hash"`
		Chain      string  `json:"chain"`
		OrphanedAt string  `json:"orphaned_at"`
		FoundAt    string  `json:"found_at"`
		RewardBTC  float64 `json:"reward_btc"`
		Source     string  `json:"source"`
	}
	fmtBlock := func(b state.BlockRecord) row {
		orphaned := ""
		if b.OrphanedAt != nil {
			orphaned = b.OrphanedAt.Format("2006-01-02T15:04:05Z")
		}
		return row{
			Height:     b.Height,
			Hash:       b.Hash,
			Chain:      b.Chain,
			OrphanedAt: orphaned,
			FoundAt:    b.FoundAt.Format("2006-01-02T15:04:05Z"),
			RewardBTC:  b.RewardBT,
			Source:     b.Source,
		}
	}

	// In-memory blocks (what /api/blocks and /api/snapshot serve)
	memBlocks := s.Agg.Blocks()
	mem := make([]row, 0, len(memBlocks))
	for _, b := range memBlocks {
		mem = append(mem, fmtBlock(b))
	}

	// DB blocks (ground truth)
	dbBlocks := s.Agg.BlocksFromStore()
	db := make([]row, 0, len(dbBlocks))
	for _, b := range dbBlocks {
		db = append(db, fmtBlock(b))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"memory": mem,
		"db":     db,
	})
}

func (s *Server) resetLatency(w http.ResponseWriter, _ *http.Request) {
	s.Agg.ResetLatency()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// rebuildShareStats recounts the all-time share statistics from the
// ckpool log. Blocking and potentially slow (it reads the entire log),
// so it is only ever reached by an explicit operator request.
func (s *Server) rebuildShareStats(w http.ResponseWriter, _ *http.Request) {
	adopted, scanned, err := s.Agg.RebuildShareStatsFromLog()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "adopted": adopted, "shares_in_log": scanned,
	})
}

func (s *Server) ackBest(w http.ResponseWriter, _ *http.Request) {
	s.Agg.AckBestDiff()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) resetAckBest(w http.ResponseWriter, _ *http.Request) {
	s.Agg.ResetAckedBestDiff()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
