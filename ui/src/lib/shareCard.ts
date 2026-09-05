// Renders the shareable cards to a canvas: the "best share" proof card and
// the celebratory "I found a block!" card.
//
// Kept out of the Svelte components so it can be driven headlessly and the
// output inspected. The layout measures its content first, then spreads the
// leftover height across the section gaps, so the same code fills a 9:16
// story and a 1:1 square without leaving voids or colliding.

import QRCode from "qrcode";
import { formatDifficulty } from "../format";
import type { BestSharePow, BlockRecord } from "../types";

export type CardFormat = "story" | "square";

export type CardData =
  | { kind: "share"; share: BestSharePow; chain: string }
  | { kind: "block"; block: BlockRecord; chain: string; explorerBase: string };

export const CARD_SIZES: Record<CardFormat, { w: number; h: number }> = {
  story: { w: 1080, h: 1920 },
  square: { w: 1080, h: 1080 },
};

const C = {
  bg: "#080a10",
  ember: "#ff7a3a",
  fg: "#e6e9ef",
  dim: "#8a92a6",
  good: "#5ce0a8",
  gold: "#ffc65c",
  btc: "#f7931a",
  panel: "rgba(255,255,255,0.045)",
  stroke: "rgba(255,255,255,0.10)",
};
const MONO = '"JetBrains Mono","Fira Code",ui-monospace,SFMono-Regular,Menlo,monospace';
const SANS = '"Inter","Segoe UI",system-ui,-apple-system,sans-serif';

type Scale = {
  margin: number; brand: number; hero: number; body: number; small: number;
  eyebrow: number; mono: number; monoHeader: number; qr: number; gap: number;
};

const SCALES: Record<CardFormat, Scale> = {
  story: {
    margin: 88, brand: 44, hero: 170, body: 32, small: 26,
    eyebrow: 26, mono: 33, monoHeader: 25, qr: 250, gap: 1,
  },
  square: {
    margin: 64, brand: 34, hero: 104, body: 25, small: 21,
    eyebrow: 21, mono: 24, monoHeader: 18, qr: 156, gap: 0.6,
  },
};

/**
 * Hash counts read as "66.66 EH hashes" if the SI symbol is kept, which
 * says hash twice. Spell the magnitude instead.
 */
export function spellHashes(n: number): string {
  const units: [number, string][] = [
    [1e27, "ronnahashes"], [1e24, "yottahashes"], [1e21, "zettahashes"],
    [1e18, "exahashes"], [1e15, "petahashes"], [1e12, "terahashes"],
    [1e9, "gigahashes"], [1e6, "megahashes"], [1e3, "kilohashes"],
  ];
  for (const [v, name] of units) {
    if (n >= v) return `${(n / v).toFixed(2)} ${name}`;
  }
  return `${Math.round(n)} hashes`;
}

export function chainLabel(chain: string | undefined): string {
  switch (chain) {
    case "main": case "": case undefined: return "mainnet";
    case "test": return "testnet3";
    default: return chain;
  }
}

function roundRect(
  ctx: CanvasRenderingContext2D,
  x: number, y: number, w: number, h: number, r: number,
): void {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

/** Flame mark, drawn rather than relying on an emoji font. */
function drawFlame(ctx: CanvasRenderingContext2D, cx: number, cy: number, s: number): void {
  ctx.save();
  ctx.translate(cx, cy);
  ctx.scale(s, s);
  const g = ctx.createLinearGradient(0, -1, 0, 1);
  g.addColorStop(0, "#ffd08a");
  g.addColorStop(0.45, C.ember);
  g.addColorStop(1, "#c0361a");
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.moveTo(0, -1);
  ctx.bezierCurveTo(0.62, -0.28, 0.78, 0.28, 0.36, 0.72);
  ctx.bezierCurveTo(0.16, 0.94, -0.2, 0.98, -0.44, 0.78);
  ctx.bezierCurveTo(-0.85, 0.42, -0.7, -0.2, -0.18, -0.52);
  ctx.bezierCurveTo(-0.24, -0.16, -0.05, -0.02, 0.08, -0.16);
  ctx.bezierCurveTo(0.22, -0.34, 0.1, -0.7, 0, -1);
  ctx.closePath();
  ctx.fill();
  ctx.restore();
}

// The official Bitcoin logo (Bitboy, public domain), as its two SVG paths
// on a 64x64 viewBox: the coin, then the ₿ knocked out of it.
const BTC_COIN =
  "M63.04 39.741c-4.274 17.143-21.638 27.575-38.783 23.301C7.12 58.768-3.313 41.404.962 " +
  "24.262 5.234 7.117 22.598-3.315 39.737.957c17.144 4.274 27.576 21.64 23.303 38.784z";
const BTC_SYMBOL =
  "M46.11 27.441c.636-4.258-2.606-6.547-7.039-8.074l1.438-5.768-3.512-.875-1.4 5.616c-.923-.23-" +
  "1.871-.447-2.813-.662l1.41-5.653-3.51-.875-1.439 5.766c-.764-.174-1.514-.346-2.242-.527l.004-" +
  ".018-4.842-1.209-.934 3.75s2.605.597 2.55.634c1.422.355 1.68 1.296 1.636 2.042l-1.638 6.571c." +
  "098.025.225.061.365.117l-.37-.092-2.297 9.205c-.174.432-.615 1.08-1.609.834.035.051-2.552-.637-" +
  "2.552-.637l-1.743 4.019 4.569 1.139c.85.213 1.683.436 2.503.646l-1.453 5.834 3.507.875 1.439-" +
  "5.772c.958.26 1.888.5 2.798.726L27.7 50.83l3.511.875 1.453-5.823c5.987 1.133 10.489.676 12.384-" +
  "4.739 1.527-4.36-.076-6.875-3.226-8.515 2.294-.529 4.022-2.038 4.483-5.155l.005-.032zm-8.023 " +
  "11.249c-1.085 4.36-8.426 2.003-10.806 1.412l1.928-7.729c2.38.594 10.012 1.77 8.878 6.317zm1.086-" +
  "11.312c-.99 3.966-7.1 1.951-9.082 1.457l1.748-7.01c1.982.494 8.365 1.416 7.334 5.553z";

/** The real Bitcoin logo, drawn from its official path data. */
function drawBitcoin(ctx: CanvasRenderingContext2D, cx: number, cy: number, r: number): void {
  ctx.save();
  ctx.translate(cx - r, cy - r);
  ctx.scale((r * 2) / 64, (r * 2) / 64);
  ctx.fillStyle = C.btc;
  ctx.fill(new Path2D(BTC_COIN));
  ctx.fillStyle = "#ffffff";
  ctx.fill(new Path2D(BTC_SYMBOL));
  ctx.restore();
}

/** Monospace is uniform width, so chunk by measured character. */
function monoChunks(ctx: CanvasRenderingContext2D, text: string, maxW: number): string[] {
  const cw = ctx.measureText("0").width || 1;
  const per = Math.max(8, Math.floor(maxW / cw));
  const out: string[] = [];
  for (let i = 0; i < text.length; i += per) out.push(text.slice(i, i + per));
  return out;
}

function eyebrow(
  ctx: CanvasRenderingContext2D, text: string, x: number, y: number,
  size: number, color: string,
): void {
  ctx.font = `600 ${size}px ${SANS}`;
  ctx.fillStyle = color;
  ctx.letterSpacing = `${size * 0.18}px`;
  ctx.fillText(text.toUpperCase(), x, y);
  ctx.letterSpacing = "0px";
}

function panelHeight(lineCount: number, font: number): number {
  const lh = font * 1.5;
  return lineCount * lh + font * 0.95 * 2 - (lh - font);
}

function hexPanel(
  ctx: CanvasRenderingContext2D,
  lines: string[], x: number, y: number, w: number,
  font: number, opts: { fill: string; stroke: string; leadingZeros?: number },
): number {
  const lh = font * 1.5;
  const pad = font * 0.95;
  const h = panelHeight(lines.length, font);
  ctx.fillStyle = opts.fill;
  ctx.strokeStyle = opts.stroke;
  ctx.lineWidth = 2;
  roundRect(ctx, x, y, w, h, 16);
  ctx.fill();
  ctx.stroke();

  let ty = y + pad + font * 0.8;
  let consumed = 0;
  const zeros = opts.leadingZeros ?? 0;
  for (const line of lines) {
    const zeroPart = Math.max(0, Math.min(line.length, zeros - consumed));
    if (zeroPart > 0) {
      ctx.fillStyle = C.good;
      ctx.shadowColor = "rgba(92,224,168,0.5)";
      ctx.shadowBlur = 16;
      ctx.fillText(line.slice(0, zeroPart), x + pad, ty);
      ctx.shadowBlur = 0;
    }
    if (zeroPart < line.length) {
      const off = zeroPart > 0 ? ctx.measureText(line.slice(0, zeroPart)).width : 0;
      ctx.fillStyle = opts.leadingZeros !== undefined ? C.fg : "rgba(230,233,239,0.88)";
      ctx.fillText(line.slice(zeroPart), x + pad + off, ty);
    }
    consumed += line.length;
    ty += lh;
  }
  return h;
}

/** Rounded pill, used for the chain badge. */
function badge(
  ctx: CanvasRenderingContext2D, text: string, x: number, y: number,
  size: number, fg: string, bg: string, border: string,
): number {
  ctx.font = `600 ${size}px ${SANS}`;
  ctx.letterSpacing = `${size * 0.1}px`;
  const tw = ctx.measureText(text.toUpperCase()).width;
  const padX = size * 0.75;
  const h = size * 2;
  ctx.fillStyle = bg;
  ctx.strokeStyle = border;
  ctx.lineWidth = 2;
  roundRect(ctx, x, y - h * 0.72, tw + padX * 2, h, h / 2);
  ctx.fill();
  ctx.stroke();
  ctx.fillStyle = fg;
  ctx.fillText(text.toUpperCase(), x + padX, y);
  ctx.letterSpacing = "0px";
  return tw + padX * 2;
}

function paintBackground(
  ctx: CanvasRenderingContext2D, w: number, h: number, celebratory: boolean,
): void {
  ctx.fillStyle = C.bg;
  ctx.fillRect(0, 0, w, h);
  const top = ctx.createRadialGradient(w * 0.5, -h * 0.08, 0, w * 0.5, -h * 0.08, h * 0.66);
  top.addColorStop(0, celebratory ? "rgba(255,180,60,0.34)" : "rgba(255,110,40,0.30)");
  top.addColorStop(1, "rgba(255,110,40,0)");
  ctx.fillStyle = top;
  ctx.fillRect(0, 0, w, h);
  const low = ctx.createRadialGradient(w * 0.06, h, 0, w * 0.06, h, h * 0.5);
  low.addColorStop(0, celebratory ? "rgba(255,140,30,0.22)" : "rgba(190,45,20,0.18)");
  low.addColorStop(1, "rgba(190,45,20,0)");
  ctx.fillStyle = low;
  ctx.fillRect(0, 0, w, h);
  ctx.fillStyle = "rgba(40,64,48,0.055)";
  const cell = 54;
  for (let yy = 0; yy < h; yy += cell * 2) {
    for (let xx = 0; xx < w; xx += cell * 2) {
      ctx.fillRect(xx, yy, cell, cell);
      ctx.fillRect(xx + cell, yy + cell, cell, cell);
    }
  }
}

/** Sparks radiating behind the headline on the block card. */
function drawSparks(ctx: CanvasRenderingContext2D, w: number, top: number, h: number): void {
  ctx.save();
  let seed = 20260903;
  const rnd = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  for (let i = 0; i < 46; i++) {
    const x = rnd() * w;
    const y = top + rnd() * h;
    const r = 1.5 + rnd() * 3.5;
    ctx.globalAlpha = 0.18 + rnd() * 0.5;
    ctx.fillStyle = rnd() > 0.4 ? C.gold : C.ember;
    ctx.beginPath();
    ctx.arc(x, y, r, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.restore();
  ctx.globalAlpha = 1;
}

export async function renderShareCard(
  canvas: HTMLCanvasElement,
  data: CardData,
  format: CardFormat,
): Promise<void> {
  const { w, h } = CARD_SIZES[format];
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;

  try {
    await document.fonts.ready;
  } catch {
    /* fall back to whatever the stack resolves to */
  }

  const s = SCALES[format];
  const M = s.margin;
  const innerW = w - M * 2;
  const isBlock = data.kind === "block";

  paintBackground(ctx, w, h, isBlock);
  ctx.textBaseline = "alphabetic";
  ctx.textAlign = "left";

  const footY = h - M * 0.75;
  const ruleY = footY - s.body * 2.0;
  const startY = M + s.brand * 0.9;

  // ── QR payload: for a mined block the chain itself is the proof, so
  //    link the explorer; for a share, carry the header bytes.
  const qrPayload = isBlock && data.block.hash
    ? `${data.explorerBase}/block/${data.block.hash}`
    : data.kind === "share" ? data.share.header : "";
  const qrCaption = isBlock
    ? ["See it on the chain", "anyone can look it up"]
    : ["Scan for the header", "verify it yourself, no trust required"];

  // QR block sits centred at the bottom, above the footer rule.
  const qrSize = s.qr;
  const qrCaptionH = s.body * 0.94 + s.small * 1.5;
  const qrBlockH = qrSize + s.body * 0.9 + qrCaptionH;

  // ── measure, then share the leftover height across the gaps
  ctx.font = `500 ${s.mono}px ${MONO}`;
  const hashText = isBlock ? (data.block.hash ?? "") : data.share.hash;
  const hashLines = hashText ? monoChunks(ctx, hashText, innerW - s.mono * 1.9) : [];
  const hashPanelH = hashLines.length ? panelHeight(hashLines.length, s.mono) : 0;

  let hdrLines: string[] = [];
  let hdrPanelH = 0;
  if (data.kind === "share") {
    ctx.font = `400 ${s.monoHeader}px ${MONO}`;
    hdrLines = monoChunks(ctx, data.share.header, innerW - s.monoHeader * 2.2);
    hdrPanelH = panelHeight(hdrLines.length, s.monoHeader);
  }

  const gapUnits = isBlock
    ? [70, 24, 56, 60, 26, 44, 40]
    : [78, 24, 58, 74, 26, 46, 66, 26, 42, 40];
  const gapTotal = gapUnits.reduce((a, b) => a + b, 0) * s.gap;

  const gapX = data.kind === "share" && data.share.sdiff > 0
    ? data.share.netdiff / data.share.sdiff : 0;

  const fixedH =
    s.small * 1.5 +
    s.hero * 0.78 +
    (isBlock ? s.body * 2.6 : (gapX > 1 ? s.body * 1.45 : 0)) +
    hashPanelH + hdrPanelH + qrBlockH;
  const natural = fixedH + gapTotal;
  const available = ruleY - s.body * 1.1 - startY;
  // Spread the slack, but cap how far any one gap can grow. Without the
  // cap a card with few sections (the block one) turns every gap into a
  // ~80px canyon; with it, the remainder becomes balanced breathing room
  // above the content and before the call to action.
  const slack = Math.max(0, available - natural);
  const extra = Math.min(slack / gapUnits.length, s.body * 1.7);
  const leftover = slack - extra * gapUnits.length;
  const topOffset = leftover * 0.45;
  const G = (n: number) => n * s.gap + extra;

  // ── brand lockup, with the real bitcoin logo balancing it
  let y = startY;
  drawFlame(ctx, M + s.brand * 0.42, y - s.brand * 0.42, s.brand * 0.62);
  ctx.font = `700 ${s.brand}px ${SANS}`;
  ctx.fillStyle = C.fg;
  ctx.letterSpacing = "2px";
  ctx.fillText("KAMADO POOL", M + s.brand * 1.08, y);
  ctx.letterSpacing = "0px";
  ctx.font = `400 ${s.small}px ${SANS}`;
  ctx.fillStyle = C.dim;
  ctx.fillText("solo bitcoin mining", M + s.brand * 1.12, y + s.small * 1.5);
  drawBitcoin(ctx, w - M - s.brand * 0.6, y - s.brand * 0.16, s.brand * 0.6);

  y += s.small * 1.5 + G(isBlock ? 70 : 78) + topOffset;

  if (isBlock) {
    const b = data.block;
    drawSparks(ctx, w, y - s.hero * 0.9, s.hero * 1.5);

    // headline
    ctx.font = `800 ${s.hero * 0.46}px ${SANS}`;
    const grad = ctx.createLinearGradient(M, y - s.hero * 0.3, M + innerW * 0.9, y);
    grad.addColorStop(0, C.gold);
    grad.addColorStop(1, C.ember);
    ctx.fillStyle = grad;
    ctx.shadowColor = "rgba(255,180,60,0.5)";
    ctx.shadowBlur = 50;
    ctx.fillText("I FOUND A BLOCK!", M, y);
    ctx.shadowBlur = 0;

    // block height as the hero figure
    y += G(24) + s.hero * 0.78;
    const hero = `#${b.height.toLocaleString()}`;
    ctx.font = `700 ${s.hero}px ${SANS}`;
    ctx.fillStyle = C.fg;
    ctx.shadowColor = "rgba(255,180,60,0.4)";
    ctx.shadowBlur = 50;
    ctx.fillText(hero, M, y);
    ctx.shadowBlur = 0;

    // chain badge beside the height, so a testnet block is never mistaken
    const heroW = ctx.measureText(hero).width;
    const label = chainLabel(b.chain || data.chain);
    const isMain = label === "mainnet";
    badge(
      ctx, label, M + heroW + s.body * 0.8, y - s.hero * 0.18, s.small,
      isMain ? C.gold : "#7aa2f7",
      isMain ? "rgba(255,198,92,0.12)" : "rgba(122,162,247,0.12)",
      isMain ? "rgba(255,198,92,0.5)" : "rgba(122,162,247,0.5)",
    );

    // reward and the difficulty of the winning share
    y += G(56);
    ctx.font = `600 ${s.body * 1.25}px ${SANS}`;
    ctx.fillStyle = C.gold;
    const reward = b.reward_btc ? `${b.reward_btc.toFixed(8)} BTC` : "block reward";
    ctx.fillText(reward, M, y);
    const rw = ctx.measureText(reward).width;
    ctx.font = `400 ${s.small}px ${SANS}`;
    ctx.fillStyle = C.dim;
    ctx.fillText("paid straight to the miner", M + rw + s.body * 0.6, y);

    y += s.body * 1.35;
    ctx.font = `400 ${s.small}px ${SANS}`;
    ctx.fillStyle = C.dim;
    const solved = b.share_diff
      ? `solved with a ${formatDifficulty(b.share_diff)} share`
      : "solved by this pool";
    ctx.fillText(
      `${solved} · ${new Date(b.found_at).toLocaleDateString(undefined, {
        day: "numeric", month: "short", year: "numeric",
      })}`,
      M, y,
    );

    // block hash
    y += G(60);
    eyebrow(ctx, "Block hash", M, y, s.eyebrow * 0.92, C.dim);
    y += G(26);
    if (hashLines.length) {
      ctx.font = `500 ${s.mono}px ${MONO}`;
      const zeros = hashText.length - hashText.replace(/^0+/, "").length;
      hexPanel(ctx, hashLines, M, y, innerW, s.mono, {
        fill: C.panel, stroke: "rgba(255,198,92,0.28)", leadingZeros: zeros,
      });
      y += hashPanelH;
    }
    y += G(44);
  } else {
    const share = data.share;
    eyebrow(ctx, "Best share", M, y, s.eyebrow, C.ember);
    y += G(24) + s.hero * 0.78;
    const heroText = formatDifficulty(share.sdiff);
    ctx.font = `700 ${s.hero}px ${SANS}`;
    ctx.fillStyle = C.fg;
    ctx.shadowColor = "rgba(255,122,58,0.45)";
    ctx.shadowBlur = 55;
    ctx.fillText(heroText, M, y);
    ctx.shadowBlur = 0;
    const heroW = ctx.measureText(heroText).width;
    ctx.font = `500 ${s.body * 1.12}px ${SANS}`;
    ctx.fillStyle = C.dim;
    ctx.fillText("difficulty", M + heroW + s.body * 0.6, y);

    y += G(58);
    ctx.font = `400 ${s.body}px ${SANS}`;
    ctx.fillStyle = C.fg;
    ctx.fillText(`≈ ${spellHashes(share.sdiff * 2 ** 32)} to find`, M, y);
    if (gapX > 1) {
      y += s.body * 1.45;
      ctx.font = `400 ${s.small}px ${SANS}`;
      ctx.fillStyle = C.dim;
      const times = gapX >= 1000 ? formatDifficulty(gapX) : gapX.toFixed(1);
      ctx.fillText(`a block needed ${times}× more`, M, y);
    }

    y += G(74);
    eyebrow(ctx, "Block header hash", M, y, s.eyebrow * 0.92, C.dim);
    y += G(26);
    ctx.font = `500 ${s.mono}px ${MONO}`;
    const zeros = hashText.length - hashText.replace(/^0+/, "").length;
    hexPanel(ctx, hashLines, M, y, innerW, s.mono, {
      fill: C.panel, stroke: C.stroke, leadingZeros: zeros,
    });
    y += hashPanelH;

    y += G(46);
    ctx.font = `400 ${s.small}px ${SANS}`;
    ctx.fillStyle = C.dim;
    ctx.fillText(
      `block #${share.height.toLocaleString()} · ${chainLabel(data.chain)} · ` +
      `${new Date(share.seen_at).toLocaleDateString(undefined, {
        day: "numeric", month: "short", year: "numeric",
      })}`,
      M, y,
    );

    y += G(66);
    eyebrow(ctx, "Proof · raw 80-byte header", M, y, s.eyebrow * 0.92, C.ember);
    y += G(26);
    ctx.font = `400 ${s.monoHeader}px ${MONO}`;
    hexPanel(ctx, hdrLines, M, y, innerW, s.monoHeader, {
      fill: "rgba(255,122,58,0.06)", stroke: "rgba(255,122,58,0.28)",
    });
    y += hdrPanelH;

    y += G(42);
    ctx.font = `400 ${s.small}px ${SANS}`;
    ctx.fillStyle = C.dim;
    ctx.fillText("SHA-256 it twice, reverse the bytes, and you get the hash above", M, y);
  }

  // ── footer, pinned
  ctx.strokeStyle = C.stroke;
  ctx.lineWidth = 2;
  ctx.beginPath();
  ctx.moveTo(M, ruleY);
  ctx.lineTo(w - M, ruleY);
  ctx.stroke();
  drawFlame(ctx, M + s.small * 0.5, footY - s.small * 0.42, s.small * 0.72);
  ctx.font = `600 ${s.body * 0.94}px ${SANS}`;
  ctx.fillStyle = C.fg;
  ctx.fillText("Run your own solo pool", M + s.small * 1.5, footY);
  ctx.font = `400 ${s.small}px ${SANS}`;
  ctx.fillStyle = C.ember;
  const cta = "Kamado Pool on StartOS";
  ctx.fillText(cta, w - M - ctx.measureText(cta).width, footY);

  // ── QR, centred above the footer
  if (qrPayload) {
    // Flow position: the slack distribution already sizes the gaps so this
    // lands just above the footer. Clamping to the bottom instead would
    // undo that and reopen a void above the code.
    const qy = Math.min(
      y + Math.max(s.small * 1.25, G(40)),
      ruleY - s.body * 1.0 - qrBlockH,
    );
    const qx = (w - qrSize) / 2;
    try {
      const qc = document.createElement("canvas");
      await QRCode.toCanvas(qc, qrPayload, {
        width: qrSize, margin: 1, errorCorrectionLevel: "L",
        color: { dark: "#0b0e14", light: "#e6e9ef" },
      });
      const pad = qrSize * 0.05;
      ctx.fillStyle = C.fg;
      roundRect(ctx, qx - pad, qy - pad, qrSize + pad * 2, qrSize + pad * 2, 14);
      ctx.fill();
      ctx.drawImage(qc, qx, qy, qrSize, qrSize);

      ctx.textAlign = "center";
      let cy = qy + qrSize + pad + s.body * 1.15;
      ctx.font = `600 ${s.body * 0.94}px ${SANS}`;
      ctx.fillStyle = C.fg;
      ctx.fillText(qrCaption[0], w / 2, cy);
      cy += s.small * 1.5;
      ctx.font = `400 ${s.small}px ${SANS}`;
      ctx.fillStyle = C.dim;
      ctx.fillText(qrCaption[1], w / 2, cy);
      ctx.textAlign = "left";
    } catch {
      /* the hash and header are printed above regardless */
    }
  }
}
