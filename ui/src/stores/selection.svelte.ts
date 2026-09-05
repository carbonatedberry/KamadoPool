// Simple hash-based routing for detail pages. The URL hash is the
// source of truth:
//   #/                    -> dashboard
//   #/user/<address>      -> per-user page
//   #/worker/<workername> -> per-worker page
//   #/accelerator         -> transaction accelerator
//
// selectUser / selectWorker / clearSelection just mutate the hash;
// a hashchange listener reads it back into reactive state so any
// component in the tree re-renders when the route changes.

const USER_PREFIX = "#/user/";
const WORKER_PREFIX = "#/worker/";
const ACCELERATOR_HASH = "#/accelerator";
const STATS_HASH = "#/stats";
const BESTSHARE_HASH = "#/bestshare";
const BESTSHARE_POW_HASH = "#/bestshare/pow";

type Selection = { user: string | null; worker: string | null; page: string | null };

export const selection = $state<Selection>(readHash());

function readHash(): Selection {
  if (typeof window === "undefined") return { user: null, worker: null, page: null };
  const h = window.location.hash;
  if (h === ACCELERATOR_HASH) {
    return { user: null, worker: null, page: "accelerator" };
  }
  if (h === STATS_HASH) {
    return { user: null, worker: null, page: "stats" };
  }
  if (h === BESTSHARE_POW_HASH) {
    return { user: null, worker: null, page: "bestshare-pow" };
  }
  if (h === BESTSHARE_HASH) {
    return { user: null, worker: null, page: "bestshare" };
  }
  if (h.startsWith(WORKER_PREFIX)) {
    const w = decodeURIComponent(h.slice(WORKER_PREFIX.length));
    return { user: null, worker: w || null, page: null };
  }
  if (h.startsWith(USER_PREFIX)) {
    const u = decodeURIComponent(h.slice(USER_PREFIX.length));
    return { user: u || null, worker: null, page: null };
  }
  return { user: null, worker: null, page: null };
}

function syncFromHash(): void {
  const parsed = readHash();
  selection.user = parsed.user;
  selection.worker = parsed.worker;
  selection.page = parsed.page;
}

if (typeof window !== "undefined") {
  window.addEventListener("hashchange", syncFromHash);
}

export function selectUser(address: string): void {
  window.location.hash = USER_PREFIX + encodeURIComponent(address);
}

export function selectWorker(workername: string): void {
  window.location.hash = WORKER_PREFIX + encodeURIComponent(workername);
}

export function selectAccelerator(): void {
  window.location.hash = ACCELERATOR_HASH;
}

export function selectStats(): void {
  window.location.hash = STATS_HASH;
}

export function selectBestShare(): void {
  window.location.hash = BESTSHARE_HASH;
}

export function selectBestSharePow(): void {
  window.location.hash = BESTSHARE_POW_HASH;
}

export function clearSelection(): void {
  window.location.hash = "";
}
