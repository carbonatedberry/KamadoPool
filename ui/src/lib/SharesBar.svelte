<script lang="ts">
  import { untrack } from "svelte";
  import { snap } from "../stores/snapshot.svelte";
  import { selectStats } from "../stores/selection.svelte";

  const data = $derived(snap.data!);

  const sessionAcc = $derived(data.session_accepted ?? 0);
  const sessionRej = $derived(data.session_rejected ?? 0);
  const allAcc = $derived(data.alltime_accepted ?? 0);
  const allRej = $derived(data.alltime_rejected ?? 0);

  const sessionTotal = $derived(sessionAcc + sessionRej);
  const sessionRejectPct = $derived(
    sessionTotal > 0 ? (sessionRej / sessionTotal) * 100 : 0,
  );
  const allTotal = $derived(allAcc + allRej);
  const allRejectPct = $derived(
    allTotal > 0 ? (allRej / allTotal) * 100 : 0,
  );

  // Detect new rejected shares to trigger shake animation.
  // Skip the first snapshot (baseline), then fire on any increase
  // including from 0 after a pool restart. Declared BEFORE the
  // accepted effect so `reject` is already set when the accept
  // guard reads it.
  let prevRejected = $state(0);
  let rejHasBaseline = false;
  let reject = $state(false);
  $effect(() => {
    const cur = sessionRej;
    const prev = untrack(() => prevRejected);
    if (rejHasBaseline && cur > prev) {
      reject = true;
      setTimeout(() => { reject = false; }, 1200);
    }
    rejHasBaseline = true;
    prevRejected = cur;
  });

  // Detect new accepted shares to trigger flame-slash animation.
  // Skip the first snapshot (baseline), then fire on any increase.
  // Suppressed while a reject animation is active or starting in the
  // same snapshot (rejects are rarer and more important).
  let prevAccepted = $state(0);
  let accHasBaseline = false;
  let pulse = $state(false);
  $effect(() => {
    const cur = sessionAcc;
    const prev = untrack(() => prevAccepted);
    const rejNow = untrack(() => reject);
    if (accHasBaseline && cur > prev && !rejNow) {
      pulse = true;
      setTimeout(() => { pulse = false; }, 700);
    }
    accHasBaseline = true;
    prevAccepted = cur;
  });

  function fmtCount(n: number): string {
    if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
    if (n >= 1e3) return (n / 1e3).toFixed(1) + "K";
    return n.toLocaleString();
  }
</script>

<section class="shares-bar" class:pulse class:reject>
  <div class="slash-effect" aria-hidden="true"></div>
  <div class="reject-flash" aria-hidden="true"></div>
  <div class="content">
    <h3 class="title">Shares
      <button class="stats-btn" onclick={selectStats} title="Share Statistics">
        <svg class="stats-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="12" width="4" height="9" rx="1"/>
          <rect x="10" y="7" width="4" height="14" rx="1"/>
          <rect x="17" y="3" width="4" height="18" rx="1"/>
        </svg>
      </button>
    </h3>
    <div class="stats">
      <div class="group">
        <span class="group-label">Session</span>
        <span class="accepted" title="{sessionAcc.toLocaleString()} accepted">{fmtCount(sessionAcc)}</span>
        <span class="sep">/</span>
        <span class="rejected" title="{sessionRej.toLocaleString()} rejected">{fmtCount(sessionRej)}</span>
        {#if sessionTotal > 0}
          <span class="pct" class:warn={sessionRejectPct > 1}>
            {sessionRejectPct.toFixed(2)}% rejected
          </span>
        {/if}
      </div>
      <div class="divider"></div>
      <div class="group">
        <span class="group-label">All-time</span>
        <span class="accepted" title="{allAcc.toLocaleString()} accepted">{fmtCount(allAcc)}</span>
        <span class="sep">/</span>
        <span class="rejected" title="{allRej.toLocaleString()} rejected">{fmtCount(allRej)}</span>
        {#if allTotal > 0}
          <span class="pct" class:warn={allRejectPct > 1}>
            {allRejectPct.toFixed(2)}% rejected
          </span>
        {/if}
      </div>
    </div>
  </div>
</section>

<style>
  .shares-bar {
    position: relative;
    overflow: hidden;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--bg-card);
    padding: 1.1rem 1.5rem;
  }
  .content {
    position: relative;
    z-index: 1;
    display: flex;
    align-items: baseline;
    justify-content: center;
    gap: 2rem;
  }
  .title {
    margin: 0;
    font-size: 1.1rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--fg);
  }
  .stats-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.35em;
    margin-left: 0.6em;
    vertical-align: middle;
    cursor: pointer;
    color: var(--fg-dim);
    transition: color 0.2s, border-color 0.2s, box-shadow 0.2s, transform 0.2s;
  }
  .stats-btn:hover {
    color: var(--accent);
    border-color: var(--accent);
    box-shadow: 0 0 12px rgba(255, 122, 58, 0.5);
    transform: translateY(-2px) scale(1.05);
  }
  .stats-icon {
    width: 1.2em;
    height: 1.2em;
  }
  .stats {
    display: flex;
    align-items: center;
    gap: 2rem;
    flex-wrap: wrap;
  }
  .group {
    display: flex;
    align-items: baseline;
    gap: 0.5em;
  }
  .group-label {
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--fg-dim);
    margin-right: 0.3em;
  }
  .accepted {
    color: var(--good);
    font-weight: 700;
    font-size: 1.4rem;
    font-variant-numeric: tabular-nums;
  }
  .rejected {
    color: var(--bad);
    font-weight: 700;
    font-size: 1.4rem;
    font-variant-numeric: tabular-nums;
  }
  .sep {
    color: var(--fg-dim);
    font-size: 1.1rem;
  }
  .pct {
    font-size: 1.05rem;
    color: var(--fg-dim);
    margin-left: 0.4em;
  }
  .pct.warn {
    color: var(--bad);
  }
  .divider {
    width: 1px;
    height: 2rem;
    background: var(--border);
  }

  /* Demon Slayer flame-slash animation on new accepted share */
  .slash-effect {
    position: absolute;
    inset: 0;
    pointer-events: none;
    opacity: 0;
    background: linear-gradient(
      90deg,
      transparent 0%,
      rgba(255, 122, 58, 0.0) 30%,
      rgba(255, 122, 58, 0.15) 48%,
      rgba(255, 200, 50, 0.25) 50%,
      rgba(255, 122, 58, 0.15) 52%,
      rgba(255, 122, 58, 0.0) 70%,
      transparent 100%
    );
  }
  .shares-bar.pulse .slash-effect {
    animation: flame-slash 0.7s ease-out;
  }
  @keyframes flame-slash {
    0% {
      opacity: 1;
      transform: translateX(-100%) scaleY(1);
    }
    40% {
      opacity: 1;
      transform: translateX(0%) scaleY(1.2);
    }
    100% {
      opacity: 0;
      transform: translateX(100%) scaleY(1);
    }
  }
  .shares-bar.pulse {
    border-color: rgba(255, 122, 58, 0.5);
    box-shadow: 0 0 12px rgba(255, 122, 58, 0.2);
  }

  /* Red flash + shake on rejected share */
  .reject-flash {
    position: absolute;
    inset: 0;
    pointer-events: none;
    opacity: 0;
    background: rgba(255, 60, 60, 0.12);
  }
  .shares-bar.reject .reject-flash {
    animation: reject-flash 1.2s ease-out;
  }
  .shares-bar.reject {
    animation: reject-shake 0.6s ease-out;
    border-color: rgba(255, 80, 80, 0.6);
    box-shadow: 0 0 16px rgba(255, 60, 60, 0.3);
  }
  @keyframes reject-flash {
    0%   { opacity: 1; }
    40%  { opacity: 0.5; }
    100% { opacity: 0; }
  }
  @keyframes reject-shake {
    0%, 100% { transform: translateX(0); }
    10%  { transform: translateX(-4px); }
    20%  { transform: translateX(4px); }
    30%  { transform: translateX(-3px); }
    40%  { transform: translateX(3px); }
    50%  { transform: translateX(-2px); }
    60%  { transform: translateX(2px); }
    70%  { transform: translateX(-1px); }
    80%  { transform: translateX(1px); }
  }

  @media (max-width: 600px) {
    .content {
      flex-direction: column;
      align-items: flex-start;
      gap: 0.5rem;
    }
    .divider {
      width: 100%;
      height: 1px;
    }
  }
  @media (max-width: 480px) {
    .shares-bar {
      padding: 0.8rem 1rem;
    }
    .accepted, .rejected {
      font-size: 1.15rem;
    }
    .pct {
      font-size: 0.88rem;
    }
    .group {
      flex-wrap: wrap;
    }
  }
</style>
