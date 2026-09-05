<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { formatDifficulty } from "../format";
  import type { BestSharePow } from "../types";
  import {
    INIT_H,
    bytesToHex,
    hex8,
    hexToBytes,
    revHex,
    sha256,
    sha256d,
    targetHex,
    diffOfHash,
  } from "./sha256";

  export type CheckResult = { name: string; ok: boolean; detail: string };

  let {
    pow,
    fields,
    onclose,
    oncomplete,
  }: {
    pow: BestSharePow;
    // Header field slices, colour-keyed to match the page's block drawing.
    fields: { key: string; name: string; raw: string }[];
    onclose: () => void;
    oncomplete: (checks: CheckResult[], finalHash: string) => void;
  } = $props();

  // This overlay is mounted for one specific share and traces its entire
  // timeline up front, so the prop is read once, deliberately: rebuilding
  // the trace mid-playback would restart the animation.
  const share = untrack(() => pow);

  // ── Chapters ────────────────────────────────────────────────────────
  const CHAPTERS = [
    "Rebuild the merkle root",
    "Pad the header into message blocks",
    "Initialize the hash state",
    "Compress block 1",
    "Compress block 2",
    "Hash the digest again",
    "Reverse for display",
    "Compare against the target",
  ];

  type PadSeg = { cls: string; hex: string };
  type Step =
    | { kind: "chapter"; ch: number; dwell: number }
    | { kind: "merkle-init"; txid: string; dwell: number }
    | { kind: "merkle-fold"; i: number; total: number; left: string; right: string; result: string; dwell: number }
    | { kind: "merkle-done"; root: string; matches: boolean; dwell: number }
    | { kind: "padding"; dwell: number }
    | { kind: "init-h"; dwell: number }
    | { kind: "schedule"; block: number; blocks: number; pass: number; words: string[]; dwell: number }
    | {
        kind: "round";
        block: number;
        blocks: number;
        pass: number;
        round: number;
        regs: number[];
        hexRegs: string[];
        wt: string;
        kt: string;
        dwell: number;
      }
    | { kind: "add-h"; pass: number; state: string[]; dwell: number }
    | { kind: "digest"; label: string; hex: string; dwell: number }
    | { kind: "reverse"; dwell: number }
    | { kind: "target"; dwell: number }
    | { kind: "verdict" };

  // Pacing. Authored dwells describe the tour at design speed; SLOWDOWN
  // stretches everything uniformly and chapter cards hold an extra beat
  // so each stage lands before its content starts.
  const SLOWDOWN = 3;
  const CHAPTER_EXTRA_MS = 1200;

  // Every round, at one fixed dwell. Earlier versions sampled sparsely
  // and shortened the dwell as the round number rose, which read as the
  // grid accelerating; the avalanche should advance at a steady rate and
  // let the mixing itself provide the drama.
  const ROUND_DWELL = 26;

  function roundKeep(_round: number): { keep: boolean; dwell: number } {
    return { keep: true, dwell: ROUND_DWELL };
  }

  // ── Formulas, as native MathML ──────────────────────────────────────
  // Rendered through {@html}: these are static, authored here, and far
  // more readable as markup strings than as ~200 lines of template. No
  // library, browsers typeset MathML themselves, which keeps the
  // dashboard dependency-free and works offline on a LAN.
  const rot = (n: string) =>
    `<msup><mi mathvariant="normal">ROTR</mi><mn>${n}</mn></msup>`;
  const shr = (n: string) =>
    `<msup><mi mathvariant="normal">SHR</mi><mn>${n}</mn></msup>`;
  /** f(inner), where inner is raw MathML. */
  const call = (f: string, inner: string) =>
    `${f}<mo stretchy="false">(</mo>${inner}<mo stretchy="false">)</mo>`;
  /** f(x), for a single italic variable. */
  const arg = (f: string, x: string) => call(f, `<mi>${x}</mi>`);
  /** W with a t-offset subscript, e.g. W_{t-15}. */
  const wSub = (off: number) =>
    `<msub><mi>W</mi><mrow><mi>t</mi><mo>&#8722;</mo><mn>${off}</mn></mrow></msub>`;
  const sigmaLower = (n: string) =>
    `<msub><mi mathvariant="normal">σ</mi><mn>${n}</mn></msub>`;

  const MATH_ROUND = `<math display="block">
    <mtable columnalign="right center left" rowspacing="0.4em">
      <mtr>
        <mtd><msub><mi>T</mi><mn>1</mn></msub></mtd><mtd><mo>=</mo></mtd>
        <mtd><mi>h</mi><mo>+</mo>${arg('<msub><mi mathvariant="normal">Σ</mi><mn>1</mn></msub>', "e")}<mo>+</mo>
          <mi mathvariant="normal">Ch</mi><mo stretchy="false">(</mo><mi>e</mi><mo>,</mo><mi>f</mi><mo>,</mo><mi>g</mi><mo stretchy="false">)</mo>
          <mo>+</mo><msub><mi>K</mi><mi>t</mi></msub><mo>+</mo><msub><mi>W</mi><mi>t</mi></msub></mtd>
      </mtr>
      <mtr>
        <mtd><msub><mi>T</mi><mn>2</mn></msub></mtd><mtd><mo>=</mo></mtd>
        <mtd>${arg('<msub><mi mathvariant="normal">Σ</mi><mn>0</mn></msub>', "a")}<mo>+</mo>
          <mi mathvariant="normal">Maj</mi><mo stretchy="false">(</mo><mi>a</mi><mo>,</mo><mi>b</mi><mo>,</mo><mi>c</mi><mo stretchy="false">)</mo></mtd>
      </mtr>
    </mtable>
  </math>`;

  const MATH_UPDATE = `<math display="block">
    <mrow>
      <mo stretchy="false">(</mo><mi>a</mi><mo>,</mo><mi>b</mi><mo>,</mo><mi>c</mi><mo>,</mo><mi>d</mi><mo>,</mo>
      <mi>e</mi><mo>,</mo><mi>f</mi><mo>,</mo><mi>g</mi><mo>,</mo><mi>h</mi><mo stretchy="false">)</mo>
      <mo>&#8592;</mo>
      <mo stretchy="false">(</mo><msub><mi>T</mi><mn>1</mn></msub><mo>+</mo><msub><mi>T</mi><mn>2</mn></msub><mo>,</mo>
      <mi>a</mi><mo>,</mo><mi>b</mi><mo>,</mo><mi>c</mi><mo>,</mo>
      <mi>d</mi><mo>+</mo><msub><mi>T</mi><mn>1</mn></msub><mo>,</mo>
      <mi>e</mi><mo>,</mo><mi>f</mi><mo>,</mo><mi>g</mi><mo stretchy="false">)</mo>
    </mrow>
  </math>`;

  const MATH_DEFS = `<math display="block">
    <mtable columnalign="right center left" rowspacing="0.35em">
      <mtr>
        <mtd><mi mathvariant="normal">Ch</mi><mo stretchy="false">(</mo><mi>x</mi><mo>,</mo><mi>y</mi><mo>,</mo><mi>z</mi><mo stretchy="false">)</mo></mtd><mtd><mo>=</mo></mtd>
        <mtd><mo stretchy="false">(</mo><mi>x</mi><mo>&#8743;</mo><mi>y</mi><mo stretchy="false">)</mo><mo>&#8853;</mo>
          <mo stretchy="false">(</mo><mo>&#172;</mo><mi>x</mi><mo>&#8743;</mo><mi>z</mi><mo stretchy="false">)</mo></mtd>
      </mtr>
      <mtr>
        <mtd><mi mathvariant="normal">Maj</mi><mo stretchy="false">(</mo><mi>x</mi><mo>,</mo><mi>y</mi><mo>,</mo><mi>z</mi><mo stretchy="false">)</mo></mtd><mtd><mo>=</mo></mtd>
        <mtd><mo stretchy="false">(</mo><mi>x</mi><mo>&#8743;</mo><mi>y</mi><mo stretchy="false">)</mo><mo>&#8853;</mo>
          <mo stretchy="false">(</mo><mi>x</mi><mo>&#8743;</mo><mi>z</mi><mo stretchy="false">)</mo><mo>&#8853;</mo>
          <mo stretchy="false">(</mo><mi>y</mi><mo>&#8743;</mo><mi>z</mi><mo stretchy="false">)</mo></mtd>
      </mtr>
      <mtr>
        <mtd>${arg('<msub><mi mathvariant="normal">Σ</mi><mn>0</mn></msub>', "x")}</mtd><mtd><mo>=</mo></mtd>
        <mtd>${arg(rot("2"), "x")}<mo>&#8853;</mo>${arg(rot("13"), "x")}<mo>&#8853;</mo>${arg(rot("22"), "x")}</mtd>
      </mtr>
      <mtr>
        <mtd>${arg('<msub><mi mathvariant="normal">Σ</mi><mn>1</mn></msub>', "x")}</mtd><mtd><mo>=</mo></mtd>
        <mtd>${arg(rot("6"), "x")}<mo>&#8853;</mo>${arg(rot("11"), "x")}<mo>&#8853;</mo>${arg(rot("25"), "x")}</mtd>
      </mtr>
    </mtable>
  </math>`;

  const MATH_SCHEDULE = `<math display="block">
    <mtable columnalign="right center left" rowspacing="0.35em">
      <mtr>
        <mtd><msub><mi>W</mi><mi>t</mi></msub></mtd><mtd><mo>=</mo></mtd>
        <mtd>${call(sigmaLower("1"), wSub(2))}<mo>+</mo>${wSub(7)}
          <mo>+</mo>${call(sigmaLower("0"), wSub(15))}<mo>+</mo>${wSub(16)}</mtd>
      </mtr>
      <mtr>
        <mtd>${arg(sigmaLower("0"), "x")}</mtd><mtd><mo>=</mo></mtd>
        <mtd>${arg(rot("7"), "x")}<mo>&#8853;</mo>${arg(rot("18"), "x")}<mo>&#8853;</mo>${arg(shr("3"), "x")}</mtd>
      </mtr>
      <mtr>
        <mtd>${arg(sigmaLower("1"), "x")}</mtd><mtd><mo>=</mo></mtd>
        <mtd>${arg(rot("17"), "x")}<mo>&#8853;</mo>${arg(rot("19"), "x")}<mo>&#8853;</mo>${arg(shr("10"), "x")}</mtd>
      </mtr>
    </mtable>
  </math>`;

  const MATH_MERKLE = `<math display="block">
    <mrow>
      <msub><mi>n</mi><mrow><mi>i</mi><mo>+</mo><mn>1</mn></mrow></msub><mo>=</mo>
      <mi mathvariant="normal">SHA256</mi><mo stretchy="false">(</mo>
      <mi mathvariant="normal">SHA256</mi><mo stretchy="false">(</mo>
      <msub><mi>n</mi><mi>i</mi></msub><mo>&#8214;</mo><msub><mi>b</mi><mi>i</mi></msub>
      <mo stretchy="false">)</mo><mo stretchy="false">)</mo>
    </mrow>
  </math>`;

  const MATH_TARGET = `<math display="block">
    <mrow>
      <mi mathvariant="normal">SHA256d</mi><mo stretchy="false">(</mo><mi>header</mi><mo stretchy="false">)</mo>
      <mo>&lt;</mo><mi mathvariant="normal">target</mi>
      <mo>,</mo><mspace width="1em"/>
      <mi mathvariant="normal">target</mi><mo>=</mo>
      <mfrac>
        <msub><mi mathvariant="normal">target</mi><mn>1</mn></msub>
        <mi>D</mi>
      </mfrac>
    </mrow>
  </math>`;

  // ── Build the timeline from the real computation ────────────────────
  const digest1 = sha256(hexToBytes(share.header));
  const digest1Hex = bytesToHex(digest1);
  const digest2Hex = bytesToHex(sha256(digest1));
  const finalHash = revHex(digest2Hex);
  const shareTarget = targetHex(share.sdiff);
  const netTarget = targetHex(share.netdiff);
  const targetGapX = share.sdiff > 0 ? share.netdiff / share.sdiff : 0;

  function tracePass(input: Uint8Array, pass: number): Step[][] {
    const schedules: Step[][] = [];
    const rounds: Step[][] = [];
    const addhs: Step[] = [];
    sha256(input, {
      schedule: (block, blocks, words) => {
        schedules[block - 1] = [{ kind: "schedule", block, blocks, pass, words, dwell: 1400 }];
      },
      round: (block, blocks, round, regs, wt, kt) => {
        const rk = roundKeep(round);
        if (!rk.keep) return;
        (rounds[block - 1] ??= []).push({
          kind: "round",
          block,
          blocks,
          pass,
          round,
          regs: Array.from(regs, (x) => x >>> 0),
          hexRegs: Array.from(regs, hex8),
          wt: hex8(wt),
          kt: hex8(kt),
          dwell: rk.dwell,
        });
      },
      blockDone: (block, _blocks, state) => {
        addhs[block - 1] = { kind: "add-h", pass, state, dwell: 1200 };
      },
    });
    return schedules.map((sch, i) => [...sch, ...(rounds[i] ?? []), ...(addhs[i] ? [addhs[i]] : [])]);
  }

  // Padding: 80 header bytes + 0x80 + 39 zero bytes + 64-bit length.
  const padBlocks = (() => {
    const segs: PadSeg[] = fields.map((f) => ({ cls: `sg-${f.key}`, hex: f.raw }));
    segs.push({ cls: "sg-pad", hex: "80" + "00".repeat(39) });
    segs.push({ cls: "sg-len", hex: "0000000000000280" });
    const out: PadSeg[][] = [[], []];
    let pos = 0;
    for (const s of segs) {
      const end = pos + s.hex.length;
      if (end <= 128) out[0].push(s);
      else if (pos >= 128) out[1].push(s);
      else {
        out[0].push({ cls: s.cls, hex: s.hex.slice(0, 128 - pos) });
        out[1].push({ cls: s.cls, hex: s.hex.slice(128 - pos) });
      }
      pos = end;
    }
    return out;
  })();

  function buildTimeline(): Step[] {
    const cbTxid = bytesToHex(sha256d(hexToBytes(share.coinbase)));
    const branches = share.merklebranches ?? [];
    const folds: Step[] = [];
    let node = cbTxid;
    const foldDwell = Math.max(300, Math.min(900, Math.floor(3600 / Math.max(branches.length, 1))));
    for (let i = 0; i < branches.length; i++) {
      const buf = new Uint8Array(64);
      buf.set(hexToBytes(node));
      buf.set(hexToBytes(branches[i]), 32);
      const result = bytesToHex(sha256d(buf));
      folds.push({
        kind: "merkle-fold",
        i: i + 1,
        total: branches.length,
        left: node,
        right: branches[i],
        result,
        dwell: foldDwell,
      });
      node = result;
    }
    const rootOk = node === share.header.slice(72, 136);
    const pass1 = tracePass(hexToBytes(share.header), 1);
    const pass2 = tracePass(digest1, 2);

    const ch = (n: number): Step => ({ kind: "chapter", ch: n, dwell: 900 });
    return [
      ch(0),
      { kind: "merkle-init", txid: cbTxid, dwell: 1500 },
      ...folds,
      { kind: "merkle-done", root: node, matches: rootOk, dwell: 1600 },
      ch(1),
      { kind: "padding", dwell: 3200 },
      ch(2),
      { kind: "init-h", dwell: 2000 },
      ch(3),
      ...pass1[0],
      ch(4),
      ...pass1[1],
      { kind: "digest", label: "First digest", hex: digest1Hex, dwell: 1500 },
      ch(5),
      ...pass2[0].filter((s) => s.kind !== "add-h"),
      { kind: "digest", label: "Double-SHA-256 digest", hex: digest2Hex, dwell: 1500 },
      ch(6),
      { kind: "reverse", dwell: 2000 },
      ch(7),
      { kind: "target", dwell: 4000 },
      { kind: "verdict" },
    ];
  }

  function computeChecks(): CheckResult[] {
    const got = revHex(bytesToHex(sha256d(hexToBytes(share.header))));
    let node = sha256d(hexToBytes(share.coinbase));
    const cbTxid = revHex(bytesToHex(node));
    for (const b of share.merklebranches ?? []) {
      const buf = new Uint8Array(64);
      buf.set(node);
      buf.set(hexToBytes(b), 32);
      node = sha256d(buf);
    }
    const rootInternal = bytesToHex(node);
    const computedDiff = diffOfHash(got);
    const ratio = share.sdiff > 0 ? computedDiff / share.sdiff : 0;
    return [
      {
        name: "SHA-256d(header) matches the recorded share hash",
        ok: got === share.hash,
        detail: got,
      },
      {
        name: "Coinbase + merkle branches reproduce the header's merkle root",
        ok: rootInternal === share.header.slice(72, 136),
        detail: `coinbase txid ${cbTxid} → root ${revHex(rootInternal)}`,
      },
      {
        name: "Hash value corresponds to the recorded difficulty",
        ok: ratio > 0.999 && ratio < 1.001,
        detail: `computed ${formatDifficulty(computedDiff)}, recorded ${formatDifficulty(share.sdiff)}`,
      },
    ];
  }

  // ── Playback ────────────────────────────────────────────────────────
  const timeline = buildTimeline();
  const checks = computeChecks();
  const allOk = checks.every((c) => c.ok);

  let index = $state(0);
  let paused = $state(false);
  let speed = $state(1);
  let done = $state(false);
  let timer: ReturnType<typeof setTimeout> | null = null;

  const step = $derived(timeline[Math.min(index, timeline.length - 1)]);
  const chapter = $derived.by(() => {
    let c = 0;
    for (let i = 0; i <= Math.min(index, timeline.length - 1); i++) {
      const s = timeline[i];
      if (s.kind === "chapter") c = s.ch;
    }
    return c;
  });
  const overallPct = $derived((index / Math.max(timeline.length - 1, 1)) * 100);

  const reduceMotion =
    typeof window !== "undefined" &&
    !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

  function schedule(i: number): void {
    const s = timeline[i];
    if (!s || s.kind === "verdict") {
      finish();
      return;
    }
    const dwell = s.dwell * SLOWDOWN + (s.kind === "chapter" ? CHAPTER_EXTRA_MS : 0);
    timer = setTimeout(() => {
      index = i + 1;
      schedule(i + 1);
    }, dwell / speed);
  }

  function finish(): void {
    if (timer) clearTimeout(timer);
    timer = null;
    index = timeline.length - 1;
    done = true;
    oncomplete(checks, finalHash);
  }

  function togglePause(): void {
    if (done) return;
    paused = !paused;
    if (paused) {
      if (timer) clearTimeout(timer);
      timer = null;
    } else {
      schedule(index);
    }
  }

  function cycleSpeed(): void {
    speed = speed === 1 ? 2 : speed === 2 ? 4 : 1;
  }

  function onKey(ev: KeyboardEvent): void {
    if (ev.key === "Escape") {
      ev.preventDefault();
      onclose();
    } else if (ev.key === " ") {
      ev.preventDefault();
      togglePause();
    }
  }

  // Start, and lock background scroll while the overlay owns the screen.
  $effect(() => {
    if (reduceMotion) {
      finish();
      return;
    }
    schedule(0);
    return () => {
      if (timer) clearTimeout(timer);
    };
  });
  $effect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  });
  onDestroy(() => {
    if (timer) clearTimeout(timer);
  });

  // ── Bit-grid canvas: the 256-bit working state, live ─────────────────
  let canvasEl: HTMLCanvasElement | undefined = $state();
  let prevRegs: number[] | null = null;
  let lastDrawnRound = -1;

  $effect(() => {
    const s = step;
    if (!canvasEl || !s || s.kind !== "round") {
      if (s && s.kind !== "round") {
        prevRegs = null;
        lastDrawnRound = -1;
      }
      return;
    }
    if (s.round === lastDrawnRound) return;
    drawBits(canvasEl, s.regs, prevRegs);
    prevRegs = s.regs;
    lastDrawnRound = s.round;
  });

  function drawBits(canvas: HTMLCanvasElement, regs: number[], prev: number[] | null): void {
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const cssW = canvas.clientWidth || 640;
    const cssH = canvas.clientHeight || 160;
    if (canvas.width !== Math.floor(cssW * dpr) || canvas.height !== Math.floor(cssH * dpr)) {
      canvas.width = Math.floor(cssW * dpr);
      canvas.height = Math.floor(cssH * dpr);
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cssW, cssH);

    const cols = 32;
    const rows = 8;
    const cw = cssW / cols;
    const chh = cssH / rows;
    const gap = Math.max(1, Math.min(cw, chh) * 0.14);

    for (let r = 0; r < rows; r++) {
      const v = regs[r] >>> 0;
      const pv = prev ? prev[r] >>> 0 : v;
      for (let c = 0; c < cols; c++) {
        const bit = (v >>> (31 - c)) & 1;
        const changed = prev !== null && bit !== ((pv >>> (31 - c)) & 1);
        const x = c * cw + gap;
        const y = r * chh + gap;
        const w = cw - gap * 2;
        const h = chh - gap * 2;
        if (changed) {
          // A bit that just flipped: hot, with a bloom halo.
          ctx.fillStyle = "rgba(255, 214, 170, 0.28)";
          ctx.fillRect(x - gap, y - gap, w + gap * 2, h + gap * 2);
          ctx.fillStyle = bit ? "#ffd9a8" : "#8a5a3a";
        } else {
          ctx.fillStyle = bit ? "rgba(255, 122, 58, 0.85)" : "rgba(120, 140, 170, 0.13)";
        }
        ctx.fillRect(x, y, w, h);
      }
    }
  }

  const finalChars = $derived.by(() => {
    let lead = true;
    return finalHash.split("").map((c) => {
      if (c !== "0") lead = false;
      return { c, zero: lead };
    });
  });
</script>

<svelte:window onkeydown={onKey} />

<div class="overlay" role="dialog" aria-modal="true" aria-label="Proof of work verification">
  <div class="embers" aria-hidden="true">
    {#each { length: 14 } as _, i}
      <span class="ember" style="--i:{i}"></span>
    {/each}
  </div>

  <header class="ov-head">
    <div class="ov-id">
      <span class="ov-eyebrow">Proof of Work</span>
      <span class="ov-block">Block #{share.height.toLocaleString()} &middot; {formatDifficulty(share.sdiff)} share</span>
    </div>
    <button type="button" class="ov-close" onclick={onclose} aria-label="Close">&#10005;</button>
  </header>

  <div class="rail" aria-hidden="true">
    {#each CHAPTERS as title, i}
      <div class="rail-seg" class:done={i < chapter} class:active={i === chapter}>
        <span class="rail-bar"></span>
        <span class="rail-label">{title}</span>
      </div>
    {/each}
  </div>

  <main class="stage">
    {#if step.kind === "chapter"}
      <div class="splash">
        <div class="splash-num">{step.ch + 1} <span>/ {CHAPTERS.length}</span></div>
        <h2 class="splash-title">{CHAPTERS[step.ch]}</h2>
      </div>
    {:else if step.kind === "merkle-init"}
      <div class="panel">
        <div class="lede">The coinbase transaction this pool built, hashed twice, gives its txid:</div>
        <div class="hexline big glow">{step.txid}</div>
        <div class="foot-note">internal byte order, the form used while hashing</div>
      </div>
    {:else if step.kind === "merkle-fold"}
      <div class="panel">
        <div class="lede">
          Level {step.i} of {step.total}, join with the branch hash and hash the pair
        </div>
        <div class="fold">
          <div class="fold-row"><span class="tag">current</span><span class="hexline">{step.left}</span></div>
          <div class="fold-row"><span class="tag">branch {step.i}</span><span class="hexline dim">{step.right}</span></div>
          <div class="fold-op">SHA-256d &darr;</div>
          <div class="fold-row"><span class="tag good">result</span><span class="hexline glow">{step.result}</span></div>
        </div>
        <div class="math small">{@html MATH_MERKLE}</div>
        <div class="foot-note">
          each branch hash <i>b</i> stands in for every other transaction in its subtree
        </div>
      </div>
    {:else if step.kind === "merkle-done"}
      <div class="panel">
        <div class="lede">Merkle root reproduced from the coinbase alone:</div>
        <div class="hexline big glow">{step.root}</div>
        <div class="verdict-line" class:ok={step.matches} class:bad={!step.matches}>
          {step.matches
            ? "matches the merkle root committed in the header"
            : "does NOT match the header's merkle root"}
        </div>
      </div>
    {:else if step.kind === "padding"}
      <div class="panel wide">
        <div class="lede">
          80 header bytes, a <span class="sg-pad">0x80</span> marker, 39 zero bytes and the
          <span class="sg-len">640-bit</span> length, exactly two 512-bit blocks
        </div>
        {#each padBlocks as blk, bi}
          <div class="msgblock">
            <div class="msgblock-label">message block {bi + 1} &middot; 512 bits</div>
            <div class="hexline wrap">
              {#each blk as s, i (i)}<span class={s.cls}>{s.hex}</span>{/each}
            </div>
          </div>
        {/each}
        <div class="legend">
          {#each fields as f (f.key)}
            <span class="lg"><i class="sw sw-{f.key}"></i> {f.name}</span>
          {/each}
          <span class="lg"><i class="sw sw-pad"></i> padding</span>
          <span class="lg"><i class="sw sw-len"></i> message length</span>
        </div>
        <div class="foot-note">
          the <span class="sg-merkle">merkle root</span> straddles the boundary, SHA-256 takes
          64 bytes at a time, indifferent to where fields begin and end
        </div>
      </div>
    {:else if step.kind === "init-h"}
      <div class="panel">
        <div class="lede">Eight 32-bit registers, seeded from the SHA-256 constants:</div>
        <div class="regs">
          {#each INIT_H as v, i (i)}
            <div class="reg"><span class="reg-name">H{i}</span><span class="reg-val">{v}</span></div>
          {/each}
        </div>
        <div class="foot-note">
          the fractional parts of the square roots of the first eight primes, "nothing up
          my sleeve" numbers, chosen so nobody could have picked them to hide a weakness
        </div>
      </div>
    {:else if step.kind === "schedule"}
      <div class="panel wide">
        <div class="lede">
          Block {step.block} of {step.blocks} expands into sixteen 32-bit words
        </div>
        <div class="words">
          {#each step.words as w, i (i)}
            <div class="word"><span class="word-i">w{i}</span>{w}</div>
          {/each}
        </div>
        <div class="math small">{@html MATH_SCHEDULE}</div>
        <div class="foot-note">
          rounds 17&ndash;64 consume words expanded from the first sixteen by this recurrence
        </div>
      </div>
    {:else if step.kind === "round"}
      <div class="panel wide">
        <div class="round-head">
          <span class="round-pass">pass {step.pass} &middot; block {step.block}/{step.blocks}</span>
          <span class="round-no">round <b>{String(step.round).padStart(2, "0")}</b> / 64</span>
          <span class="round-in">W[t] {step.wt} &nbsp; K[t] {step.kt}</span>
        </div>
        <div class="round-bar"><div class="round-fill" style="width:{(step.round / 64) * 100}%"></div></div>
        <div class="bitwrap">
          <canvas bind:this={canvasEl} class="bits"></canvas>
          <div class="bit-rows" aria-hidden="true">
            {#each "abcdefgh".split("") as n, i (i)}<span>{n}</span>{/each}
          </div>
        </div>
        <div class="legend">
          <span class="lg"><i class="sw sw-one"></i> bit is 1</span>
          <span class="lg"><i class="sw sw-zero"></i> bit is 0</span>
          <span class="lg"><i class="sw sw-flip"></i> flipped on this round</span>
          <span class="lg">rows are registers <i>a</i>&ndash;<i>h</i>, 32 bits each</span>
        </div>
        <div class="regs tight">
          {#each step.hexRegs as r, i (i)}
            <div class="reg" class:fresh={i === 0 || i === 4}>
              <span class="reg-name">{"abcdefgh"[i]}</span><span class="reg-val">{r}</span>
            </div>
          {/each}
        </div>
        <div class="mathgrid">
          <div class="mathcol">
            <div class="math">{@html MATH_ROUND}</div>
            <div class="math small">{@html MATH_UPDATE}</div>
          </div>
          <div class="mathcol defs">
            <div class="math small">{@html MATH_DEFS}</div>
          </div>
        </div>
        <div class="foot-note">
          every lit cell above is a real bit of this share's working state
        </div>
      </div>
    {:else if step.kind === "add-h"}
      <div class="panel">
        <div class="lede">
          64 rounds done, the result is <em>added</em> into the running state, chaining
          this block into the next
        </div>
        <div class="regs">
          {#each step.state as v, i (i)}
            <div class="reg"><span class="reg-name">H{i}</span><span class="reg-val">{v}</span></div>
          {/each}
        </div>
        <div class="foot-note">
          addition, not replacement, the Davies&ndash;Meyer construction, which is what makes
          the compression one-way
        </div>
      </div>
    {:else if step.kind === "digest"}
      <div class="panel">
        <div class="lede">{step.label}</div>
        <div class="hexline big glow">{step.hex}</div>
      </div>
    {:else if step.kind === "reverse"}
      <div class="panel">
        <div class="lede">Bitcoin prints hashes in reverse byte order</div>
        <div class="hexline big dim">{digest2Hex}</div>
        <div class="flip">&darr; reverse all 32 bytes</div>
        <div class="hexline big">
          {#each finalChars as ch}<span class:fz={ch.zero}>{ch.c}</span>{/each}
        </div>
        <div class="legend">
          <span class="lg"><i class="sw sw-zeroes"></i> leading zeros, the visible sign of the work done</span>
        </div>
      </div>
    {:else if step.kind === "target"}
      <div class="panel wide">
        <div class="targets">
          <div class="trow">
            <span class="tag">your hash</span>
            <span class="hexline">{#each finalChars as ch}<span class:fz={ch.zero}>{ch.c}</span>{/each}</span>
          </div>
          <div class="trow">
            <span class="tag">share target</span><span class="hexline dim">{shareTarget}</span>
          </div>
          <div class="trow">
            <span class="tag">network target</span><span class="hexline dim">{netTarget}</span>
          </div>
        </div>
        <div class="legend">
          <span class="lg"><i class="sw sw-zeroes"></i> leading zeros of your hash</span>
          <span class="lg">a hash counts when it is numerically <em>below</em> the target</span>
        </div>
        <div class="math small">{@html MATH_TARGET}</div>
        <div class="foot-note">
          the hash is below the share target, that is what made it a
          {formatDifficulty(share.sdiff)} share. The network target sits another
          <b>{targetGapX >= 1000 ? formatDifficulty(targetGapX) : targetGapX.toFixed(1)}×</b>
          lower: clear that, and this exact header would have been block
          #{share.height.toLocaleString()}.
        </div>
      </div>
    {:else}
      <div class="panel final">
        <div class="final-badge" class:ok={allOk} class:bad={!allOk}>
          {allOk ? "Proof of work verified" : "Verification failed"}
        </div>
        <div class="hexline huge">
          {#each finalChars as ch}<span class:fz={ch.zero}>{ch.c}</span>{/each}
        </div>
        <ul class="checks">
          {#each checks as c, i (c.name)}
            <li class:ok={c.ok} style="--d:{i * 0.12}s">
              <span class="mark">{c.ok ? "✓" : "✗"}</span>
              <span><span class="cname">{c.name}</span><span class="cdetail">{c.detail}</span></span>
            </li>
          {/each}
        </ul>
        <button type="button" class="final-close" onclick={onclose}>Close</button>
      </div>
    {/if}
  </main>

  <footer class="ov-foot">
    <div class="ov-progress"><div class="ov-progress-fill" style="width:{overallPct}%"></div></div>
    <div class="ov-controls">
      <button type="button" onclick={togglePause} disabled={done}>
        {paused ? "▶ Play" : "❚❚ Pause"}
      </button>
      <button type="button" onclick={cycleSpeed} disabled={done}>{speed}×</button>
      <button type="button" onclick={finish} disabled={done}>Skip to result</button>
      <span class="hint">Esc closes &middot; Space pauses</span>
    </div>
  </footer>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 2000;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background:
      radial-gradient(ellipse 1300px 900px at 50% -12%, rgba(255, 110, 40, 0.20), transparent 62%),
      radial-gradient(circle 700px at 8% 96%, rgba(190, 45, 20, 0.16), transparent 70%),
      radial-gradient(circle 600px at 96% 20%, rgba(40, 110, 80, 0.10), transparent 70%),
      #05070c;
    color: var(--fg);
    animation: ov-in 0.45s ease-out;
  }
  @keyframes ov-in {
    from { opacity: 0; }
    to { opacity: 1; }
  }

  /* Drifting embers, pure decoration, cheap. */
  .embers {
    position: absolute;
    inset: 0;
    pointer-events: none;
    overflow: hidden;
  }
  .ember {
    position: absolute;
    left: calc(var(--i) * 7.4% + 2%);
    bottom: -6vh;
    width: 3px;
    height: 3px;
    border-radius: 50%;
    background: rgba(255, 150, 80, 0.85);
    box-shadow: 0 0 10px 2px rgba(255, 120, 50, 0.55);
    animation: rise calc(16s + var(--i) * 1.7s) linear infinite;
    animation-delay: calc(var(--i) * -1.9s);
    opacity: 0;
  }
  @keyframes rise {
    0% { transform: translateY(0) translateX(0); opacity: 0; }
    12% { opacity: 0.75; }
    80% { opacity: 0.5; }
    100% { transform: translateY(-108vh) translateX(28px); opacity: 0; }
  }

  /* ── Header ── */
  .ov-head {
    position: relative;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
    padding: 1.1rem 1.6rem 0.6rem;
    flex-shrink: 0;
  }
  .ov-id {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .ov-eyebrow {
    font-size: 0.82rem;
    text-transform: uppercase;
    letter-spacing: 0.22em;
    color: var(--accent);
  }
  .ov-block {
    font-size: 1.1rem;
    color: var(--fg-dim);
  }
  .ov-close {
    font: inherit;
    font-size: 1.1rem;
    line-height: 1;
    color: var(--fg-dim);
    background: transparent;
    border: 1px solid rgba(255, 255, 255, 0.14);
    border-radius: 8px;
    padding: 0.45em 0.6em;
    cursor: pointer;
    transition: color 0.15s, border-color 0.15s;
  }
  .ov-close:hover {
    color: var(--fg);
    border-color: var(--accent);
  }

  /* ── Chapter rail ── */
  .rail {
    position: relative;
    display: flex;
    gap: 0.5rem;
    padding: 0.4rem 1.6rem 0.9rem;
    flex-shrink: 0;
  }
  .rail-seg {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .rail-bar {
    height: 3px;
    border-radius: 2px;
    background: rgba(255, 255, 255, 0.12);
    transition: background 0.4s, box-shadow 0.4s;
  }
  .rail-seg.done .rail-bar {
    background: rgba(255, 122, 58, 0.55);
  }
  .rail-seg.active .rail-bar {
    background: var(--accent);
    box-shadow: 0 0 14px rgba(255, 122, 58, 0.75);
  }
  .rail-label {
    font-size: 0.75rem;
    letter-spacing: 0.03em;
    color: rgba(230, 233, 239, 0.32);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    transition: color 0.4s;
  }
  .rail-seg.active .rail-label {
    color: var(--fg);
  }
  .rail-seg.done .rail-label {
    color: rgba(230, 233, 239, 0.55);
  }

  /* ── Stage ── */
  .stage {
    position: relative;
    flex: 1;
    min-height: 0;
    display: flex;
    /* `safe` matters: a flex item taller than its container, centred,
       overflows equally in both directions and the top half becomes
       unreachable, which hid register row `a` until the window was
       tall enough. Safe alignment falls back to start on overflow.
       The unprefixed value is declared first for older engines. */
    align-items: center;
    align-items: safe center;
    justify-content: center;
    justify-content: safe center;
    padding: 0.9rem 1.6rem;
    overflow-y: auto;
  }
  .panel {
    width: 100%;
    max-width: 1040px;
    animation: panel-in 0.4s cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  .panel.wide {
    max-width: 1480px;
  }
  @keyframes panel-in {
    from { opacity: 0; transform: translateY(10px) scale(0.99); }
    to { opacity: 1; transform: none; }
  }
  .splash {
    text-align: center;
    animation: splash-in 0.5s cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  @keyframes splash-in {
    from { opacity: 0; transform: translateY(18px); letter-spacing: 0.3em; }
    to { opacity: 1; transform: none; }
  }
  .splash-num {
    font-size: 0.95rem;
    letter-spacing: 0.3em;
    color: var(--accent);
    margin-bottom: 0.9rem;
  }
  .splash-num span {
    color: var(--fg-dim);
  }
  .splash-title {
    margin: 0;
    font-size: clamp(1.9rem, 5.5vw, 4rem);
    font-weight: 600;
    line-height: 1.15;
    text-shadow: 0 0 40px rgba(255, 122, 58, 0.28);
  }

  .lede {
    font-size: clamp(1.05rem, 1.9vw, 1.5rem);
    color: var(--fg);
    margin-bottom: 1.2rem;
    line-height: 1.5;
  }
  .foot-note {
    margin-top: 1.1rem;
    font-size: clamp(0.9rem, 1.25vw, 1.12rem);
    line-height: 1.6;
    color: var(--fg-dim);
  }
  .foot-note.formula {
    font-family: "JetBrains Mono", "Fira Code", ui-monospace, monospace;
    font-size: 0.74rem;
  }
  .foot-note b {
    color: var(--fg);
  }

  .hexline {
    font-family: "JetBrains Mono", "Fira Code", ui-monospace, monospace;
    word-break: break-all;
    line-height: 1.65;
    letter-spacing: 0.04em;
    font-size: clamp(0.95rem, 1.7vw, 1.35rem);
  }
  .hexline.big {
    font-size: clamp(1.05rem, 2.5vw, 1.85rem);
  }
  .hexline.huge {
    font-size: clamp(1.15rem, 3.4vw, 2.5rem);
    line-height: 1.5;
  }
  .hexline.dim {
    color: var(--fg-dim);
  }
  .hexline.glow {
    color: var(--good);
    text-shadow: 0 0 18px rgba(92, 224, 168, 0.4);
  }
  .fz {
    color: var(--good);
    text-shadow: 0 0 16px rgba(92, 224, 168, 0.55);
  }

  /* Formulas */
  .math {
    margin: 0.4rem 0 0.2rem;
    color: var(--fg);
    overflow-x: auto;
  }
  .math :global(math) {
    font-size: clamp(1.05rem, 1.85vw, 1.5rem);
    color: var(--fg);
  }
  .math.small :global(math) {
    font-size: clamp(0.85rem, 1.25vw, 1.05rem);
    color: rgba(230, 233, 239, 0.78);
  }
  /* Operators and numerals read better slightly dimmed against the
     italic variables they join. */
  .math :global(mo) {
    color: rgba(230, 233, 239, 0.62);
    padding: 0 0.12em;
  }
  .math :global(mn) {
    color: var(--warn);
  }
  .math :global(mi) {
    color: var(--fg);
  }
  .mathgrid {
    display: grid;
    grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
    gap: 0.4rem 2rem;
    align-items: start;
    margin-top: 0.7rem;
    padding-top: 0.7rem;
    border-top: 1px solid rgba(255, 255, 255, 0.09);
  }
  .mathcol {
    min-width: 0;
  }
  .mathcol.defs {
    border-left: 1px solid rgba(255, 255, 255, 0.09);
    padding-left: 1.6rem;
  }

  /* Colour legends */
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem 1.3rem;
    margin-top: 0.7rem;
    font-size: clamp(0.8rem, 1.1vw, 0.98rem);
    color: var(--fg-dim);
  }
  .lg {
    display: inline-flex;
    align-items: center;
    gap: 0.45em;
  }
  .sw {
    width: 0.85em;
    height: 0.85em;
    border-radius: 3px;
    flex-shrink: 0;
  }
  .sw-version { background: #7aa2f7; }
  .sw-prev { background: var(--accent); }
  .sw-merkle { background: var(--good); }
  .sw-time { background: var(--warn); }
  .sw-bits { background: #bb9af7; }
  .sw-nonce { background: var(--bad); }
  .sw-pad { background: rgba(230, 233, 239, 0.42); }
  .sw-len {
    background: transparent;
    border-bottom: 2px dotted #e6e9ef;
    border-radius: 0;
    height: 0.7em;
  }
  .sw-one { background: rgba(255, 122, 58, 0.85); }
  .sw-zero { background: rgba(120, 140, 170, 0.16); }
  .sw-flip {
    background: #ffd9a8;
    box-shadow: 0 0 7px 2px rgba(255, 214, 170, 0.5);
  }
  .sw-zeroes {
    background: var(--good);
    box-shadow: 0 0 7px rgba(92, 224, 168, 0.5);
  }

  /* Merkle fold */
  .fold {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .fold-row {
    display: grid;
    grid-template-columns: 6.5em 1fr;
    gap: 0.9rem;
    align-items: baseline;
  }
  .fold-op {
    color: var(--accent);
    font-size: 0.8rem;
    letter-spacing: 0.12em;
    padding-left: 7.4em;
  }
  .tag {
    font-size: 0.82rem;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--fg-dim);
    white-space: nowrap;
  }
  .tag.good {
    color: var(--good);
  }
  .verdict-line {
    margin-top: 1.2rem;
    font-size: clamp(1.05rem, 1.8vw, 1.45rem);
    font-weight: 600;
  }
  .verdict-line.ok {
    color: var(--good);
  }
  .verdict-line.bad {
    color: var(--bad);
  }

  /* Padding blocks */
  .msgblock {
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 10px;
    overflow: hidden;
    margin-bottom: 0.7rem;
    background: rgba(255, 255, 255, 0.02);
  }
  .msgblock-label {
    padding: 0.4rem 1rem;
    font-size: 0.78rem;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--fg-dim);
    background: rgba(255, 255, 255, 0.04);
  }
  .hexline.wrap {
    padding: 0.8rem 1rem;
    font-size: clamp(0.75rem, 1.45vw, 1.12rem);
  }
  .sg-version { color: #7aa2f7; }
  .sg-prev { color: var(--accent); }
  .sg-merkle { color: var(--good); }
  .sg-time { color: var(--warn); }
  .sg-bits { color: #bb9af7; }
  .sg-nonce { color: var(--bad); }
  .sg-pad { color: rgba(230, 233, 239, 0.42); }
  .sg-len { color: #e6e9ef; text-decoration: underline dotted; text-underline-offset: 3px; }

  /* Registers */
  .regs {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 0.5rem;
  }
  .regs.tight {
    gap: 0.4rem;
    margin-top: 0.75rem;
  }
  .reg {
    display: flex;
    align-items: center;
    gap: 0.55em;
    background: rgba(255, 255, 255, 0.04);
    border: 1px solid rgba(255, 255, 255, 0.09);
    border-radius: 8px;
    padding: 0.42rem 0.6rem;
    min-width: 0;
  }
  .reg.fresh {
    border-color: rgba(255, 122, 58, 0.5);
    box-shadow: 0 0 14px rgba(255, 122, 58, 0.16);
  }
  .reg-name {
    color: var(--accent);
    font-weight: 700;
    font-size: clamp(0.9rem, 1.3vw, 1.1rem);
    min-width: 1.4em;
  }
  .reg-val {
    font-family: "JetBrains Mono", "Fira Code", ui-monospace, monospace;
    font-size: clamp(0.88rem, 1.55vw, 1.25rem);
    letter-spacing: 0.05em;
    white-space: nowrap;
    overflow: hidden;
  }

  /* Message schedule */
  .words {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 0.4rem;
  }
  .word {
    display: flex;
    align-items: center;
    gap: 0.5em;
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 8px;
    padding: 0.3rem 0.55rem;
    font-family: "JetBrains Mono", "Fira Code", ui-monospace, monospace;
    font-size: clamp(0.82rem, 1.45vw, 1.15rem);
    white-space: nowrap;
    overflow: hidden;
  }
  .word-i {
    color: var(--fg-dim);
    font-size: 0.8rem;
    min-width: 2.3em;
  }

  /* Round view */
  .round-head {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem 1.4rem;
    align-items: baseline;
    font-size: clamp(0.9rem, 1.4vw, 1.15rem);
    color: var(--fg-dim);
    margin-bottom: 0.6rem;
  }
  .round-no b {
    color: var(--fg);
    font-size: clamp(1.05rem, 1.7vw, 1.4rem);
  }
  .round-in {
    font-family: "JetBrains Mono", "Fira Code", ui-monospace, monospace;
    font-size: clamp(0.85rem, 1.3vw, 1.05rem);
  }
  .round-bar {
    height: 4px;
    border-radius: 2px;
    background: rgba(255, 255, 255, 0.1);
    overflow: hidden;
    margin-bottom: 0.9rem;
  }
  .round-fill {
    height: 100%;
    background: linear-gradient(90deg, var(--accent-dim), var(--accent));
    box-shadow: 0 0 12px rgba(255, 122, 58, 0.6);
    transition: width 0.15s linear;
  }
  .bitwrap {
    position: relative;
    padding-left: 1.7rem;
  }
  .bits {
    display: block;
    width: 100%;
    height: clamp(118px, 23vh, 300px);
    border-radius: 8px;
    background: rgba(0, 0, 0, 0.25);
  }
  .bit-rows {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 1.5rem;
    display: flex;
    flex-direction: column;
    justify-content: space-around;
    font-size: 0.95rem;
    font-weight: 700;
    color: var(--accent);
    text-align: center;
  }

  /* Targets */
  .targets {
    display: flex;
    flex-direction: column;
    gap: 0.7rem;
  }
  .trow {
    display: grid;
    grid-template-columns: 8em 1fr;
    gap: 0.9rem;
    align-items: baseline;
  }

  /* Final */
  .panel.final {
    text-align: center;
  }
  .final-badge {
    display: inline-block;
    font-size: clamp(1.5rem, 4.2vw, 3rem);
    font-weight: 700;
    letter-spacing: 0.04em;
    margin-bottom: 1.2rem;
    animation: badge-in 0.7s cubic-bezier(0.16, 1, 0.3, 1);
  }
  .final-badge.ok {
    color: var(--good);
    text-shadow: 0 0 34px rgba(92, 224, 168, 0.55);
  }
  .final-badge.bad {
    color: var(--bad);
  }
  @keyframes badge-in {
    from { opacity: 0; transform: scale(0.86); letter-spacing: 0.4em; }
    to { opacity: 1; transform: none; }
  }
  .checks {
    list-style: none;
    margin: 1.6rem auto 0;
    padding: 0;
    max-width: 860px;
    display: flex;
    flex-direction: column;
    gap: 0.7rem;
    text-align: left;
  }
  .checks li {
    display: flex;
    gap: 0.7em;
    line-height: 1.5;
    font-size: clamp(0.95rem, 1.35vw, 1.15rem);
    opacity: 0;
    animation: check-in 0.45s ease-out forwards;
    animation-delay: var(--d);
  }
  @keyframes check-in {
    from { opacity: 0; transform: translateX(-8px); }
    to { opacity: 1; transform: none; }
  }
  .checks .mark {
    flex-shrink: 0;
    font-weight: 700;
    color: var(--bad);
  }
  .checks li.ok .mark {
    color: var(--good);
  }
  .cname {
    display: block;
  }
  .cdetail {
    display: block;
    font-family: "JetBrains Mono", "Fira Code", ui-monospace, monospace;
    font-size: clamp(0.82rem, 1.15vw, 1rem);
    color: var(--fg-dim);
    word-break: break-all;
    margin-top: 0.15rem;
  }
  .final-close {
    font: inherit;
    margin-top: 1.6rem;
    font-weight: 600;
    color: var(--bg);
    background: var(--accent);
    border: none;
    border-radius: 8px;
    padding: 0.6em 1.6em;
    cursor: pointer;
  }
  .final-close:hover {
    filter: brightness(1.1);
  }

  /* ── Footer ── */
  .ov-foot {
    position: relative;
    flex-shrink: 0;
    padding: 0.7rem 1.6rem 1.1rem;
  }
  .ov-progress {
    height: 2px;
    background: rgba(255, 255, 255, 0.1);
    border-radius: 1px;
    overflow: hidden;
    margin-bottom: 0.75rem;
  }
  .ov-progress-fill {
    height: 100%;
    background: var(--accent);
    box-shadow: 0 0 10px rgba(255, 122, 58, 0.7);
    transition: width 0.25s ease;
  }
  .ov-controls {
    display: flex;
    align-items: center;
    gap: 0.55rem;
    flex-wrap: wrap;
  }
  .ov-controls button {
    font: inherit;
    font-size: 0.92rem;
    color: var(--fg-dim);
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.12);
    border-radius: 7px;
    padding: 0.34em 0.85em;
    cursor: pointer;
    transition: color 0.15s, border-color 0.15s;
  }
  .ov-controls button:hover:not(:disabled) {
    color: var(--fg);
    border-color: var(--accent);
  }
  .ov-controls button:disabled {
    opacity: 0.35;
    cursor: default;
  }
  .hint {
    margin-left: auto;
    font-size: 0.85rem;
    color: rgba(230, 233, 239, 0.35);
  }

  @media (max-width: 900px) {
    .rail-label { display: none; }
    .regs, .words { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .mathgrid { grid-template-columns: 1fr; gap: 0.2rem; }
    .mathcol.defs { border-left: none; padding-left: 0; }
  }
  @media (max-width: 560px) {
    .ov-head, .rail, .stage, .ov-foot { padding-left: 0.9rem; padding-right: 0.9rem; }
    .fold-row, .trow { grid-template-columns: 1fr; gap: 0.15rem; }
    .fold-op { padding-left: 0; }
    .hint { display: none; }
    .bits { height: 22vh; }
  }
</style>
