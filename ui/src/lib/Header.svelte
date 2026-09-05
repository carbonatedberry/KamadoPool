<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { selectAccelerator } from "../stores/selection.svelte";

  const statusClass = $derived.by(() => {
    if (snap.status === "open") return "good";
    if (snap.status === "connecting") return "warn";
    return "bad";
  });

  const statusLabel = $derived.by(() => {
    switch (snap.status) {
      case "open": return "live";
      case "connecting": return "connecting";
      default: return "offline";
    }
  });
</script>

<header class="bar">
  <div class="brand">
    <span class="logo">&#x1F525;</span>
    <span class="name">Kamado Pool</span>
    <button
      type="button"
      class="accel-btn"
      onclick={selectAccelerator}
      title="Transaction Accelerator"
    >
      <svg class="rocket-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/>
        <path d="M12 15l-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/>
        <path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0"/>
        <path d="M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/>
      </svg>
    </button>
  </div>
  <div class="meta" title="WebSocket status: {statusLabel}">
    <span class="ws-dot {statusClass}" aria-hidden="true"></span>
    <span class="ws-label">{statusLabel}</span>
  </div>
</header>

<style>
  .bar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0.25rem 0 1rem;
    border-bottom: 1px solid var(--border);
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    font-size: 1.25rem;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .logo {
    font-size: 1.4rem;
  }
  .accel-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.45em;
    cursor: pointer;
    color: var(--fg-dim);
    transition: color 0.2s, border-color 0.2s, box-shadow 0.2s, transform 0.2s;
    animation: jiggle 10s ease-in-out infinite;
  }
  .accel-btn:hover {
    color: var(--accent);
    border-color: var(--accent);
    box-shadow: 0 0 12px rgba(255, 122, 58, 0.5);
    transform: translateY(-2px) scale(1.05);
    animation: none;
  }
  @keyframes jiggle {
    0%, 94%, 100% { transform: rotate(0deg) scale(1); }
    95% { transform: rotate(-12deg) scale(1.1); }
    96.5% { transform: rotate(10deg) scale(1.1); }
    97.5% { transform: rotate(-8deg) scale(1.05); }
    98.5% { transform: rotate(6deg) scale(1.05); }
    99.5% { transform: rotate(-3deg) scale(1); }
  }
  .rocket-icon {
    width: 1.4em;
    height: 1.4em;
  }
  .meta {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.9rem;
    color: var(--fg-dim);
  }
  .ws-dot {
    width: 10px;
    height: 10px;
    border-radius: 999px;
    display: inline-block;
    background: var(--fg-dim);
  }
  .ws-dot.good {
    background: var(--good);
    box-shadow: 0 0 8px var(--good);
  }
  .ws-dot.warn {
    background: var(--warn);
  }
  .ws-dot.bad {
    background: var(--bad);
  }
  .ws-label {
    text-transform: lowercase;
  }

  @media (max-width: 480px) {
    .brand {
      font-size: 1.05rem;
      gap: 0.4rem;
    }
    .name {
      display: none;
    }
    .meta {
      font-size: 0.8rem;
    }
  }
</style>
