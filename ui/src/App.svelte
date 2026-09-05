<script lang="ts">
  import { onMount } from "svelte";
  import { connect, snap } from "./stores/snapshot.svelte";
  import { selection } from "./stores/selection.svelte";
  import Header from "./lib/Header.svelte";
  import HealthBanners from "./lib/HealthBanners.svelte";
  import PoolOverview from "./lib/PoolOverview.svelte";
  import HashrateChart from "./lib/HashrateChart.svelte";
  import MinersTable from "./lib/MinersTable.svelte";
  import BlocksTable from "./lib/BlocksTable.svelte";
  import BestShares from "./lib/BestShares.svelte";
  import UserDetailPage from "./lib/UserDetailPage.svelte";
  import WorkerDetailPage from "./lib/WorkerDetailPage.svelte";
  import AcceleratorPage from "./lib/AcceleratorPage.svelte";
  import StatsPage from "./lib/StatsPage.svelte";
  import BestSharePage from "./lib/BestSharePage.svelte";
  import SharePowPage from "./lib/SharePowPage.svelte";
  import SharesBar from "./lib/SharesBar.svelte";
  import BlockFoundAnimation from "./lib/BlockFoundAnimation.svelte";
  import QRCode from "qrcode";

  const DONATE_ADDR = "bc1qcyuh66rl3xl6a8w6k02ncupg4yhg898023gvar";
  let qrCanvas: HTMLCanvasElement;

  onMount(() => {
    connect();
    QRCode.toCanvas(qrCanvas, DONATE_ADDR, {
      width: 180,
      margin: 1,
      color: { dark: "#e6e9ef", light: "#151a24" },
    });
  });
</script>

<main class:no-bg={selection.page === "accelerator"}>
  <Header />

  {#if snap.data}
    <HealthBanners />
  {/if}

  {#if !snap.data}
    <div class="card placeholder page-enter">
      <div class="stat-label">Status</div>
      <div class="stat-value">Connecting to kamado-api…</div>
      {#if snap.error}
        <div class="stat-sub bad">{snap.error}</div>
      {/if}
    </div>
  {:else if selection.page === "accelerator"}
    {#key 'accelerator'}
      <div class="page-enter">
        <AcceleratorPage />
      </div>
    {/key}
  {:else if selection.page === "stats"}
    {#key 'stats'}
      <div class="page-enter">
        <StatsPage />
      </div>
    {/key}
  {:else if selection.page === "bestshare"}
    {#key 'bestshare'}
      <div class="page-enter">
        <BestSharePage />
      </div>
    {/key}
  {:else if selection.page === "bestshare-pow"}
    {#key 'bestshare-pow'}
      <div class="page-enter">
        <SharePowPage />
      </div>
    {/key}
  {:else if selection.worker}
    {#key selection.worker}
      <div class="page-enter">
        <WorkerDetailPage />
      </div>
    {/key}
  {:else if selection.user}
    {#key selection.user}
      <div class="page-enter">
        <UserDetailPage />
      </div>
    {/key}
  {:else}
    {#key 'dashboard'}
      <div class="page-enter">
        <PoolOverview />
        <SharesBar />
        <HashrateChart />
        <BlocksTable />
        <BestShares />
        <MinersTable />
      </div>
    {/key}
  {/if}
</main>

<footer class="donate">
  <div class="donate-tagline">Open source & Made with ❤️</div>
  <div class="donate-label">Donate BTC</div>
  <div class="donate-addr-wrap">
    <span class="addr">bc1qcyuh66rl3xl6a8w6k02ncupg4yhg898023gvar</span>
    <div class="qr-popup">
      <canvas bind:this={qrCanvas} width="180" height="180"></canvas>
    </div>
  </div>
</footer>

<BlockFoundAnimation />

<style>
  main {
    position: relative;
    max-width: 1280px;
    margin: 0 auto;
    padding: 1.5rem 1.25rem 4rem;
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }
  @media (max-width: 480px) {
    main {
      padding: 1rem 0.6rem 3rem;
      gap: 1rem;
    }
  }
  main::before {
    content: '';
    position: fixed;
    inset: 0;
    z-index: -1;
    background: url('/tanjiro2.jpg') center center / cover no-repeat;
    opacity: 0.12;
    pointer-events: none;
    transition: opacity 0.4s ease;
  }
  main.no-bg::before {
    opacity: 0;
  }
  main::after {
    content: '';
    position: fixed;
    inset: 0;
    z-index: -1;
    background: url('/rengoku1.jpg') center center / cover no-repeat;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.4s ease;
  }
  main.no-bg::after {
    opacity: 0.30;
  }
  .placeholder {
    text-align: center;
  }
  .bad {
    color: var(--bad);
  }
  :global(.donate) {
    text-align: center;
    padding: 2.5rem 1rem 2rem;
    color: var(--fg-dim);
    font-size: 0.9rem;
    border-top: 1px solid var(--border);
    margin-top: 1rem;
  }
  :global(.donate .donate-tagline) {
    font-size: 1rem;
    margin-bottom: 0.75rem;
    color: var(--fg);
    letter-spacing: 0.02em;
  }
  :global(.donate .donate-label) {
    font-size: 0.8rem;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--fg-dim);
    margin-bottom: 0.4rem;
  }
  :global(.donate .donate-addr-wrap) {
    position: relative;
    display: inline-block;
  }
  :global(.donate .addr) {
    font-family: "Fira Code", "Cascadia Code", monospace;
    font-size: 0.95rem;
    color: var(--accent);
    word-break: break-all;
    user-select: all;
    cursor: pointer;
    transition: text-shadow 0.2s;
  }
  :global(.donate .addr:hover) {
    text-shadow: 0 0 8px var(--accent);
  }
  :global(.donate .qr-popup) {
    position: absolute;
    bottom: calc(100% + 12px);
    left: 50%;
    transform: translateX(-50%) scale(0.9);
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 12px;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.2s, transform 0.2s;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  }
  :global(.donate .donate-addr-wrap:hover .qr-popup) {
    opacity: 1;
    pointer-events: auto;
    transform: translateX(-50%) scale(1);
  }
  :global(.donate .qr-popup canvas) {
    display: block;
    border-radius: 6px;
  }
  .page-enter {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
    animation: page-fade-in 0.3s ease-out;
  }
  @keyframes page-fade-in {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
</style>
