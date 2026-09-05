<script lang="ts">
  import { untrack } from "svelte";
  import { snap } from "../stores/snapshot.svelte";

  type Particle = {
    id: number;
    left: number;    // viewport %
    delay: number;   // seconds
    duration: number; // seconds
    drift: number;   // horizontal drift in vw
    rotate: number;  // degrees
    scale: number;
    kind: "confetti" | "petal";
    color: string;
    shape: "square" | "rect" | "circle";
  };

  const CONFETTI_COUNT = 60;
  const PETAL_COUNT = 18;

  let active = $state(false);
  let particles = $state<Particle[]>([]);
  let blockCount = $state(0);
  let foundBlock = $state<{ height: number; reward?: number; hash?: string } | null>(null);

  const currentBlockCount = $derived(
    (snap.data?.recent_blocks ?? []).length,
  );

  // Detect when the pool finds a new block (recent_blocks grows).
  $effect(() => {
    const count = currentBlockCount;
    const prev = untrack(() => blockCount);
    if (count > 0 && prev > 0 && count > prev) {
      const blocks = snap.data?.recent_blocks ?? [];
      const latest = blocks[blocks.length - 1];
      trigger(latest);
    }
    blockCount = count;
  });

  const confettiColors = [
    "#ff7a3a", "#ffb347", "#ff6b6b", "#5ce0a8", "#47b8f5",
    "#f5c447", "#ff5ecd", "#a78bfa", "#34d399", "#fb923c",
  ];

  function trigger(block?: { height: number; reward_btc?: number; hash?: string }): void {
    if (block) {
      foundBlock = { height: block.height, reward: block.reward_btc, hash: block.hash };
    }
    active = true;

    // Stagger delays across a full cycle so the rain looks continuous
    // when looping. Each particle's delay spreads it evenly over its
    // own duration window.
    const confetti: Particle[] = Array.from({ length: CONFETTI_COUNT }, (_, i) => {
      const dur = 2.0 + Math.random() * 2.5;
      return {
        id: Date.now() + i,
        left: Math.random() * 100,
        delay: (i / CONFETTI_COUNT) * dur,
        duration: dur,
        drift: (Math.random() - 0.5) * 30,
        rotate: (Math.random() - 0.5) * 1080,
        scale: 0.5 + Math.random() * 0.7,
        kind: "confetti" as const,
        color: confettiColors[Math.floor(Math.random() * confettiColors.length)],
        shape: (["square", "rect", "circle"] as const)[Math.floor(Math.random() * 3)],
      };
    });

    const petals: Particle[] = Array.from({ length: PETAL_COUNT }, (_, i) => {
      const dur = 2.5 + Math.random() * 1.5;
      return {
        id: Date.now() + CONFETTI_COUNT + i,
        left: Math.random() * 100,
        delay: (i / PETAL_COUNT) * dur,
        duration: dur,
        drift: (Math.random() - 0.5) * 20,
        rotate: (Math.random() - 0.5) * 720,
        scale: 0.6 + Math.random() * 0.8,
        kind: "petal" as const,
        color: "",
        shape: "circle" as const,
      };
    });

    particles = [...confetti, ...petals];
  }

  function dismiss(): void {
    active = false;
    particles = [];
    foundBlock = null;
  }
</script>

{#if active}
  <div class="overlay" aria-hidden="true">
    <div class="burst"></div>
    <div class="rays"></div>

    {#each particles as p (p.id)}
      {#if p.kind === "confetti"}
        <span
          class="confetti {p.shape}"
          style="
            left: {p.left}vw;
            animation-delay: {p.delay}s;
            animation-duration: {p.duration}s;
            --drift: {p.drift}vw;
            --rot: {p.rotate}deg;
            --scale: {p.scale};
            --color: {p.color};
          "
        ></span>
      {:else}
        <span
          class="petal"
          style="
            left: {p.left}vw;
            animation-delay: {p.delay}s;
            animation-duration: {p.duration}s;
            --drift: {p.drift}vw;
            --rot: {p.rotate}deg;
            --scale: {p.scale};
          "
        >
          <svg viewBox="0 0 32 32">
            <g fill="currentColor">
              {#each [0, 72, 144, 216, 288] as a}
                <ellipse
                  cx="16" cy="7" rx="4.5" ry="8"
                  transform="rotate({a} 16 16)"
                />
              {/each}
              <circle cx="16" cy="16" r="1.6" fill="#6b1f1f" />
            </g>
          </svg>
        </span>
      {/if}
    {/each}
  </div>

  <!-- Banner, above confetti, interactive -->
  <div class="banner-wrap">
    <div class="banner">
      <div class="party-row">
        <span class="party-emoji">&#x1F389;</span>
      </div>
      <div class="banner-title">BLOCK FOUND!</div>
      {#if foundBlock}
        <div class="banner-details">
          <span class="detail">Height <strong>{foundBlock.height.toLocaleString()}</strong></span>
          {#if foundBlock.reward}
            <span class="detail-sep">|</span>
            <span class="detail">Reward <strong>{foundBlock.reward.toFixed(4)} BTC</strong></span>
          {/if}
        </div>
      {/if}
      <button class="dismiss-btn" onclick={dismiss}>
        LET'S GOOO
      </button>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    pointer-events: none;
    overflow: hidden;
    z-index: 150;
  }

  .burst {
    position: absolute;
    left: 50%;
    top: -20vh;
    transform: translate(-50%, -50%);
    width: 40vw;
    height: 40vw;
    border-radius: 50%;
    background:
      radial-gradient(circle, rgba(255, 160, 70, 0.75) 0%,
                              rgba(255, 90, 30, 0.45) 30%,
                              rgba(180, 40, 20, 0.18) 55%,
                              transparent 75%);
    filter: blur(20px);
    animation: burst 2s ease-out forwards;
  }
  @keyframes burst {
    0%   { transform: translate(-50%, -50%) scale(0.2); opacity: 0; }
    15%  { opacity: 1; }
    100% { transform: translate(-50%, -50%) scale(6); opacity: 0; }
  }

  .rays {
    position: absolute;
    left: 50%;
    top: 50%;
    width: 200vmax;
    height: 200vmax;
    transform: translate(-50%, -50%);
    background: repeating-conic-gradient(
      from 0deg,
      rgba(255, 140, 50, 0.06) 0deg 4deg,
      transparent 4deg 14deg
    );
    mix-blend-mode: screen;
    animation: rays-spin 20s linear infinite;
    opacity: 0.6;
  }
  @keyframes rays-spin {
    from { transform: translate(-50%, -50%) rotate(0deg); }
    to   { transform: translate(-50%, -50%) rotate(360deg); }
  }

  /* Confetti pieces */
  .confetti {
    position: absolute;
    top: -5vh;
    background: var(--color);
    opacity: 0;
    animation-name: confettiFall;
    animation-timing-function: cubic-bezier(0.25, 0.1, 0.25, 1);
    animation-fill-mode: none;
    animation-iteration-count: infinite;
  }
  .confetti.square {
    width: 10px;
    height: 10px;
    border-radius: 1px;
  }
  .confetti.rect {
    width: 8px;
    height: 14px;
    border-radius: 1px;
  }
  .confetti.circle {
    width: 10px;
    height: 10px;
    border-radius: 50%;
  }
  @keyframes confettiFall {
    0% {
      transform: translateX(0) translateY(0) rotate(0) scale(var(--scale, 1));
      opacity: 0;
    }
    8% { opacity: 1; }
    85% { opacity: 1; }
    100% {
      transform: translateX(var(--drift, 0)) translateY(110vh) rotate(var(--rot, 0))
                 scale(var(--scale, 1));
      opacity: 0;
    }
  }

  /* Sakura petals */
  .petal {
    position: absolute;
    top: -8vh;
    width: 26px;
    height: 26px;
    color: hsl(340, 70%, 78%);
    filter: drop-shadow(0 1px 2px rgba(200, 60, 90, 0.35));
    opacity: 0;
    animation-name: petalFall;
    animation-timing-function: cubic-bezier(0.4, 0.1, 0.4, 1);
    animation-fill-mode: none;
    animation-iteration-count: infinite;
  }
  .petal svg {
    width: 100%;
    height: 100%;
  }
  @keyframes petalFall {
    0%   { transform: translateX(0) translateY(0) rotate(0) scale(var(--scale, 1));
           opacity: 0; }
    10%  { opacity: 1; }
    85%  { opacity: 0.8; }
    100% { transform: translateX(var(--drift, 0)) translateY(110vh) rotate(var(--rot, 0))
                      scale(var(--scale, 1));
           opacity: 0; }
  }

  /* Banner */
  .banner-wrap {
    position: fixed;
    inset: 0;
    z-index: 200;
    display: flex;
    align-items: center;
    justify-content: center;
    pointer-events: none;
    animation: banner-enter 0.5s ease-out both;
  }
  @keyframes banner-enter {
    0% { opacity: 0; transform: scale(0.7); }
    50% { transform: scale(1.05); }
    100% { opacity: 1; transform: scale(1); }
  }

  .banner {
    pointer-events: auto;
    background: rgb(20, 24, 32);
    border: 2px solid var(--accent);
    border-radius: 16px;
    padding: 2.5rem 3.5rem;
    text-align: center;
    box-shadow:
      0 0 60px rgba(255, 122, 58, 0.3),
      0 0 120px rgba(255, 122, 58, 0.1),
      inset 0 1px 0 rgba(255, 255, 255, 0.05);
    animation: banner-glow 2s ease-in-out infinite alternate;
  }
  @keyframes banner-glow {
    0%   { box-shadow: 0 0 40px rgba(255, 122, 58, 0.25),
                       0 0 80px rgba(255, 122, 58, 0.08); }
    100% { box-shadow: 0 0 60px rgba(255, 122, 58, 0.4),
                       0 0 120px rgba(255, 122, 58, 0.15); }
  }

  .party-row {
    display: flex;
    justify-content: center;
    gap: 1rem;
    margin-bottom: 0.8rem;
  }
  .party-emoji {
    font-size: 3rem;
    animation: party-bounce 0.6s ease-in-out infinite alternate;
  }
  @keyframes party-bounce {
    0%   { transform: translateY(0) rotate(-5deg); }
    100% { transform: translateY(-12px) rotate(8deg); }
  }

  .banner-title {
    font-size: 2.2rem;
    font-weight: 800;
    letter-spacing: 0.04em;
    background: linear-gradient(135deg, #ff7a3a, #f5c447, #ff7a3a);
    background-size: 200% 200%;
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
    background-clip: text;
    animation: shimmer 2s ease-in-out infinite;
  }
  @keyframes shimmer {
    0%   { background-position: 0% 50%; }
    50%  { background-position: 100% 50%; }
    100% { background-position: 0% 50%; }
  }

  .banner-details {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.8rem;
    margin-top: 0.8rem;
    color: var(--fg-dim);
    font-size: 1rem;
  }
  .detail strong {
    color: var(--fg);
    font-weight: 600;
  }
  .detail-sep {
    color: var(--border);
  }

  .dismiss-btn {
    margin-top: 1.5rem;
    padding: 0.8em 2.5em;
    font-size: 1.1rem;
    font-weight: 700;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    background: linear-gradient(135deg, #ff7a3a, #ff5722);
    color: #fff;
    border: none;
    border-radius: 10px;
    cursor: pointer;
    transition: transform 0.15s, box-shadow 0.15s;
    box-shadow: 0 4px 20px rgba(255, 87, 34, 0.4);
  }
  .dismiss-btn:hover {
    transform: translateY(-2px) scale(1.04);
    box-shadow: 0 6px 28px rgba(255, 87, 34, 0.55);
  }
  .dismiss-btn:active {
    transform: translateY(0) scale(0.98);
  }

  @media (max-width: 600px) {
    .banner {
      padding: 2rem 1.5rem;
      margin: 0 1rem;
    }
    .banner-title {
      font-size: 1.6rem;
    }
    .party-emoji {
      font-size: 2.2rem;
    }
    .banner-details {
      flex-direction: column;
      gap: 0.3rem;
    }
    .detail-sep {
      display: none;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .burst, .rays, .confetti, .petal, .party-emoji, .banner-title {
      animation-duration: 0.01s;
      animation-iteration-count: 1;
    }
    .rays { animation: none; opacity: 0; }
  }
</style>
