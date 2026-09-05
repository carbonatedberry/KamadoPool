// kamado-api is the middleware that sits between ckpool-solo and the
// Kamado dashboard. It polls ckpool's Unix socket API, calls bitcoind
// over JSON-RPC, and serves the merged state over REST (WebSocket push,
// SQLite persistence, ZMQ and log-tailer-based block detection land in
// a follow-up commit).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kamadopool/kamado-api/internal/accelerator"
	"github.com/kamadopool/kamado-api/internal/bitcoind"
	"github.com/kamadopool/kamado-api/internal/ckpool"
	"github.com/kamadopool/kamado-api/internal/config"
	"github.com/kamadopool/kamado-api/internal/httpapi"
	"github.com/kamadopool/kamado-api/internal/logmon"
	"github.com/kamadopool/kamado-api/internal/state"
	"github.com/kamadopool/kamado-api/internal/store"
	"github.com/kamadopool/kamado-api/internal/zmqmon"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	log.Info("kamado-api starting",
		"listen", cfg.ListenAddr,
		"sockdir", cfg.CKPoolSockDir,
		"bitcoind", cfg.BitcoinRPCURL,
		"poll_interval", cfg.PollInterval,
	)

	ck := ckpool.New(cfg.CKPoolSockDir)
	rpc := bitcoind.NewRPC(cfg.BitcoinRPCURL, cfg.BitcoinRPCUser, cfg.BitcoinRPCPassword, cfg.BitcoinRPCTimeout)

	// Persistent block history. Non-fatal if it can't be opened, the
	// aggregator falls back to an in-memory ring so the pool keeps
	// running even with a broken data volume.
	var blockStore *store.BlockStore
	if cfg.DBPath != "" {
		if s, err := store.Open(cfg.DBPath); err != nil {
			log.Warn("block store open failed, running without persistence", "path", cfg.DBPath, "err", err)
		} else {
			blockStore = s
			defer blockStore.Close()
			log.Info("block store opened", "path", cfg.DBPath)
		}
	}

	agg := state.New(ck, rpc, cfg.PollInterval, log)
	agg.Store = blockStore
	agg.MempoolBaseURL = cfg.MempoolBaseURL
	// Purely descriptive (it drives the dashboard's connection badge), so a
	// malformed value degrades to the UI's fallback rather than refusing to
	// start a pool that is otherwise fine.
	if cfg.StratumServersJSON != "" {
		var servers []state.StratumServer
		if err := json.Unmarshal([]byte(cfg.StratumServersJSON), &servers); err != nil {
			log.Warn("ignoring malformed STRATUM_SERVERS", "err", err)
		} else {
			agg.StratumServers = servers
			log.Info("stratum binds declared", "count", len(servers))
		}
	}
	agg.LogFilePath = cfg.CKPoolLogFile
	agg.KillCKPool = killCKPool(log)

	// Transaction accelerator (prioritisetransaction).
	var accSvc *accelerator.Service
	if blockStore != nil {
		accSvc = accelerator.NewService(rpc, blockStore, log)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	api := httpapi.New(agg, accSvc, log)

	// Wire snapshot refreshes into the WebSocket hub so subscribers get
	// real-time updates without polling.
	agg.OnRefresh = api.Hub.Broadcast

	// Optional: bitcoind hashblock ZMQ subscription for sub-second
	// chain-tip refreshes. Empty endpoint disables it.
	zmq := zmqmon.New(cfg.BitcoinZMQBlock, log)
	go zmq.Run(ctx)

	// Restore persisted state before anything can mutate it. The log
	// tailer's ingest goroutines below share these counters and persist
	// them on their first event, so starting them against a half-loaded
	// aggregator overwrites the accumulated history with a few seconds
	// of new shares.
	agg.LoadPersisted()

	go agg.Run(ctx, zmq.Events)

	// Background reconciliation: retry hash/reward enrichment for blocks
	// the initial RPC lookup couldn't fetch, and detect chain reorgs by
	// comparing recorded hashes against the canonical chain.
	go agg.ReconcileBlocks(ctx)

	// Tail the ckpool log for block-solve events (our own solves).
	tailer := logmon.New(cfg.CKPoolLogFile, log)
	if blockStore != nil {
		const cursorKey = "logmon_cursor"
		tailer.LoadCursor = func() (uint64, int64, bool) {
			v, err := blockStore.GetKV(cursorKey)
			if err != nil || v == "" {
				return 0, 0, false
			}
			parts := strings.SplitN(v, ":", 2)
			if len(parts) != 2 {
				return 0, 0, false
			}
			ino, err1 := strconv.ParseUint(parts[0], 10, 64)
			off, err2 := strconv.ParseInt(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				return 0, 0, false
			}
			return ino, off, true
		}
		tailer.SaveCursor = func(ino uint64, off int64) {
			if err := blockStore.SetKV(cursorKey, fmt.Sprintf("%d:%d", ino, off)); err != nil {
				log.Warn("logmon cursor persist failed", "err", err)
			}
		}
	}
	// Wait for the aggregator's first refresh before starting the log
	// tailer. This guarantees currentChain is set before any block
	// events are processed, so blocks are always stamped with the
	// correct chain, preventing false orphans on network switches.
	select {
	case <-agg.Ready():
		log.Info("first snapshot ready")
	case <-time.After(8 * time.Second):
		log.Warn("first snapshot not ready within 8s, serving HTTP anyway (snapshot will be partial until backends respond)")
	case <-ctx.Done():
		return
	}

	go tailer.Run(ctx)
	go agg.IngestBlockEvents(ctx, tailer.Events)
	go agg.IngestAttemptEvents(ctx, tailer.Attempts)
	go agg.IngestLatencyEvents(ctx, tailer.Latencies)
	go agg.IngestShareEvents(ctx, tailer.Shares)

	if accSvc != nil {
		go accSvc.Cleanup(ctx)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Shutdown on ctx cancel
	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("http listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http server", "err", err)
		os.Exit(1)
	}
}

// killCKPool returns a function that finds the ckpool process by name
// and sends it SIGTERM. Used by the aggregator to disconnect miners
// when bitcoind is unreachable so they can failover to other pools.
func killCKPool(log *slog.Logger) func() error {
	return func() error {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return fmt.Errorf("read /proc: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			if err != nil {
				continue
			}
			// ckpool's cmdline is NUL-separated; the first arg is the binary path.
			if strings.Contains(string(cmdline), "ckpool") {
				log.Info("sending SIGTERM to ckpool", "pid", pid)
				proc, err := os.FindProcess(pid)
				if err != nil {
					return fmt.Errorf("find process %d: %w", pid, err)
				}
				return proc.Signal(syscall.SIGTERM)
			}
		}
		return fmt.Errorf("ckpool process not found")
	}
}
