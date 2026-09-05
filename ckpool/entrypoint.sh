#!/bin/sh
# ----------------------------------------------------------------------------
# ckpool-solo entrypoint for Kamado Pool
#
# Reads configuration from environment variables, renders the ckpool.conf
# from the template, and execs ckpool-solo.
#
# Required env:
#   POOL_BTCADDRESS       - Bitcoin address that receives block rewards
#   BITCOIN_RPC_HOST      - e.g. bitcoind, 127.0.0.1
#   BITCOIN_RPC_PORT      - e.g. 8332 (mainnet) / 48332 (testnet4)
#   BITCOIN_RPC_USER      - RPC username
#   BITCOIN_RPC_PASSWORD  - RPC password
#
# Optional env (with defaults):
#   STRATUM_PORT=3333
#   POOL_BTCSIG="/Kamado Pool/"
#   BLOCKPOLL_MS=100
#   UPDATE_INTERVAL_S=30
#   MINDIFF=1
#   STARTDIFF=42
#   MAXDIFF=0              (0 = unlimited)
#   DROPIDLE=0             (seconds; 0 = never drop idle clients)
#   BITCOIN_NOTIFY=true    (use bitcoind blocknotify/walletnotify)
#   ZMQ_BLOCK=""           (e.g. tcp://bitcoind:28332)
#   LOGDIR=/var/log/ckpool
#   SOCKET_DIR=/run/ckpool
#   SHARE_LOG=1            (1 = enable -L share logging)
# ----------------------------------------------------------------------------
set -eu

: "${POOL_BTCADDRESS:?POOL_BTCADDRESS is required}"
: "${BITCOIN_RPC_HOST:?BITCOIN_RPC_HOST is required}"
: "${BITCOIN_RPC_PORT:?BITCOIN_RPC_PORT is required}"
: "${BITCOIN_RPC_USER:?BITCOIN_RPC_USER is required}"
: "${BITCOIN_RPC_PASSWORD:?BITCOIN_RPC_PASSWORD is required}"

STRATUM_PORT="${STRATUM_PORT:-3333}"
POOL_BTCSIG="${POOL_BTCSIG:-/Kamado Pool/}"
BLOCKPOLL_MS="${BLOCKPOLL_MS:-100}"
UPDATE_INTERVAL_S="${UPDATE_INTERVAL_S:-30}"
MINDIFF="${MINDIFF:-1}"
STARTDIFF="${STARTDIFF:-42}"
MAXDIFF="${MAXDIFF:-0}"
DROPIDLE="${DROPIDLE:-0}"
BITCOIN_NOTIFY="${BITCOIN_NOTIFY:-true}"
ZMQ_BLOCK="${ZMQ_BLOCK:-}"
LOGDIR="${LOGDIR:-/var/log/ckpool}"
SOCKET_DIR="${SOCKET_DIR:-/run/ckpool}"
SHARE_LOG="${SHARE_LOG:-1}"
CKPOOL_LOGLEVEL="${CKPOOL_LOGLEVEL:-6}"
# ckpool's second, loopback-only stratum bind. Nothing terminates TLS in the
# dev compose stack, but the bind must still resolve: ckpool passes the port
# straight to getaddrinfo() and a non-numeric service string is fatal
# (connector.c logs "Failed to extract resolved url" and exit(1)s).
TLS_INTERNAL_PORT="${TLS_INTERNAL_PORT:-3437}"

mkdir -p "$LOGDIR" "$SOCKET_DIR"

TEMPLATE=/etc/ckpool/ckpool.conf.template
CONF=/etc/ckpool/ckpool.conf

# Simple placeholder substitution (no envsubst dependency).
# We intentionally don't use eval/printf tricks, sed is safe and predictable.
sed \
    -e "s|\${BITCOIN_RPC_HOST}|${BITCOIN_RPC_HOST}|g" \
    -e "s|\${BITCOIN_RPC_PORT}|${BITCOIN_RPC_PORT}|g" \
    -e "s|\${BITCOIN_RPC_USER}|${BITCOIN_RPC_USER}|g" \
    -e "s|\${BITCOIN_RPC_PASSWORD}|${BITCOIN_RPC_PASSWORD}|g" \
    -e "s|\${BITCOIN_NOTIFY}|${BITCOIN_NOTIFY}|g" \
    -e "s|\${POOL_BTCADDRESS}|${POOL_BTCADDRESS}|g" \
    -e "s|\${POOL_BTCSIG}|${POOL_BTCSIG}|g" \
    -e "s|\${BLOCKPOLL_MS}|${BLOCKPOLL_MS}|g" \
    -e "s|\${UPDATE_INTERVAL_S}|${UPDATE_INTERVAL_S}|g" \
    -e "s|\${STRATUM_PORT}|${STRATUM_PORT}|g" \
    -e "s|\${TLS_INTERNAL_PORT}|${TLS_INTERNAL_PORT}|g" \
    -e "s|\${MINDIFF}|${MINDIFF}|g" \
    -e "s|\${STARTDIFF}|${STARTDIFF}|g" \
    -e "s|\${MAXDIFF}|${MAXDIFF}|g" \
    -e "s|\${DROPIDLE}|${DROPIDLE}|g" \
    -e "s|\${LOGDIR}|${LOGDIR}|g" \
    -e "s|\${ZMQ_BLOCK}|${ZMQ_BLOCK}|g" \
    "$TEMPLATE" > "$CONF"

# If ZMQ is not configured, strip the zmqblock line entirely.
# The template keeps zmqblock as a non-final key with a trailing comma,
# so removing the whole line leaves valid JSON.
if [ -z "$ZMQ_BLOCK" ]; then
    sed -i '/"zmqblock":/d' "$CONF"
fi

echo "[kamado] rendered ckpool.conf:"
sed "s|\"pass\": \".*\"|\"pass\": \"<redacted>\"|" "$CONF"
echo

# Build command line.
# -l 6 = LOG_INFO: enables per-share Accepted/Rejected log lines
# that the kamado-api log tailer uses for share statistics.
CMD="/usr/local/bin/ckpool --btcsolo --config $CONF --sockdir $SOCKET_DIR -l $CKPOOL_LOGLEVEL"
if [ "$SHARE_LOG" = "1" ]; then
    CMD="$CMD --log-shares"
fi

echo "[kamado] exec: $CMD"
exec $CMD
