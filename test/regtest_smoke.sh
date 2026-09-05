#!/usr/bin/env bash
# Regtest smoke test for kamado-api, two phases:
#
# Phase 1 (always): log-inject path
#   Injects a "Solved and confirmed block N" line directly into the ckpool log
#   and verifies the full tailer → aggregator → bitcoind RPC enrichment → /api/blocks
#   chain. Quick, no ckpool binary needed.
#
# Phase 2 (auto): full stratum path, the complete revenue-critical path
#   A Python stratum miner connects to ckpool, mines a real diff-1 SHA256d hash
#   using multiprocessing, submits the winning share, ckpool calls bitcoind
#   submitblock, logs "Solved and confirmed block N+1", and the kamado-api
#   tailer propagates it to /api/blocks.
#   ckpool is auto-built from the pinned source in ckpool/ if not already cached.
#   Built binary is cached in ~/.cache/kamado-dev/ (keyed on commit hash) so
#   subsequent runs are instant.
#
# Prerequisites: bitcoind, bitcoin-cli, jq, curl, go, python3
#               + gcc/autoconf/automake/libtool/pkg-config/libzmq-dev for ckpool build
#
# Usage:
#   test/regtest_smoke.sh
#   KAMADO_BIN=./api/bin/kamado-api test/regtest_smoke.sh
#   KAMADO_BIN=./api/bin/kamado-api CKPOOL_BIN=/usr/local/bin/ckpool test/regtest_smoke.sh

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
API_DIR="$SCRIPT_DIR/../api"

# ---- helpers ----------------------------------------------------------------

log()  { printf '[smoke] %s\n' "$*"; }
die()  { printf '[smoke] FAIL: %s\n' "$*" >&2; exit 1; }

WORK_DIR=""
BITCOIN_PID=""
KAMADO_PID=""
CKPOOL_PID=""
MINER_PID=""
CKPOOL_BUILD_TMP=""

cleanup() {
    [[ -n "$MINER_PID"          ]] && kill "$MINER_PID"          2>/dev/null || true
    [[ -n "$CKPOOL_PID"         ]] && kill "$CKPOOL_PID"         2>/dev/null || true
    [[ -n "$KAMADO_PID"         ]] && kill "$KAMADO_PID"         2>/dev/null || true
    [[ -n "$BITCOIN_PID"        ]] && kill "$BITCOIN_PID"        2>/dev/null || true
    [[ -n "$WORK_DIR"           ]] && rm -rf "$WORK_DIR"
    [[ -n "$CKPOOL_BUILD_TMP"   ]] && rm -rf "$CKPOOL_BUILD_TMP"
}
trap cleanup EXIT

# ---- prerequisites ----------------------------------------------------------

for cmd in bitcoind bitcoin-cli jq curl python3; do
    command -v "$cmd" >/dev/null 2>&1 || die "required command not found: $cmd"
done

# Locate or build kamado-api.
if [[ -n "${KAMADO_BIN:-}" ]]; then
    [[ -x "$KAMADO_BIN" ]] || die "KAMADO_BIN=$KAMADO_BIN is not executable"
elif command -v go >/dev/null 2>&1; then
    WORK_DIR=$(mktemp -d)
    log "building kamado-api…"
    (cd "$API_DIR" && go build -o "$WORK_DIR/kamado-api" ./cmd/kamado-api) \
        || die "go build failed"
    KAMADO_BIN="$WORK_DIR/kamado-api"
else
    die "KAMADO_BIN not set and go not in PATH; build the binary first:
       cd api && go build -o bin/kamado-api ./cmd/kamado-api
       KAMADO_BIN=api/bin/kamado-api test/regtest_smoke.sh"
fi

[[ -n "${WORK_DIR:-}" ]] || WORK_DIR=$(mktemp -d)

# Locate or build ckpool, required for Phase 2.
# Priority: CKPOOL_BIN env var → PATH → cached build → build from source.
CKPOOL_AVAILABLE=false
CKPOOL_DIR="$SCRIPT_DIR/../ckpool"
CKPOOL_REPO=$(cat "$CKPOOL_DIR/CKPOOL_REPO" | tr -d '[:space:]')
CKPOOL_COMMIT=$(cat "$CKPOOL_DIR/CKPOOL_COMMIT" | tr -d '[:space:]')
# Cache keyed on commit so a pinned-commit bump automatically triggers a rebuild.
CKPOOL_CACHE="$HOME/.cache/kamado-dev/ckpool-${CKPOOL_COMMIT:0:12}/ckpool"

if [[ -n "${CKPOOL_BIN:-}" ]]; then
    [[ -x "$CKPOOL_BIN" ]] || die "CKPOOL_BIN=$CKPOOL_BIN is not executable"
    CKPOOL_AVAILABLE=true
elif command -v ckpool >/dev/null 2>&1; then
    CKPOOL_BIN=$(command -v ckpool)
    CKPOOL_AVAILABLE=true
elif [[ -x "$CKPOOL_CACHE" ]]; then
    CKPOOL_BIN="$CKPOOL_CACHE"
    CKPOOL_AVAILABLE=true
    log "ckpool: using cached build at $CKPOOL_BIN"
elif command -v gcc autoconf automake libtool pkg-config git >/dev/null 2>&1 \
     && pkg-config --exists libzmq 2>/dev/null; then
    log "ckpool: building from source at commit ${CKPOOL_COMMIT:0:12}…"
    log "  (this takes ~1-2 min; the result is cached for future runs)"
    CKPOOL_BUILD_TMP=$(mktemp -d)   # cleaned up by the existing cleanup trap
    git clone --quiet "$CKPOOL_REPO" "$CKPOOL_BUILD_TMP/ckpool" \
        || die "ckpool: git clone failed"
    (
        cd "$CKPOOL_BUILD_TMP/ckpool"
        git checkout --quiet "$CKPOOL_COMMIT"
        for patch in "$CKPOOL_DIR/patches/"*.patch; do
            [[ -f "$patch" ]] || continue
            log "ckpool: applying $(basename "$patch")"
            git apply "$patch"
        done
        ./autogen.sh >/dev/null 2>&1
        CFLAGS="-O2 -Wall -pipe" ./configure --quiet --prefix=/usr/local >/dev/null
        make -j"$(nproc)" >/dev/null
    ) || die "ckpool: build failed"
    mkdir -p "$(dirname "$CKPOOL_CACHE")"
    cp "$CKPOOL_BUILD_TMP/ckpool/src/ckpool" "$CKPOOL_CACHE"
    chmod +x "$CKPOOL_CACHE"
    rm -rf "$CKPOOL_BUILD_TMP"
    CKPOOL_BIN="$CKPOOL_CACHE"
    CKPOOL_AVAILABLE=true
    log "ckpool: build complete → $CKPOOL_BIN"
else
    log "ckpool not found and build prerequisites missing, Phase 2 will be skipped"
    log "  Missing one or more of: gcc, autoconf, automake, libtool, pkg-config, git, libzmq-dev"
    log "  Or set CKPOOL_BIN=/path/to/ckpool to skip the build"
fi

# Write the Python stratum miner, no build step needed; python3 is in PATH (checked above).
# The miner uses hashlib SHA256 midstate optimization + multiprocessing so it scales linearly
# with CPU count. Diff-1 requires on average 2^32 ≈ 4.3B hashes; at ~1.5 MH/s per core this
# takes ~43 minutes on a single core but ~3 minutes on 16 cores.
STRATUM_MINER="$WORK_DIR/stratum_miner.py"
cat > "$STRATUM_MINER" << 'PYEOF'
#!/usr/bin/env python3
"""
ckpool regtest stratum miner.

Field encoding derived from ckpool source (bitcoin.c / stratifier.c):
  version, ntime, nbits: sent as 8-char big-endian hex of the integer value.
    → canonical LE header bytes: struct.pack('<I', int(hex_str, 16))
  prevhash: sent as swap_256(hex2bin(display_hash)) rendered as hex.
    swap_256 reverses the 8 uint32-word order without byte-swapping each word.
    This equals: each 4-byte group of the raw SHA256d bytes, byte-reversed.
    → canonical bytes: b''.join(ph[i:i+4][::-1] for i in range(0,32,4))
  nonce submission: f'{nonce_int:08x}' (big-endian hex).
    ckpool does hex2bin → stores → flip_80 → canonical LE nonce. ✓
  diff-1 check: int.from_bytes(sha256d_bytes, 'little') <= DIFF1.
    SHA256d output bytes[28..31] must be near-zero for a valid Bitcoin hash.
    le256todouble() in ckpool reads bytes[24..31] as the most-significant word
    (little-endian 256-bit), so zeros at the trailing end pass the check.
    Using 'big' endian finds leading-zero hashes which ckpool rejects (diff≈0).
"""
import hashlib, json, os, socket, struct, sys, time
from multiprocessing import Process, Queue, Event

DIFF1 = int("00000000FFFF0000000000000000000000000000000000000000000000000000", 16)


def sha256d(b):
    return hashlib.sha256(hashlib.sha256(b).digest()).digest()


def _mine_worker(h64, h12, start, end, out_q, stop_ev):
    """Hash nonces [start..end]; put found nonce or None (exhausted) into out_q."""
    mid = hashlib.sha256()
    mid.update(h64)
    payload = bytearray(16)
    payload[:12] = h12
    n = start
    BATCH = 200_000
    sha256 = hashlib.sha256
    while n <= end:
        if stop_ev.is_set():
            out_q.put(None)
            return
        lim = min(n + BATCH, end + 1)
        while n < lim:
            h = mid.copy()
            struct.pack_into('<I', payload, 12, n)
            h.update(payload)
            if int.from_bytes(sha256(h.digest()).digest(), 'little') <= DIFF1:
                out_q.put(n)
                return
            n += 1
    out_q.put(None)


def build_header76(ver_hex, prevhash_hex, coinb1, en1_hex, en2, coinb2, branches, ntime_hex, nbits_hex):
    version  = struct.pack('<I', int(ver_hex, 16))
    ph       = bytes.fromhex(prevhash_hex)
    prevhash = b''.join(ph[i:i+4][::-1] for i in range(0, 32, 4))
    coinbase = bytes.fromhex(coinb1) + bytes.fromhex(en1_hex) + en2 + bytes.fromhex(coinb2)
    root     = sha256d(coinbase)
    for br in branches:
        root = sha256d(root + bytes.fromhex(br))
    ntime = struct.pack('<I', int(ntime_hex, 16))
    nbits = struct.pack('<I', int(nbits_hex, 16))
    return version + prevhash + root + ntime + nbits


def main():
    host, port, user, _log_path = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]

    def log(msg):
        # stdout is redirected to $MINER_LOG by the shell; no double-write needed
        print(f'[miner] {msg}', flush=True)

    sock = socket.create_connection((host, port), timeout=30)
    buf = ''
    msg_id = 1

    def send(d):
        sock.sendall((json.dumps(d) + '\n').encode())

    def recv_line(timeout=60):
        nonlocal buf
        sock.settimeout(timeout)
        while '\n' not in buf:
            chunk = sock.recv(4096).decode(errors='replace')
            if not chunk:
                raise EOFError('stratum connection closed')
            buf += chunk
        line, buf = buf.split('\n', 1)
        return json.loads(line)

    # Subscribe
    send({'id': msg_id, 'method': 'mining.subscribe', 'params': []}); msg_id += 1
    resp = recv_line()
    en1_hex = resp['result'][1]
    en2_size = resp['result'][2]
    log(f'Subscribed: extranonce1={en1_hex} extranonce2_size={en2_size}')

    # Authorize
    send({'id': msg_id, 'method': 'mining.authorize', 'params': [user, 'x']}); msg_id += 1

    # Wait for the first mining.notify
    job = None
    while job is None:
        msg = recv_line(timeout=30)
        method = msg.get('method', '')
        if method == 'mining.notify':
            p = msg['params']
            job = {'id': p[0], 'ph': p[1], 'cb1': p[2], 'cb2': p[3],
                   'br': p[4], 'ver': p[5], 'nb': p[6], 'nt': p[7]}
            log(f'Job received: id={job["id"]} ntime={job["nt"]} nbits={job["nb"]}')

    n_proc = min(os.cpu_count() or 4, 16)
    log(f'Starting miner with {n_proc} worker processes')

    # Benchmark real SHA256d rate before starting workers
    _bh76 = build_header76(job['ver'], job['ph'], job['cb1'], en1_hex,
                           (0).to_bytes(en2_size, 'little'), job['cb2'], job['br'], job['nt'], job['nb'])
    _bh64, _bh12 = _bh76[:64], _bh76[64:76]
    _bmid = hashlib.sha256(); _bmid.update(_bh64)
    _bpayload = bytearray(16); _bpayload[:12] = _bh12
    _bt0 = time.time()
    for _bn in range(20_000):
        _bh = _bmid.copy(); struct.pack_into('<I', _bpayload, 12, _bn)
        _bh.update(_bpayload); hashlib.sha256(_bh.digest()).digest()
    bench_mhs = 20_000 / (time.time() - _bt0) / 1e6
    total_mhs = bench_mhs * n_proc
    expected_s = 4_295_000_000 / (total_mhs * 1e6)
    log(f'Benchmark: {bench_mhs*1e3:.0f} kH/s per process → {total_mhs:.2f} MH/s total')
    log(f'Expected solve time: {expected_s:.0f}s average ({expected_s/60:.1f} min)')

    en2_int = 0
    t0 = time.time()
    last_log_time = t0

    while True:
        # Snapshot the job and en2 used to build this round's header.
        # These must match exactly what we submit to ckpool, if job is
        # updated mid-round we still submit against the header we actually mined.
        cur_job = job
        cur_en2 = en2_int.to_bytes(en2_size, 'little')
        cur_en2_hex = cur_en2.hex()

        h76 = build_header76(cur_job['ver'], cur_job['ph'], cur_job['cb1'], en1_hex,
                             cur_en2, cur_job['cb2'], cur_job['br'], cur_job['nt'], cur_job['nb'])
        h64 = h76[:64]
        h12 = h76[64:76]

        # Launch n_proc workers, each covering an equal nonce range
        out_q = Queue()
        stop_ev = Event()
        chunk = 0xFFFFFFFF // n_proc
        procs = []
        for i in range(n_proc):
            ns = chunk * i
            ne = chunk * (i + 1) - 1 if i < n_proc - 1 else 0xFFFFFFFF
            p = Process(target=_mine_worker,
                        args=(h64, h12, ns, ne, out_q, stop_ev),
                        daemon=True)
            p.start()
            procs.append(p)

        found_nonce = None
        exhausted = 0
        clean_job_arrived = False
        sock.settimeout(0.3)

        while found_nonce is None and exhausted < n_proc and not clean_job_arrived:
            now = time.time()
            if now - last_log_time >= 30:
                elapsed = now - t0
                eta = max(0, expected_s - elapsed)
                log(f'mining: elapsed={elapsed:.0f}s {total_mhs:.1f} MH/s ETA~{eta:.0f}s en2={cur_en2_hex}')
                last_log_time = now

            # Drain result queue
            try:
                while True:
                    r = out_q.get_nowait()
                    if r is None:
                        exhausted += 1
                    else:
                        found_nonce = r
                        break
            except Exception:
                pass

            if found_nonce is not None:
                break

            # Poll for new stratum messages
            try:
                chunk_data = sock.recv(4096).decode(errors='replace')
                if chunk_data:
                    buf += chunk_data
                while '\n' in buf:
                    line_str, buf = buf.split('\n', 1)
                    try:
                        msg = json.loads(line_str)
                        if msg.get('method') == 'mining.notify':
                            pn = msg['params']
                            new_job = {'id': pn[0], 'ph': pn[1], 'cb1': pn[2], 'cb2': pn[3],
                                       'br': pn[4], 'ver': pn[5], 'nb': pn[6], 'nt': pn[7]}
                            log(f'New job: id={new_job["id"]} ntime={new_job["nt"]}')
                            job = new_job
                            # Always restart on any new job. ckpool only keeps
                            # recent workbases (~120s); mining one job for 300s
                            # guarantees a stale rejection regardless of clean flag.
                            clean_job_arrived = True
                    except (json.JSONDecodeError, IndexError):
                        pass
            except socket.timeout:
                pass

        # Stop all workers
        stop_ev.set()
        for proc in procs:
            proc.join(timeout=3)
            if proc.is_alive():
                proc.kill()

        if found_nonce is not None:
            nonce_hex = f'{found_nonce:08x}'
            full_header = h64 + h12 + struct.pack('<I', found_nonce)
            computed_hash = sha256d(full_header)
            hash_int = int.from_bytes(computed_hash, 'little')
            log(f'Share found! nonce={nonce_hex} job={cur_job["id"]} en2={cur_en2_hex}')
            log(f'  header80: {full_header.hex()}')
            log(f'  sha256d:  {computed_hash.hex()}')
            log(f'  meets_diff1: {hash_int <= DIFF1} (hash={hash_int:#066x} diff1={DIFF1:#066x})')
            submit_id = msg_id
            send({'id': submit_id, 'method': 'mining.submit',
                  'params': [user, cur_job['id'], cur_en2_hex, cur_job['nt'], nonce_hex]})
            msg_id += 1
            # Read ckpool's response to learn the exact accept/reject reason
            sock.settimeout(3)
            resp_buf = ''
            try:
                deadline = time.time() + 3
                while time.time() < deadline:
                    chunk = sock.recv(4096).decode(errors='replace')
                    if not chunk:
                        break
                    resp_buf += chunk
            except socket.timeout:
                pass
            for rline in resp_buf.split('\n'):
                rline = rline.strip()
                if rline:
                    log(f'  ckpool→ {rline}')
                    try:
                        r = json.loads(rline)
                        if r.get('id') == submit_id:
                            if r.get('result') is True:
                                log('  ACCEPTED ✓')
                            else:
                                log(f'  REJECTED: {r.get("error")}')
                    except json.JSONDecodeError:
                        pass
                    buf += rline + '\n'
            time.sleep(0.5)
            en2_int += 1
        elif clean_job_arrived:
            log(f'Clean job arrived, restarting with job={job["id"]}')
            en2_int = 0
        else:
            # Full nonce space exhausted for this en2; try next
            en2_int += 1
            log(f'Nonce space exhausted for en2={cur_en2_hex}')


if __name__ == '__main__':
    main()
PYEOF
chmod +x "$STRATUM_MINER"

# ---- configuration ----------------------------------------------------------

BTC_DIR="$WORK_DIR/bitcoin"
BTC_RPC_PORT=18443          # regtest default
BTC_USER=smoketest
BTC_PASS=smokepass

KAMADO_PORT=19080           # avoid clashing with a running instance
STRATUM_PORT=19333          # ckpool stratum port for Phase 2

CKPOOL_LOG="$WORK_DIR/ckpool.log"   # kamado-api watches this; Phase 1 writes here
KAMADO_DB="$WORK_DIR/kamado.db"
CKPOOL_SOCK_DIR="$WORK_DIR/ckpool-sockets"

mkdir -p "$BTC_DIR" "$CKPOOL_SOCK_DIR"
touch "$CKPOOL_LOG"

# Kill any stale processes from a previous run that bypassed cleanup (e.g.
# killed with SIGKILL). Without this they hold the ports, the new processes
# fail to bind, but the readiness health-checks see the old processes
# and report "ready", causing very confusing test failures.
log "clearing test ports from any prior stale run…"
for _port in $BTC_RPC_PORT $KAMADO_PORT $STRATUM_PORT; do
    fuser -k -TERM "${_port}/tcp" 2>/dev/null || true
done
sleep 0.5   # give killed processes a moment to release their ports

# Shorthand for bitcoin-cli pointing at the regtest instance.
cli() {
    bitcoin-cli \
        -regtest \
        -datadir="$BTC_DIR" \
        -rpcport="$BTC_RPC_PORT" \
        -rpcuser="$BTC_USER" \
        -rpcpassword="$BTC_PASS" \
        "$@"
}

# ---- start bitcoind ---------------------------------------------------------

log "starting bitcoind regtest on port $BTC_RPC_PORT"
bitcoind \
    -regtest \
    -datadir="$BTC_DIR" \
    -rpcport="$BTC_RPC_PORT" \
    -rpcuser="$BTC_USER" \
    -rpcpassword="$BTC_PASS" \
    -rpcbind=127.0.0.1 \
    -rpcallowip=127.0.0.1 \
    -fallbackfee=0.0001 \
    -debug=validation \
    -nodaemon \
    >"$WORK_DIR/bitcoind.log" 2>&1 &
BITCOIN_PID=$!

log "waiting for bitcoind RPC…"
ready=false
for i in $(seq 1 30); do
    if cli getblockcount >/dev/null 2>&1; then ready=true; break; fi
    sleep 1
done
$ready || die "bitcoind did not become ready within 30s (see $WORK_DIR/bitcoind.log)"
kill -0 "$BITCOIN_PID" 2>/dev/null \
    || die "bitcoind (pid $BITCOIN_PID) is not running, another process may have answered on port $BTC_RPC_PORT"
log "bitcoind ready (pid $BITCOIN_PID)"

# Create a descriptor wallet and mine enough blocks for coinbase maturity.
log "setting up wallet and generating 101 maturity blocks"
cli createwallet smoke >/dev/null 2>&1 || cli loadwallet smoke >/dev/null 2>&1 || true
MINE_ADDR=$(cli getnewaddress)
cli generatetoaddress 101 "$MINE_ADDR" >/dev/null

# Mine the Phase 1 target block (it exists in bitcoind before we inject the log line).
BEFORE_HEIGHT=$(cli getblockcount)
cli generatetoaddress 1 "$MINE_ADDR" >/dev/null
P1_HEIGHT=$((BEFORE_HEIGHT + 1))
log "Phase 1 target block at height $P1_HEIGHT is live in bitcoind"

# ---- start kamado-api -------------------------------------------------------

log "starting kamado-api on port $KAMADO_PORT"
LISTEN_ADDR=":$KAMADO_PORT"                        \
BITCOIN_RPC_URL="http://127.0.0.1:$BTC_RPC_PORT"  \
BITCOIN_RPC_USER="$BTC_USER"                       \
BITCOIN_RPC_PASSWORD="$BTC_PASS"                   \
BITCOIN_ZMQ_BLOCK=""                               \
CKPOOL_SOCKDIR="$CKPOOL_SOCK_DIR"                  \
CKPOOL_LOGFILE="$CKPOOL_LOG"                       \
DB_PATH="$KAMADO_DB"                               \
POLL_INTERVAL=2s                                   \
    "$KAMADO_BIN" >"$WORK_DIR/kamado.log" 2>&1 &
KAMADO_PID=$!

log "waiting for kamado-api snapshot endpoint…"
ready=false
for i in $(seq 1 30); do
    if curl -sf "http://127.0.0.1:$KAMADO_PORT/api/snapshot" >/dev/null 2>&1; then
        ready=true; break
    fi
    kill -0 "$KAMADO_PID" 2>/dev/null || die "kamado-api exited unexpectedly (see $WORK_DIR/kamado.log)"
    sleep 1
done
$ready || die "kamado-api did not become ready within 30s (see $WORK_DIR/kamado.log)"
kill -0 "$KAMADO_PID" 2>/dev/null \
    || die "kamado-api (pid $KAMADO_PID) is not running, port $KAMADO_PORT may already be in use"
log "kamado-api ready (pid $KAMADO_PID)"

# ============================================================
# Phase 1: basic log injection
# ============================================================
log ""
log "=== Phase 1: log-injection path ==="

log "writing 'Solved and confirmed block $P1_HEIGHT' to ckpool log"
printf 'Solved and confirmed block %d\n' "$P1_HEIGHT" >> "$CKPOOL_LOG"

log "polling /api/blocks for height $P1_HEIGHT (up to 30s)…"
found=false
for i in $(seq 1 30); do
    RESP=$(curl -sf "http://127.0.0.1:$KAMADO_PORT/api/blocks" 2>/dev/null || echo "[]")
    if echo "$RESP" | jq -e "any(.[]; .height == $P1_HEIGHT)" >/dev/null 2>&1; then
        found=true; break
    fi
    sleep 1
done
if ! $found; then
    log "last /api/blocks response: $(curl -sf "http://127.0.0.1:$KAMADO_PORT/api/blocks" || echo "(unreachable)")"
    log "kamado-api log tail:"
    tail -20 "$WORK_DIR/kamado.log" | sed 's/^/  /'
    die "Phase 1: block $P1_HEIGHT did not appear in /api/blocks within 30s"
fi

P1_HASH=$(
    curl -sf "http://127.0.0.1:$KAMADO_PORT/api/blocks" \
    | jq -r "first(.[] | select(.height == $P1_HEIGHT) | .hash // \"\")"
)
[[ -n "$P1_HASH" && "$P1_HASH" != "null" ]] \
    || die "Phase 1: block $P1_HEIGHT appeared but hash enrichment failed"
[[ "$P1_HASH" =~ ^[0-9a-f]{64}$ ]] \
    || die "Phase 1: hash looks wrong: $P1_HASH"
P1_CANONICAL=$(cli getblockhash "$P1_HEIGHT")
[[ "$P1_HASH" == "$P1_CANONICAL" ]] \
    || die "Phase 1: hash mismatch: api=$P1_HASH bitcoind=$P1_CANONICAL"

log "Phase 1 PASS: block $P1_HEIGHT → hash $P1_HASH (matches bitcoind)"

# ============================================================
# Phase 2: full stratum path (requires ckpool)
# ============================================================
if ! $CKPOOL_AVAILABLE; then
    log ""
    log "=== Phase 2: SKIPPED (ckpool not available) ==="
    log "To run Phase 2 set CKPOOL_BIN=<path-to-ckpool-binary>"
    printf '[smoke] PASS: Phase 1 (log-injection path) verified\n'
    exit 0
fi

log ""
log "=== Phase 2: stratum path ($(basename "$CKPOOL_BIN")) ==="

P2_HEIGHT=$((P1_HEIGHT + 1))    # the block ckpool will mine via stratum
# Any valid address works; ckpool-solo uses it for the coinbase output.
POOL_ADDR=$(cli getnewaddress)

# ---- write ckpool.conf ------------------------------------------------------
# ckpool parses mindiff/startdiff as int64_t. Regtest network difficulty
# (≈4.65e-10) is clamped to 1 internally, so clients must produce a share
# with sdiff ≥ 1 for acceptance. Set both to 1 (the minimum meaningful
# integer value) so ckpool hands out diff-1 work. Expected solve time:
# ~2^32 hashes per process; at ~1.5 MH/s per Python worker × 8 workers
# ≈ 360s on average.
CKPOOL_CONF="$WORK_DIR/ckpool.conf"
cat > "$CKPOOL_CONF" << CONF_EOF
{
    "btcd": [
        {
            "url": "127.0.0.1:$BTC_RPC_PORT",
            "auth": "$BTC_USER",
            "pass": "$BTC_PASS",
            "notify": false
        }
    ],
    "btcaddress": "$POOL_ADDR",
    "btcsig": "/KamadoSmoke/",
    "blockpoll": 100,
    "update_interval": 30,
    "serverurl": ["0.0.0.0:$STRATUM_PORT"],
    "mindiff": 1,
    "startdiff": 1,
    "maxdiff": 0,
    "logdir": "$WORK_DIR"
}
CONF_EOF

# ---- start ckpool -----------------------------------------------------------

log "starting ckpool (stratum port $STRATUM_PORT, logdir $WORK_DIR)"
"$CKPOOL_BIN" --btcsolo --config "$CKPOOL_CONF" --sockdir "$CKPOOL_SOCK_DIR" \
    >"$WORK_DIR/ckpool-stderr.log" 2>&1 &
CKPOOL_PID=$!

log "waiting for ckpool stratum port $STRATUM_PORT…"
ready=false
for i in $(seq 1 30); do
    (exec 3<>/dev/tcp/127.0.0.1/"$STRATUM_PORT" && exec 3>&-) 2>/dev/null \
        && { ready=true; break; } || true
    kill -0 "$CKPOOL_PID" 2>/dev/null \
        || die "ckpool exited unexpectedly (see $WORK_DIR/ckpool-stderr.log)"
    sleep 1
done
$ready || die "ckpool stratum port did not open within 30s (see $WORK_DIR/ckpool-stderr.log)"
kill -0 "$CKPOOL_PID" 2>/dev/null \
    || die "ckpool (pid $CKPOOL_PID) is not running, check $WORK_DIR/ckpool-stderr.log"
log "ckpool ready (pid $CKPOOL_PID)"

# ---- run Python stratum miner in background ---------------------------------
# ckpool-solo requires the stratum username to be a valid Bitcoin address.
# The Python miner connects via stratum, receives diff-1 work, and mines real
# SHA256d hashes across multiple processes until one finds sdiff ≥ 1.

MINER_LOG="$WORK_DIR/miner.log"
log "launching Python stratum miner for block $P2_HEIGHT…"
# PYTHONWARNINGS suppresses the "leaked semaphore objects" noise from
# multiprocessing.resource_tracker when daemon workers are killed mid-run.
PYTHONWARNINGS=ignore python3 "$STRATUM_MINER" \
    "127.0.0.1" "$STRATUM_PORT" "$POOL_ADDR" "$MINER_LOG" \
    >>"$MINER_LOG" 2>&1 &
MINER_PID=$!

# ---- monitor mining progress until ckpool logs the block solve --------------

log "waiting up to 1800s for ckpool to log 'Solved and confirmed block $P2_HEIGHT'…"
MINE_START=$(date +%s)
last_report=0
solved=false
for _i in $(seq 1 600); do
    sleep 3

    if ! kill -0 "$MINER_PID" 2>/dev/null; then
        log "miner log (last 20 lines):"
        tail -20 "$MINER_LOG" | sed 's/^/  /'
        die "Phase 2: miner exited before block was solved"
    fi

    if grep -q "Solved and confirmed block $P2_HEIGHT" "$CKPOOL_LOG" 2>/dev/null; then
        solved=true
        break
    fi

    now=$(date +%s)
    if (( now - last_report >= 30 )); then
        last_report=$now
        elapsed=$(( now - MINE_START ))
        rate=$(grep -oE '[0-9]+(\.[0-9]+)? [kMG]H/s' "$MINER_LOG" 2>/dev/null | tail -1 || true)
        if [[ -n "$rate" ]]; then
            rate_val=${rate%% *}
            rate_unit=${rate##* }
            eta_s=$(awk -v v="$rate_val" -v u="${rate_unit:0:1}" -v e="$elapsed" \
                'BEGIN { m=(u=="k")?1000:(u=="M")?1e6:(u=="G")?1e9:1; r=4294967296/(v*m)-e; printf "%.0f", (r>0)?r:0 }')
            log "  mining: elapsed=${elapsed}s  rate=$rate  ETA~${eta_s}s"
        else
            log "  mining: elapsed=${elapsed}s  (hashrate not yet reported)"
        fi
    fi

    if (( $(date +%s) - MINE_START >= 1800)); then break; fi
done

kill "$MINER_PID" 2>/dev/null || true
MINER_PID=""

if ! $solved; then
    log "miner log (last 30 lines):"
    tail -30 "$MINER_LOG" | sed 's/^/  /'
    log "ckpool log (last 20 lines):"
    tail -20 "$CKPOOL_LOG" | sed 's/^/  /'
    log "ckpool stderr (last 10 lines):"
    tail -10 "$WORK_DIR/ckpool-stderr.log" | sed 's/^/  /'
    die "Phase 2: block $P2_HEIGHT not solved within 1800s"
fi

log "ckpool logged 'Solved and confirmed block $P2_HEIGHT' ✓"

# ---- wait for block to appear in /api/blocks --------------------------------

log "polling /api/blocks for height $P2_HEIGHT (up to 30s)…"
found=false
for i in $(seq 1 30); do
    RESP=$(curl -sf "http://127.0.0.1:$KAMADO_PORT/api/blocks" 2>/dev/null || echo "[]")
    if echo "$RESP" | jq -e "any(.[]; .height == $P2_HEIGHT)" >/dev/null 2>&1; then
        found=true; break
    fi
    sleep 1
done
if ! $found; then
    log "last /api/blocks response: $(curl -sf "http://127.0.0.1:$KAMADO_PORT/api/blocks" || echo "(unreachable)")"
    log "kamado-api log tail:"
    tail -20 "$WORK_DIR/kamado.log" | sed 's/^/  /'
    die "Phase 2: block $P2_HEIGHT did not appear in /api/blocks within 30s"
fi

# ---- cross-check hash against bitcoind --------------------------------------

P2_HASH=$(
    curl -sf "http://127.0.0.1:$KAMADO_PORT/api/blocks" \
    | jq -r "first(.[] | select(.height == $P2_HEIGHT) | .hash // \"\")"
)
[[ -n "$P2_HASH" && "$P2_HASH" != "null" ]] \
    || die "Phase 2: block $P2_HEIGHT in /api/blocks but hash enrichment failed"
[[ "$P2_HASH" =~ ^[0-9a-f]{64}$ ]] \
    || die "Phase 2: hash looks wrong: $P2_HASH"
P2_CANONICAL=$(cli getblockhash "$P2_HEIGHT")
[[ "$P2_HASH" == "$P2_CANONICAL" ]] \
    || die "Phase 2: hash mismatch: api=$P2_HASH bitcoind=$P2_CANONICAL"

# ---- verbose coinbase validation --------------------------------------------
# Decode the mined block's coinbase transaction and verify the scriptSig
# contains "kamado" (our patched branding) and the btcsig tag, and that
# bitcoind accepted the coinbase as consensus-valid.

log ""
log "=== Coinbase validation ==="

P2_BLOCK_HEX=$(cli getblock "$P2_HASH" 0)
# The coinbase is the first transaction in the block. getblock verbosity=2
# gives us the decoded transaction directly.
P2_BLOCK_JSON=$(cli getblock "$P2_HASH" 2)

COINBASE_TXID=$(echo "$P2_BLOCK_JSON" | jq -r '.tx[0].txid')
COINBASE_SCRIPTSIG_HEX=$(echo "$P2_BLOCK_JSON" | jq -r '.tx[0].vin[0].coinbase')
COINBASE_SCRIPTSIG_SIZE=${#COINBASE_SCRIPTSIG_HEX}
COINBASE_SCRIPTSIG_BYTES=$((COINBASE_SCRIPTSIG_SIZE / 2))
COINBASE_SCRIPTSIG_ASM=$(echo "$P2_BLOCK_JSON" | jq -r '.tx[0].vin[0].coinbase' | xxd -r -p | strings -n 3 | tr '\n' ' ')

log "  coinbase txid:       $COINBASE_TXID"
log "  scriptSig hex:       $COINBASE_SCRIPTSIG_HEX"
log "  scriptSig size:      $COINBASE_SCRIPTSIG_BYTES bytes (max 100)"
log "  readable strings:    $COINBASE_SCRIPTSIG_ASM"

# Verify scriptSig is within consensus limit
(( COINBASE_SCRIPTSIG_BYTES <= 100 )) \
    || die "Coinbase scriptSig exceeds 100-byte consensus limit ($COINBASE_SCRIPTSIG_BYTES bytes)"
(( COINBASE_SCRIPTSIG_BYTES >= 2 )) \
    || die "Coinbase scriptSig below 2-byte consensus minimum ($COINBASE_SCRIPTSIG_BYTES bytes)"

# Verify our branding is present (kamado in hex = 6b616d61646f)
if echo "$COINBASE_SCRIPTSIG_HEX" | grep -qi "6b616d61646f"; then
    log "  branding check:     'kamado' found in scriptSig ✓"
else
    # Fall back to checking for 'ckpool' (pre-patch binary)
    if echo "$COINBASE_SCRIPTSIG_HEX" | grep -qi "636b706f6f6c"; then
        log "  branding check:     'ckpool' found (pre-patch binary, expected if patch not applied)"
    else
        log "  WARNING: neither 'kamado' nor 'ckpool' found in scriptSig"
    fi
fi

# Verify btcsig tag is present
BTCSIG_HEX=$(echo -n "/KamadoSmoke/" | xxd -p)
if echo "$COINBASE_SCRIPTSIG_HEX" | grep -qi "$BTCSIG_HEX"; then
    log "  btcsig check:       '/KamadoSmoke/' found in scriptSig ✓"
else
    log "  WARNING: btcsig '/KamadoSmoke/' not found in scriptSig"
fi

# The ultimate proof: bitcoind accepted this block into its chain.
# getblock succeeds and confirmations > 0 means full consensus validation passed.
P2_CONFIRMATIONS=$(echo "$P2_BLOCK_JSON" | jq -r '.confirmations')
log "  confirmations:      $P2_CONFIRMATIONS (block accepted by bitcoind consensus) ✓"

log "Phase 2 PASS: block $P2_HEIGHT → hash $P2_HASH (matches bitcoind)"

# ---- bitcoind validation debug log ------------------------------------------
# Show bitcoind's internal validation messages for the mined block.
# Requires -debug=validation flag (added to bitcoind startup above).

BTC_DEBUG_LOG="$BTC_DIR/regtest/debug.log"
if [[ -f "$BTC_DEBUG_LOG" ]]; then
    log ""
    log "=== bitcoind validation log (block $P2_HASH) ==="
    # Show only lines mentioning our specific block hash (filters out generatetoaddress noise)
    grep -F "$P2_HASH" \
        "$BTC_DEBUG_LOG" | while IFS= read -r line; do
        log "  $line"
    done
    log "=== end validation log ==="
else
    log "  WARNING: bitcoind debug.log not found at $BTC_DEBUG_LOG"
fi

# ---- all done ---------------------------------------------------------------

printf '[smoke] PASS: Phase 1 (log-injection) + Phase 2 (stratum → submitblock) both verified\n'
