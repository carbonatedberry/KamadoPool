// Mirrors api/internal/state.Snapshot. Keep field names aligned; the
// Go side is authoritative.

export type PoolStats = {
  start: number;
  update: number;
  workers: number;
  users: number;
  disconnected: number;
  shares: number;
  accepted: number;
  rejected: number;
  rejectcount: number;
  dsps1: number;
  dsps5: number;
  dsps15: number;
  dsps60: number;
  dsps360: number;
  dsps1440: number;
  dsps10080: number;
};

export type User = {
  user: string;
  id: number;
  workers: number;
  bestdiff: number;
  bestever: number;
  dsps1: number;
  dsps5: number;
  dsps60: number;
  dsps1440: number;
  dsps10080: number;
  lastshare: number;
};

export type Worker = {
  user: string;
  worker: string;
  id: number;
  dsps1: number;
  dsps5: number;
  dsps60: number;
  dsps1440: number;
  lastshare: number;
  bestdiff: number;
  bestever: number;
  shares: number;
  mindiff: number;
  idle: boolean;
};

export type StratumClient = {
  id: number;
  enonce1: string;
  diff: number;
  dsps1: number;
  dsps5: number;
  dsps60: number;
  dsps1440: number;
  lastshare: number;
  starttime: number;
  // Source IP, NOT the BTC payout address. ckpool sets this from
  // inet_ntop(client_addr) in connector.c, see stratum_add_instance.
  // The BTC address comes from `workername` (or the joined Worker.user).
  address: string;
  subscribed: boolean;
  authorised: boolean;
  idle: boolean;
  useragent: string;
  workername: string;
  userid: number;
  // Index into ckpool's serverurl[] array; identifies which stratum
  // bind the client connected on. What each index *means* depends on
  // how the deployment rendered ckpool.conf, so resolve it through
  // Snapshot.stratum_servers rather than hardcoding, see
  // connectionOf() in format.ts.
  server: number;
  bestdiff: number;
};

export type BlockchainInfo = {
  chain: string;
  blocks: number;
  headers: number;
  bestblockhash: string;
  difficulty: number;
  mediantime: number;
  verificationprogress: number;
  initialblockdownload: boolean;
};

export type BlockRecord = {
  height: number;
  hash?: string;
  reward_btc?: number;
  found_at: string; // RFC 3339
  source: string;
  share_diff?: number;
  // Set when the periodic chain reconciler finds the recorded hash no
  // longer matches the canonical block at this height (network reorged
  // us out). UI renders these strikethrough.
  orphaned_at?: string; // RFC 3339, omitted when zero
  // Bitcoin network the block was mined on ("main", "test", "signet").
  // Absent for legacy rows recorded before this field was added.
  chain?: string;
  // Workername (address.worker) of the miner who found the block.
  miner?: string;
};

export type HashratePoint = {
  t: number; // unix seconds
  v: number; // H/s
};

// PoW reproduction data for the best share seen since ckpool patch 0008
// went live (logged by ckpool, ingested from the log by kamado-api).
// `header` is the raw 80-byte block header in hashing byte order,
// double-SHA256 it and reverse the digest to get `hash`. `coinbase` plus
// `merklebranches` reproduce the merkle root committed in the header.
export type BestSharePow = {
  sdiff: number;
  netdiff: number;
  height: number;
  hash: string; // display-order (big-endian) block header hash
  header: string; // 160 hex chars
  coinbase: string; // full serialized coinbase tx, hex
  cb1len: number; // bytes preceding extranonce1 within the coinbase
  merklebranches: string[] | null;
  enonce1: string;
  nonce2: string;
  workername: string;
  seen_at: string; // RFC 3339, when kamado-api ingested the record
};

// One entry per ckpool serverurl[] bind, declared by the deployment via
// the STRATUM_SERVERS env. `kind` is the stable tag the UI switches on;
// `label` is free text shown on hover.
export type StratumServerKind = "plain" | "tls-local" | "tls-public";

export type StratumServer = {
  kind: StratumServerKind;
  label: string;
};

export type Snapshot = {
  generated_at: string;
  pool: PoolStats | null;
  uptime_seconds: number;
  users: User[] | null;
  workers: Worker[] | null;
  clients: StratumClient[] | null;
  hashrate_hs_1m: number;
  hashrate_hs_5m: number;
  hashrate_hs_1h: number;
  hashrate_hs_24h: number;
  best_diff: number;
  best_share_hash?: string;
  best_share_net_diff?: number;
  // Full header/coinbase/merkle-branch record for the best share seen
  // since ckpool patch 0008 was deployed. May describe a lower-diff
  // share than best_diff when the all-time best predates the patch,
  // compare hashes and disclose.
  best_share_pow?: BestSharePow;
  acked_best_diff: number;
  cumulative_shares: number;
  next_block_reward_btc: number;
  next_difficulty_percent: number;
  // Observed mean seconds per block so far this epoch, measured between
  // block timestamps. Absent before the first block of an epoch.
  epoch_avg_block_seconds?: number;
  chain: BlockchainInfo | null;
  network_hashrate_hs: number;
  recent_blocks?: BlockRecord[];
  hashrate_history?: HashratePoint[];
  // Optional override for explorer links. Empty/undefined means the
  // UI falls back to its mempool.space defaults.
  mempool_base_url?: string;
  // Describes ckpool's serverurl[] binds: entry N says what a client
  // with `server === N` is connected over. Undefined when the
  // deployment didn't declare them, see connectionOf() in format.ts.
  stratum_servers?: StratumServer[];
  // Submission attempt tracking, count of "Possible block solve"
  // log lines vs "Solved and confirmed" lines. A growing gap means
  // submissions are being rejected by bitcoind.
  block_submit_attempts: number;
  block_submits_confirmed: number;
  // ZMQ subscriber freshness diagnostics.
  zmq_enabled: boolean;
  has_last_zmq_event: boolean;
  last_zmq_event_age?: number; // seconds
  tip_changed_age: number;     // seconds since tip height last changed
  peer_count: number;
  peers_in: number;
  peers_out: number;
  // Share counters (raw, 1 submission = 1 share).
  session_accepted: number;
  session_rejected: number;
  alltime_accepted: number;
  alltime_rejected: number;

  // Share statistics: rejection reasons and difficulty distribution.
  reject_reasons_session?: Record<string, number>;
  reject_reasons_alltime?: Record<string, number>;
  // Difficulty distribution buckets: [<1M, 1M-100M, 100M-1G, 1G-100G, 100G-1T, >=1T]
  diff_dist_session: number[];
  diff_dist_alltime: number[];
  avg_diff_session: number;
  avg_diff_alltime: number;

  // Block update latency diagnostics (ZMQ → mining.notify).
  latency_count: number;
  latency_avg_ms: number;
  latency_last_ms: number;
  stale_work_hashes: number;

  ckpool_ok: boolean;
  bitcoin_ok: boolean;
  last_error?: string;
};
