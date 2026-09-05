<script lang="ts">
  import { untrack } from "svelte";
  import { formatDifficulty } from "../format";
  import { renderShareCard, type CardData, type CardFormat } from "./shareCard";

  let {
    data,
    onclose,
  }: { data: CardData; onclose: () => void } = $props();

  const card = untrack(() => data);
  const isBlock = card.kind === "block";

  // Story is the default (Instagram/X/WhatsApp status); square suits feed
  // posts, where a 9:16 image gets cropped or letterboxed.
  let format: CardFormat = $state("story");

  let canvasEl: HTMLCanvasElement | undefined = $state();
  let busy = $state(false);
  let note = $state("");

  $effect(() => {
    const f = format;
    const el = canvasEl;
    if (el) void renderShareCard(el, card, f);
  });

  const slug = $derived(
    card.kind === "block"
      ? `block-${card.block.height}`
      : `best-share-${formatDifficulty(card.share.sdiff).replace(/\s+/g, "")}`,
  );
  const fileName = $derived(`kamado-${slug}-${format}.png`);

  function toBlob(): Promise<Blob | null> {
    return new Promise((resolve) => {
      if (!canvasEl) return resolve(null);
      canvasEl.toBlob((b) => resolve(b), "image/png");
    });
  }

  async function download(): Promise<void> {
    busy = true;
    note = "";
    const blob = await toBlob();
    busy = false;
    if (!blob) {
      note = "Could not render the image.";
      return;
    }
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = fileName;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
    note = "Saved.";
  }

  async function copyImage(): Promise<void> {
    busy = true;
    note = "";
    try {
      const blob = await toBlob();
      if (!blob) throw new Error("render failed");
      // Clipboard image writes need a secure context; LAN dashboards are
      // often plain HTTP, so fall back to the download path.
      await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
      note = "Copied. Paste it straight into a post.";
    } catch {
      note = "Copying images needs HTTPS here, so use Save instead.";
    } finally {
      busy = false;
    }
  }

  function onKey(ev: KeyboardEvent): void {
    if (ev.key === "Escape") onclose();
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="backdrop" role="dialog" aria-modal="true" aria-label="Share card">
  <div class="sheet">
    <header class="sheet-head">
      <div>
        <div class="sheet-title">{isBlock ? "Share your block" : "Share your best share"}</div>
        <div class="sheet-sub">
          {isBlock
            ? "The card names the chain and links the block, so anyone can look it up for themselves."
            : "The card carries the raw header, so anyone who sees it can verify the work themselves."}
        </div>
      </div>
      <button type="button" class="x" onclick={onclose} aria-label="Close">&#10005;</button>
    </header>

    <div class="preview-wrap">
      <canvas bind:this={canvasEl} class="preview" class:square={format === "square"}></canvas>
    </div>

    <div class="controls">
      <div class="seg">
        <button type="button" class:on={format === "story"} onclick={() => (format = "story")}>
          Story 9:16
        </button>
        <button type="button" class:on={format === "square"} onclick={() => (format = "square")}>
          Square 1:1
        </button>
      </div>
      <div class="acts">
        <button type="button" class="ghost" onclick={copyImage} disabled={busy}>Copy image</button>
        <button type="button" class="primary" onclick={download} disabled={busy}>Save PNG</button>
      </div>
    </div>
    {#if note}
      <div class="note">{note}</div>
    {/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 1500;
    display: flex;
    align-items: center;
    align-items: safe center;
    justify-content: center;
    justify-content: safe center;
    padding: 1.2rem;
    overflow-y: auto;
    background: rgba(5, 7, 12, 0.82);
    backdrop-filter: blur(6px);
    -webkit-backdrop-filter: blur(6px);
    animation: fade 0.2s ease-out;
  }
  @keyframes fade {
    from { opacity: 0; }
    to { opacity: 1; }
  }
  .sheet {
    width: 100%;
    max-width: 560px;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 14px;
    padding: 1.1rem 1.25rem 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.9rem;
  }
  .sheet-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  .sheet-title {
    font-size: 1.05rem;
    font-weight: 600;
  }
  .sheet-sub {
    color: var(--fg-dim);
    font-size: 0.85rem;
    line-height: 1.5;
    margin-top: 0.2rem;
  }
  .x {
    font: inherit;
    flex-shrink: 0;
    color: var(--fg-dim);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.35em 0.55em;
    cursor: pointer;
  }
  .x:hover {
    color: var(--fg);
    border-color: var(--accent);
  }
  .preview-wrap {
    display: flex;
    justify-content: center;
    background: var(--bg-alt);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.8rem;
  }
  .preview {
    display: block;
    width: auto;
    max-width: 100%;
    max-height: 52vh;
    border-radius: 8px;
    box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5);
  }
  .controls {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.8rem;
    flex-wrap: wrap;
  }
  .seg {
    display: flex;
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
  }
  .seg button {
    font: inherit;
    font-size: 0.82rem;
    background: transparent;
    color: var(--fg-dim);
    border: none;
    padding: 0.45em 0.9em;
    cursor: pointer;
  }
  .seg button:not(:last-child) {
    border-right: 1px solid var(--border);
  }
  .seg button.on {
    background: var(--accent);
    color: var(--bg);
    font-weight: 600;
  }
  .acts {
    display: flex;
    gap: 0.5rem;
  }
  .acts button {
    font: inherit;
    font-size: 0.88rem;
    border-radius: 8px;
    padding: 0.5em 1.1em;
    cursor: pointer;
    border: 1px solid var(--border);
  }
  .acts .ghost {
    background: transparent;
    color: var(--fg-dim);
  }
  .acts .ghost:hover:not(:disabled) {
    color: var(--fg);
    border-color: var(--accent);
  }
  .acts .primary {
    background: var(--accent);
    color: var(--bg);
    border-color: var(--accent);
    font-weight: 600;
  }
  .acts .primary:hover:not(:disabled) {
    filter: brightness(1.1);
  }
  .acts button:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .note {
    font-size: 0.82rem;
    color: var(--fg-dim);
  }

  @media (max-width: 480px) {
    .preview {
      max-height: 44vh;
    }
    .controls {
      justify-content: center;
    }
  }
</style>
