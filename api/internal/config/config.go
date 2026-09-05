// Package config loads kamado-api configuration from environment variables.
// Missing required values are a hard error at startup; optional values get
// documented defaults.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// HTTP server
	ListenAddr string

	// CKPool socket
	CKPoolSockDir string

	// Bitcoin Core RPC
	BitcoinRPCURL      string // full URL e.g. http://bitcoind:8332
	BitcoinRPCUser     string
	BitcoinRPCPassword string
	BitcoinRPCTimeout  time.Duration

	// ZMQ (Phase 2b)
	BitcoinZMQBlock string // e.g. tcp://bitcoind:28332, empty to disable

	// CKPool log file, for block-solve detection (Phase 2b)
	CKPoolLogFile string

	// SQLite DB path for persistence (Phase 2b)
	DBPath string

	// Poll interval for refreshing ckpool stats
	PollInterval time.Duration

	// Optional mempool.space-compatible explorer base URL. Empty means
	// the UI uses the public mempool.space; non-empty means the user
	// has pointed Kamado at their own instance via the StartOS config.
	MempoolBaseURL string

	// Optional JSON array describing ckpool's serverurl[] binds, so the
	// dashboard can name the transport a miner connected over instead of
	// guessing from the bind index. Rendered by whoever writes
	// ckpool.conf; empty means "undeclared" and the UI falls back. Parsed
	// in main (the concrete type lives with the snapshot it belongs to).
	StratumServersJSON string
}

func FromEnv() (*Config, error) {
	cfg := &Config{
		ListenAddr:         getenv("LISTEN_ADDR", ":8080"),
		CKPoolSockDir:      getenv("CKPOOL_SOCKDIR", "/run/ckpool"),
		BitcoinRPCURL:      os.Getenv("BITCOIN_RPC_URL"),
		BitcoinRPCUser:     os.Getenv("BITCOIN_RPC_USER"),
		BitcoinRPCPassword: os.Getenv("BITCOIN_RPC_PASSWORD"),
		BitcoinRPCTimeout:  getenvDuration("BITCOIN_RPC_TIMEOUT", 10*time.Second),
		BitcoinZMQBlock:    os.Getenv("BITCOIN_ZMQ_BLOCK"),
		CKPoolLogFile:      getenv("CKPOOL_LOGFILE", "/var/log/ckpool/ckpool.log"),
		DBPath:             getenv("DB_PATH", "/var/lib/kamado/kamado.db"),
		PollInterval:       getenvDuration("POLL_INTERVAL", 5*time.Second),
		MempoolBaseURL:     os.Getenv("MEMPOOL_BASE_URL"),
		StratumServersJSON: os.Getenv("STRATUM_SERVERS"),
	}

	if cfg.BitcoinRPCURL == "" {
		return nil, fmt.Errorf("BITCOIN_RPC_URL is required")
	}
	if cfg.BitcoinRPCUser == "" || cfg.BitcoinRPCPassword == "" {
		return nil, fmt.Errorf("BITCOIN_RPC_USER and BITCOIN_RPC_PASSWORD are required")
	}
	return cfg, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	// Accept both Go duration syntax ("5s", "10s") and bare seconds ("10").
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}
