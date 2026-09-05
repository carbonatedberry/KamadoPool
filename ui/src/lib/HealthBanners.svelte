<script lang="ts">
  import { snap } from "../stores/snapshot.svelte";
  import { formatAgo } from "../format";

  // submit_gap > 0 means ckpool tried to submit at least one block that
  // never got the "Solved and confirmed" follow-up, so either bitcoind
  // rejected the submission or the RPC dropped. These are persistent
  // counters so the banner stays visible until the operator clears it
  // by inspecting bitcoind's logs.
  const submitGap = $derived.by(() => {
    const d = snap.data;
    if (!d) return 0;
    return Math.max(0, (d.block_submit_attempts ?? 0) - (d.block_submits_confirmed ?? 0));
  });

  // ZMQ stale = the tip has advanced but ZMQ didn't fire. ZMQ
  // delivers within milliseconds, so if the tip changed recently
  // but the last ZMQ event is much older, the subscriber is broken.
  // When no block has been mined, both ages grow together → no alarm.
  const zmqStale = $derived.by(() => {
    const d = snap.data;
    if (!d || !d.zmq_enabled || !d.has_last_zmq_event) return false;
    const zmqAge = d.last_zmq_event_age ?? 0;
    const tipAge = d.tip_changed_age ?? 0;
    // Alarm when ZMQ is 3+ min older than the last tip change.
    return zmqAge > tipAge + 180;
  });

  const bitcoindDown = $derived(snap.data != null && !snap.data.bitcoin_ok);
</script>

{#if bitcoindDown || submitGap > 0 || zmqStale}
  <div class="banners">
    {#if bitcoindDown}
      <div class="banner error">
        <span class="icon">!</span>
        <div class="text">
          <strong>Bitcoin Core is unreachable.</strong>
          Stratum server has been stopped, miners will failover to backup pools.
          Service will resume automatically when Bitcoin Core recovers.
        </div>
      </div>
    {/if}
    {#if submitGap > 0}
      <div class="banner warn">
        <span class="icon">!</span>
        <div class="text">
          <strong>Submit gap:</strong>
          {submitGap} block{submitGap === 1 ? "" : "s"} attempted but not
          confirmed by bitcoind. Investigate primary bitcoind logs,
          submission may have been rejected or the RPC dropped.
        </div>
      </div>
    {/if}
    {#if zmqStale}
      <div class="banner warn">
        <span class="icon">~</span>
        <div class="text">
          <strong>ZMQ subscriber stale.</strong>
          No <code>hashblock</code> frame from bitcoind in
          {formatAgo(Date.now() / 1000 - (snap.data?.last_zmq_event_age ?? 0))}.
          Tip changes will fall back to slower polling; check that
          bitcoind is reachable on its ZMQ port.
        </div>
      </div>
    {/if}
  </div>
{/if}

<style>
  .banners {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .banner {
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
    padding: 0.65rem 0.85rem;
    border-radius: 6px;
    border: 1px solid transparent;
    line-height: 1.4;
  }
  .banner.warn {
    background: rgb(50, 40, 15);
    border-color: rgb(220, 170, 60);
    color: rgb(220, 180, 100);
  }
  .banner.error {
    background: rgb(50, 20, 20);
    border-color: rgb(220, 60, 60);
    color: rgb(230, 120, 120);
  }
  .icon {
    flex: 0 0 auto;
    width: 1.5em;
    height: 1.5em;
    border-radius: 50%;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 0.9em;
    background: currentColor;
    color: var(--bg, #111);
  }
  .text {
    flex: 1 1 auto;
    font-size: 0.92em;
  }
  .banner code {
    font-size: 0.9em;
    padding: 0.05em 0.3em;
    border-radius: 3px;
    background: rgba(255, 255, 255, 0.07);
  }
</style>
