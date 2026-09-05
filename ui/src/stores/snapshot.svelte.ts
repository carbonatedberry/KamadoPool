// Global snapshot store. Holds the latest Snapshot from kamado-api.
// Uses Svelte 5 runes ($state) so any component that imports `snap`
// re-renders automatically.
//
// Lifecycle:
//   - connect() fetches the initial snapshot via REST so the UI has
//     data to render before the socket is open.
//   - then opens /api/ws and overwrites the state on every frame.
//   - on close, backs off and reconnects. No jitter needed for now.

import type { Snapshot } from "../types";

type Status = "connecting" | "open" | "closed";

export const snap = $state<{
  data: Snapshot | null;
  status: Status;
  error: string | null;
}>({
  data: null,
  status: "connecting",
  error: null,
});

let socket: WebSocket | null = null;
let retryMs = 1000;

export async function connect(): Promise<void> {
  // Initial REST fetch, populates the first paint and tells us if
  // the API is reachable at all before we commit to a WebSocket.
  try {
    const res = await fetch("/api/snapshot", { cache: "no-store" });
    if (res.ok) {
      snap.data = (await res.json()) as Snapshot;
      snap.error = null;
    } else {
      snap.error = `snapshot HTTP ${res.status}`;
    }
  } catch (err) {
    snap.error = `snapshot fetch failed: ${err}`;
  }
  openSocket();
}

function openSocket(): void {
  snap.status = "connecting";
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${proto}//${location.host}/api/ws`;
  socket = new WebSocket(url);

  socket.addEventListener("open", () => {
    snap.status = "open";
    snap.error = null;
    retryMs = 1000;
  });

  socket.addEventListener("message", (ev) => {
    try {
      snap.data = JSON.parse(ev.data as string) as Snapshot;
    } catch (err) {
      console.error("snapshot parse failed", err);
    }
  });

  socket.addEventListener("close", () => {
    snap.status = "closed";
    socket = null;
    setTimeout(openSocket, retryMs);
    retryMs = Math.min(retryMs * 2, 15000);
  });

  socket.addEventListener("error", () => {
    snap.error = "websocket error";
  });
}
