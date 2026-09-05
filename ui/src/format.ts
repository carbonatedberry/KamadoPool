// Formatters for hashrate, share difficulty, time, and miner hardware
// detection from stratum user-agent strings. Pure functions, no DOM.

import type { StratumServer } from "./types";

const HASHRATE_UNITS = ["H/s", "kH/s", "MH/s", "GH/s", "TH/s", "PH/s", "EH/s"];

export function formatHashrate(hs: number): string {
  if (!hs || hs <= 0 || !isFinite(hs)) return "0 H/s";
  let i = 0;
  let v = hs;
  while (v >= 1000 && i < HASHRATE_UNITS.length - 1) {
    v /= 1000;
    i++;
  }
  const digits = v >= 100 ? 0 : v >= 10 ? 1 : 2;
  return `${v.toFixed(digits)} ${HASHRATE_UNITS[i]}`;
}

// Difficulty is a share-diff value, scale with k/M/G/T suffixes.
export function formatDifficulty(d: number): string {
  if (!d || d <= 0 || !isFinite(d)) return "0";
  const units = ["", "k", "M", "G", "T", "P"];
  let i = 0;
  let v = d;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  const digits = v >= 100 ? 1 : 2;
  return `${v.toFixed(digits)}${units[i]}`;
}

// Cumulative hashes, scale up to ZH / YH since pools accumulate fast.
// Input is plain hashes (a diff-1 share is 2^32 hashes).
export function formatWork(hashes: number): string {
  if (!hashes || hashes <= 0 || !isFinite(hashes)) return "0 H";
  const units = ["H", "kH", "MH", "GH", "TH", "PH", "EH", "ZH", "YH"];
  let i = 0;
  let v = hashes;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v.toFixed(2)} ${units[i]}`;
}

export function formatUptime(seconds: number): string {
  if (!seconds || seconds < 0) return "-";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function formatAgo(unixSeconds: number): string {
  if (!unixSeconds) return "never";
  const ageSec = Math.max(0, Date.now() / 1000 - unixSeconds);
  if (ageSec < 60) return `${Math.floor(ageSec)}s ago`;
  if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
  if (ageSec < 86400) return `${Math.floor(ageSec / 3600)}h ago`;
  return `${Math.floor(ageSec / 86400)}d ago`;
}

// Star = open-source HARDWARE *and* SOFTWARE, matching public-pool's
// classification at https://github.com/benjamin-wilson/public-pool-ui
// (user-agent-link.component.html). Closed hardware running open
// firmware (Braiins OS on Antminer) doesn't qualify, and neither do
// generic CPU/cgminer agents that don't tell us what hardware
// they're running on.
export function isOpenSource(ua: string): boolean {
  if (!ua) return false;
  const s = ua.toLowerCase();
  return /bitaxe|nerdminer|nerdnos|nerdaxe|nerdoctaxe|nerdeko|nerdqaxe|piaxe|qaxe|0xaxe|leafminer/.test(s);
}

// Extract the BTC payout address from a stratum username. Stratum
// usernames in solo mode are "<btc-address>" or "<btc-address>.label".
// ckpool stores this verbatim in the worker name.
export function btcAddressOf(workername: string): string {
  if (!workername) return "";
  const dot = workername.indexOf(".");
  return dot < 0 ? workername : workername.slice(0, dot);
}

// Build the explorer base URL for the active network. If the user
// configured a custom mempool instance via StartOS, we trust that
// verbatim and don't append a /testnet4 / /signet path, the user's
// instance presumably runs on a single network already. Otherwise
// fall back to mempool.space with the appropriate network prefix.
export function explorerBaseFor(
  chain: string | undefined,
  customBaseUrl: string | undefined,
): string {
  if (customBaseUrl && customBaseUrl.length > 0) {
    return customBaseUrl.replace(/\/+$/, "");
  }
  if (chain === "test" || chain === "testnet4") return "https://mempool.space/testnet4";
  if (chain === "signet") return "https://mempool.space/signet";
  return "https://mempool.space";
}

// Resolve how a miner is connected from its ckpool serverurl[] index.
//
// The deployment declares the bind array via STRATUM_SERVERS (the StartOS
// wrapper renders one entry per bind it configures). When it hasn't, the
// dev compose stack, or an older wrapper, fall back to the historical
// layout, where index 0 is the plaintext bind and anything else is the
// loopback bind that stunnel forwards TLS traffic to.
export function connectionOf(
  serverIndex: number | undefined,
  servers: StratumServer[] | undefined,
): { tls: boolean; label: string } {
  const declared = servers?.[serverIndex ?? 0];
  if (declared) {
    return { tls: declared.kind !== "plain", label: declared.label };
  }
  return serverIndex
    ? { tls: true, label: "Encrypted connection via TLS" }
    : { tls: false, label: "Unencrypted connection" };
}

// Detect common Bitcoin mining hardware from the stratum useragent
// string. This is a best-effort heuristic; unknown agents fall back
// to a stripped version of the raw string. Order matters, check
// more specific tokens before generic ones.
export function detectHardware(ua: string): string {
  if (!ua) return "unknown";
  const s = ua.toLowerCase();
  const rules: Array<[RegExp, string]> = [
    // Bitaxe and Nerd-family open-source ASICs. Order matters, check
    // narrower variants before the broader Nerd* / Bitaxe roots.
    [/bitaxehex/, "Bitaxe Hex"],
    [/bitaxe/, "Bitaxe"],
    [/nerdqaxe\+\+/, "NerdQAxe++"],
    [/nerdqaxe/, "NerdQAxe+"],
    [/nerdaxegamma/, "NerdAxeGamma"],
    [/nerdaxe/, "NerdAxe"],
    [/nerdoctaxe/, "NerdOCTAXE"],
    [/nerdeko/, "NerdEKO"],
    [/nerdnos/, "NerdNOS"],
    [/nerdminer/, "NerdMiner"],
    [/0xaxe/, "0xAxe"],
    [/qaxe\+/, "QAxe+"],
    [/qaxe/, "QAxe"],
    [/piaxe/, "PiAxe"],
    [/leafminer/, "LeafMiner"],
    [/antminer/, "Antminer"],
    [/whatsminer/, "Whatsminer"],
    [/avalon/, "Avalon"],
    [/cgminer/, "cgminer"],
    [/bfgminer/, "bfgminer"],
    [/bmminer/, "BMMiner"],
    [/ckminer/, "ckminer"],
    [/cpuminer/, "cpuminer"],
    [/braiins/, "Braiins OS"],
    [/termux/, "termux-miner"],
    [/micro[- ]?bt/, "MicroBT"],
    [/esp32/, "ESP32"],
    [/s9/, "Antminer S9"],
  ];
  for (const [re, label] of rules) {
    if (re.test(s)) return label;
  }
  // Fall back to the first whitespace-free token, truncated.
  const token = ua.split(/\s+/)[0] ?? ua;
  return token.length > 24 ? token.slice(0, 24) + "…" : token;
}

// Rough expected time to find a block, in seconds, given pool and
// network hashrate. Returns Infinity (displayed as "∞") if the pool
// isn't mining.
export function expectedBlockSeconds(
  poolHs: number,
  networkHs: number,
  networkDifficulty: number,
): number {
  if (!poolHs || poolHs <= 0) return Infinity;
  // Expected hashes per block = difficulty * 2^32.
  const hashesPerBlock = networkDifficulty * 2 ** 32;
  return hashesPerBlock / poolHs;
}

export function formatDuration(seconds: number): string {
  if (!isFinite(seconds)) return "∞";
  if (seconds < 60) return `${Math.round(seconds)} seconds`;
  if (seconds < 3600) {
    const m = Math.round(seconds / 60);
    return `${m} minute${m === 1 ? "" : "s"}`;
  }
  if (seconds < 86400) {
    const h = seconds / 3600;
    const v = h >= 10 ? Math.round(h) : +h.toFixed(1);
    return `${v} hour${v === 1 ? "" : "s"}`;
  }
  if (seconds < 31536000) {
    const d = seconds / 86400;
    const v = d >= 10 ? Math.round(d) : +d.toFixed(1);
    return `${v} day${v === 1 ? "" : "s"}`;
  }
  const y = seconds / 31536000;
  const v = +y.toFixed(1);
  return `${v} year${v === 1 ? "" : "s"}`;
}
