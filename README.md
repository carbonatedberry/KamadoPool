# Kamado Pool

A solo Bitcoin mining pool built on a patched fork of [CKPool](https://bitbucket.org/ckolivas/ckpool), with a Go middleware API, real-time Svelte dashboard, and full StartOS integration.

Kamado exists because existing CKPool wrappers like Bassin read only a handful of periodic stats files and miss most of CKPool's rich runtime data. Kamado talks directly to CKPool's Unix socket API, subscribes to bitcoind via both RPC and ZMQ, tails CKPool's log for block-solve events, and merges everything into a single live snapshot that the dashboard consumes over WebSocket.

## Architecture

```
                         ┌──────────────────────────────┐
  Miners ──stratum:3333──►  ckpool-solo (C, patched)    │
                         │    │ Unix socket    │ log    │
                         │    ▼                ▼        │
                         │  kamado-api (Go)             │
                         │    │ RPC+ZMQ    ▲ HTTP/WS    │
                         │    ▼            │            │
                         │  bitcoind     Browser :8080  │
                         └──────────────────────────────┘
```

| Component | Language | Role |
|-----------|----------|------|
| `ckpool/` | C | Stratum server, share validation, vardiff, block assembly and submission |
| `api/` | Go | Socket client, bitcoind RPC, ZMQ subscriber, log tailer, state aggregator, REST + WebSocket API, SQLite persistence |
| `ui/` | Svelte 5 | Real-time dashboard with pool overview, miner stats, block history, best share tracking, transaction accelerator |

The Go binary embeds the built Svelte app via `//go:embed` and serves it at `/`, a single static binary with no external web server.

## Improvements Over Upstream CKPool

CKPool is a high-performance stratum server, but it has no web interface and limited observability. Kamado adds a complete operational layer on top:

### Patches Applied to CKPool

Nine patches are applied to upstream commit `cfb0f83` (which itself includes the workbase_id fix, extended ESP32/NerdMiner timeouts, configurable `dropidle`, and vardiff improvements):

| Patch | Purpose |
|-------|---------|
| `0001` | Expose `bestever` (all-time best share) in runtime socket JSON alongside `bestdiff` (current round) |
| `0002` | Always reply on the listener socket so kamado-api gets responses in `btcsolo` mode |
| `0003` | Return share errors as proper Stratum `[code, msg, null]` arrays per the Slush pool protocol spec |
| `0004` | Expose per-worker share counts (accepted/rejected) in runtime socket JSON |
| `0005` | Log ZMQ-to-notify latency for block-update performance monitoring |
| `0006` | Expose raw reject count in pool stats |
| `0007` | Replace hardcoded "ckpool" branding in the coinbase scriptSig with "kamado" (same 6 bytes, consensus-safe) |
| `0008` | Log full PoW reproduction data (raw 80-byte header, coinbase, merkle branches) when a share sets a new best difficulty |
| `0009` | Send `mining.set_difficulty` before the first `mining.notify`, resume a reconnecting miner's tuned difficulty, and grant its first job a difficulty grace window |

### Middleware API

The Go API (`kamado-api`) bridges CKPool's Unix socket protocol, bitcoind's JSON-RPC, and ZMQ into a unified HTTP/WebSocket interface:

- **Socket client**: CKPool uses a 4-byte length-prefixed binary protocol on a Unix domain socket. kamado-api opens a fresh connection per request and queries pool, user, worker, and client state in real time.
- **State aggregator**: Merges socket responses, bitcoind chain info, ZMQ events, and log-tailed block solves into a single thread-safe snapshot, refreshed on a configurable interval.
- **ZMQ subscriber**: Listens to bitcoind's `hashblock` topic for sub-second block notifications, triggering an immediate state refresh. Falls back to RPC polling if ZMQ is unavailable.
- **Log tailer**: Watches CKPool's log file (inotify-based) for "Solved and confirmed block" lines, extracts height and hash, enriches via bitcoind RPC (reward, confirmations), and records to SQLite.
- **Block reconciliation**: On startup, cross-references in-memory blocks with the database and bitcoind to detect orphans and fill gaps.
- **Chain reorg detection**: Scoped to the active chain; clears stale state on tip changes.
- **SQLite persistence**: Blocks, accelerated transactions, and log cursor survive restarts. Non-fatal if unavailable (in-memory ring fallback).
- **Transaction accelerator**: Inspects the current block template to find the marginal (lowest fee-rate) transaction, calls `prioritisetransaction` to boost a target tx, and reports the displaced fee as revenue impact. Includes a hard cap (2000 sat/vB) and lifecycle cleanup.

### Stratum TLS

Optional encrypted stratum via stunnel. CKPool binds a public plaintext socket plus one loopback-only socket per TLS certificate; stunnel terminates TLS on the external port and forwards decrypted traffic to the matching internal bind. CKPool tags each connection with its `serverurl` index, and the deployment declares what those indices mean via the `STRATUM_SERVERS` env, so the dashboard shows a lock icon next to encrypted miners and names the certificate in use on hover, with no source-IP heuristics.

Under StartOS this drives two certificates on one port, chosen per connection by SNI: a CA-issued (Let's Encrypt) certificate for miners connecting over a clearnet domain, and the self-signed one for miners on the LAN, which send no SNI and fall through to it.

The self-signed certificate is auto-generated on first start with broad SAN coverage (`.local`, `.embassy`, `.onion`, `.lan`, `.home.arpa`, `.internal`) so miner firmware that validates the SAN against the connection hostname (e.g. AxeOS with mbedtls) works without manual cert pinning. A version marker triggers automatic regeneration when the cert format changes.

### Dashboard

The Svelte 5 dashboard connects via WebSocket for real-time push updates (no polling in normal operation) and includes:

- **Pool overview**: Hashrate (1m/5m/1h/24h), uptime, workers online, shares accepted/rejected, expected time to block, round effort
- **Hashrate chart**: Interactive multi-window visualization
- **Miners table**: Per-user and per-worker stats: hashrate, difficulty, latency, best shares, idle detection
- **Hardware detection**: Parses stratum user-agent strings to identify miner hardware (Bitaxe, Bitaxe Hex, NerdMiner, NerdAxe, NerdQAxe, NerdOCTAXE, NerdEKO, NerdNOS, PiAxe, QAxe, 0xAxe, LeafMiner, and more). Open-source hardware is flagged with a star badge so operators can see their fleet composition at a glance.
- **TLS badges**: Miners connected via the encrypted stratum port display a lock icon in the miners table, derived from CKPool's `server` field (no IP heuristics).
- **User/worker detail pages**: Deep dive into individual miner stats, cumulative work, and luck
- **Block history**: Found blocks with height, hash, reward, solving worker, chain name, orphan status
- **Best share leaderboard**: "This round" and "all-time" tracking with per-worker breakdown and glowing difficulty range indicators
- **PoW inspector**: Graphical breakdown of the block header behind the best share (version, prev block, merkle root, time, bits, nonce), the raw 80-byte header hex, and the coinbase + merkle branches that reproduce the merkle root, with one-click in-browser verification that replays the real SHA-256 compression rounds as an animation, ending at the share's block hash, self-contained implementation, no external services, works over plain-HTTP LAN
- **Transaction accelerator**: Boost transactions via `prioritisetransaction` with marginal fee displacement analysis showing the revenue impact of each boost
- **Block-found animation**: Celebratory toast on solve events
- **Health banners**: Live status indicators for CKPool, bitcoind, ZMQ, and submit-gap diagnostics
- **Block-update latency**: Tracks and displays average, last, and wasted-work latency for block notifications so operators can tune ZMQ and polling
- **Shares bar**: Accepted/rejected/stale share summary with ratio visualization
- **Custom mempool explorer**: Transaction and block links can point at a self-hosted mempool instance instead of the public mempool.space
- **Mobile responsive**: Full breakpoint coverage for phone and tablet
- **Donation footer**: BTC address with hover QR code

### Reliability

- **Dual block notification**: ZMQ hashblock for instant detection + RPC polling as fallback. Both run simultaneously; every second of stale work in solo mode is hashrate burned on a dead block.
- **ckpool kill-on-failure**: When bitcoind becomes unreachable, kamado-api kills ckpool so miners can failover to another pool. ckpool restarts automatically when bitcoind recovers.
- **Deferred RPC**: Dashboard RPCs wait until the ckpool notifier completes on tip change to prioritize system resources for block reconstruction and validation.
- **Log cursor persistence**: The tailer's file offset is stored in SQLite so block-solve detection resumes correctly after restart.
- **Load before ingest**: Persisted counters are restored *before* the log-tailer goroutines start. They share the same state and persist it on their first event, so starting them against a half-restored aggregator would write a few seconds of new shares over the accumulated all-time history, and then read that back as the new truth. `RebuildShareStatsFromLog` (exposed as `POST /api/admin/rebuild-share-stats`) recounts the all-time difficulty distribution and rejection reasons from the ckpool log if they are ever lost; it only adopts the result when the log accounts for more shares than the stored totals, so it can restore history but never erase it.
- **Clean restarts for miners**: Upstream ckpool sends a reconnecting miner its first job *before* telling it the difficulty, and resets every client to the pool's `startdiff`, discarding what vardiff had tuned. Both produce a burst of "Above target" rejects on every pool restart. Patch `0009` sends `mining.set_difficulty` first, seeds the starting difficulty from the worker's retained hashrate, and accepts shares for the already-in-flight job down to the pool minimum until the next `mining.notify`.
- **Warm-up resilience**: The ckpool socket client handles transient EOFs during startup gracefully instead of crashing.

## API Reference

| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/api/health` | Pool + bitcoind health, submit-gap tracking, ZMQ staleness |
| `GET` | `/api/pool` | Pool stats, hashrate windows, chain info, network hashrate |
| `GET` | `/api/users` | All users: shares, best diff, idle status |
| `GET` | `/api/workers` | All workers: hashrate, best share, share counts |
| `GET` | `/api/clients` | Active stratum sessions: useragent, IP, assigned difficulty |
| `GET` | `/api/blocks` | Recent solved blocks: height, hash, reward, orphan status |
| `GET` | `/api/snapshot` | Full merged snapshot (everything above combined) |
| `GET` | `/api/ws` | WebSocket: push on every state refresh + immediate on block solve |
| `GET` | `/api/admin/debug-blocks` | In-memory vs. DB block discrepancy troubleshooting |
| `POST` | `/api/admin/reset-latency` | Zero all block-update latency counters |
| `POST` | `/api/admin/ack-best` | Acknowledge new best share (UI state marker) |
| `POST` | `/api/admin/reset-ack-best` | Reset best share acknowledgment |
| `POST` | `/api/accelerate` | Boost a transaction via `prioritisetransaction` |
| `POST` | `/api/accelerate/cancel` | Cancel a previously boosted transaction |
| `POST` | `/api/accelerate/max` | Boost a tx with a new feerate 2x the mempool highest (capped at 2000 sat/vB) |
| `GET` | `/api/accelerate/list` | List all currently boosted transactions |

## Development

### Prerequisites

- Go 1.22+
- Node.js 22+
- Docker and Docker Compose
- bitcoind (for regtest testing)

### Quick Start

```bash
cp .env.example .env          # set bitcoind RPC credentials
make up                       # build images + start ckpool + api
```

The dashboard is at `http://localhost:8080`. Point a miner at `stratum+tcp://localhost:3333` with a valid Bitcoin address as the username.

### Make Targets

| Target | Description |
|--------|-------------|
| `make up` | Build and start all services |
| `make down` | Stop all services |
| `make logs` | Tail ckpool + api logs |
| `make api` | Build the `kamado/api:dev` image |
| `make ckpool` | Build the `kamado/ckpool:dev` image |
| `make api-test` | Run Go tests with race detector |
| `make ui` | Build Svelte dashboard to `ui/dist` |
| `make ui-dev` | Start Vite dev server (`:5173`, proxies `/api` to `:8080`) |
| `make ui-check` | Run svelte-check type diagnostics |
| `make clean` | Remove data, build artifacts, and volumes |

### Environment Variables

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `BITCOIN_RPC_URL` |, | yes | bitcoind RPC endpoint (e.g. `http://127.0.0.1:8332`) |
| `BITCOIN_RPC_USER` |, | yes | RPC username |
| `BITCOIN_RPC_PASSWORD` |, | yes | RPC password |
| `LISTEN_ADDR` | `:8080` | no | HTTP bind address |
| `CKPOOL_SOCKDIR` | `/run/ckpool` | no | CKPool Unix socket directory |
| `CKPOOL_LOGFILE` | `/var/log/ckpool/ckpool.log` | no | CKPool log path for block-solve detection |
| `DB_PATH` | `/var/lib/kamado/kamado.db` | no | SQLite database path |
| `POLL_INTERVAL` | `5s` | no | State refresh interval |
| `BITCOIN_ZMQ_BLOCK` | (disabled) | no | ZMQ hashblock endpoint (e.g. `tcp://127.0.0.1:28332`) |
| `BITCOIN_RPC_TIMEOUT` | `10s` | no | RPC call timeout |
| `MEMPOOL_BASE_URL` | (mempool.space) | no | Custom mempool explorer URL |
| `STRATUM_SERVERS` | (undeclared) | no | JSON array describing ckpool's `serverurl[]` binds, e.g. `[{"kind":"plain","label":"Plaintext"},{"kind":"tls-local","label":"TLS, self-signed"}]`. Entry N labels clients with `server == N` in the dashboard. Unset falls back to treating index 1 as TLS |

### UI Development

```bash
make ui-dev
```

Starts Vite on `:5173` with hot reload. API calls are proxied to `:8080`, run `make up` first so the backend is available.

### Testing

```bash
make api-test                          # Go unit tests (race detector enabled)
cd api && go test -race ./...          # same thing without Make
./test/regtest_smoke.sh                # Full regtest integration test
```

#### Go Unit Tests

The test suite covers the critical mining path with 38 tests across 5 packages:

| Package | Tests | Coverage |
|---------|-------|----------|
| `bitcoind` | RPC retry logic (transient 503s, warmup errors, semantic errors, exhausted retries), coinbase reward extraction | Ensures the RPC client retries on transport failures and bitcoind warmup but fails fast on semantic errors |
| `ckpool` | Socket client ping, pool stats parsing, client listing, dial error handling | Validates the 4-byte length-prefixed binary protocol against a mock Unix socket |
| `logmon` | Line parsing (solved blocks, submitting variants, diff reset, unrelated lines), tailer run (basic read, cursor resume after restart, log rotation) | Covers the full log tailer lifecycle including inotify-based file watching |
| `state` | Block reconciliation (chain-scoped filtering, genuine reorgs, RPC errors, legacy blocks, cross-network), block ingestion (new blocks, dedup, chain stamping) | Ensures reorg detection never false-orphans blocks from other chains |
| `store` | SQLite roundtrips (insert, dedup, orphan marking, enrichment updates, enrichment queries, chain column migration, KV store) | Validates schema migrations and all persistence operations |

#### Regtest Smoke Test

The smoke test runs two phases on regtest:

1. **Log-inject path**, Injects a synthetic block-solve line into ckpool's log, verifies the tailer detects it, enriches it via bitcoind RPC, and surfaces it at `/api/blocks`. Quick, no ckpool binary needed.
2. **Full stratum path**, Starts ckpool, connects a Python stratum miner, mines a real block, verifies the coinbase contains the "kamado" tag and the configured pool identifier, validates that bitcoind accepted the block with confirmations > 0, and checks that the block validation log shows correct chain acceptance.

The smoke test auto-builds ckpool from the pinned upstream source with patches applied if not cached (build cached in `~/.cache/kamado-dev/`).

### Project Structure

```
api/
  cmd/kamado-api/       Main entry point
  internal/
    accelerator/        Transaction priority boosting via prioritisetransaction
    bitcoind/           Minimal JSON-RPC client with retry logic
    ckpool/             Unix socket protocol client (4-byte LE framing)
    config/             Environment variable loading and validation
    httpapi/            REST routes, WebSocket hub, SPA handler
    logmon/             inotify-based CKPool log tailer
    state/              Snapshot aggregator, block reconciliation, reorg detection
    store/              SQLite persistence (blocks, KV, accelerated txs, cursor)
    webui/              Embedded Svelte assets (go:embed)
    zmqmon/             ZMQ hashblock subscriber
ckpool/
  patches/              Seven-patch series applied to upstream
  config/               ckpool.conf.template (sed-rendered at startup)
  Dockerfile            Two-stage ckpool build
ui/
  src/
    lib/                Svelte 5 components (dashboard, miners, blocks, accelerator)
    format.ts           Hardware detection, hashrate formatting, address parsing
    snapshot.svelte.ts  Global reactive store (WebSocket + REST fallback)
    types.ts            TypeScript interfaces for API responses
test/
  regtest_smoke.sh      Two-phase integration test (log-inject + full stratum)
```

## StartOS

The StartOS wrapper can be found at: https://github.com/carbonatedberry/KamadoPool-StartOS

## Upstream

CKPool by Con Kolivas, https://bitbucket.org/ckolivas/ckpool

Pinned commit: `cfb0f83b70d7b382b85d2bd0710cf4cb2dda4007`

## License

GPL-3.0, see [LICENSE](LICENSE).
