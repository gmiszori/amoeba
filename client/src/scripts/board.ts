import type { Cell, Mark } from "./protocol";

const MIN_SCALE = 12; // CSS px per cell
const MAX_SCALE = 120;

interface Theme {
  bg: string;
  grid: string;
  lastMove: string;
  X: string;
  O: string;
  previewX: string;
  previewO: string;
}

interface Point {
  x: number;
  y: number;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/**
 * Draws the infinite board on a canvas and turns pointer input into pans,
 * zooms and cell taps. Cell (x, y) covers world coordinates [x, x+1) × [y, y+1);
 * the camera is the world point at the centre of the screen.
 */
export class BoardView {
  /** Called when a cell is tapped or clicked without dragging. */
  onTap: (cell: Cell, pointerType: string) => void = () => {};
  /** Called when a mouse hovers over a cell (null when it leaves the board). */
  onHover: (cell: Cell | null) => void = () => {};

  private readonly ctx: CanvasRenderingContext2D;
  private width = 0;
  private height = 0;
  private dpr = 1;
  private cx = 0.5;
  private cy = 0.5;
  private scale = 40;
  private sized = false;
  private theme!: Theme;
  private frame = 0;

  private moves: Cell[] = [];
  private line: Cell[] | null = null;
  private winner: Mark | null = null;
  private preview: { cell: Cell; mark: Mark } | null = null;

  private readonly pointers = new Map<number, Point>();
  private tap: (Point & { pointerType: string }) | null = null;
  private panAnchor = { sx: 0, sy: 0, cx: 0, cy: 0 };
  private pinch = { dist: 1, scale: 1, wx: 0, wy: 0 };

  constructor(private readonly canvas: HTMLCanvasElement) {
    this.ctx = canvas.getContext("2d")!;
    this.readTheme();
    matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
      this.readTheme();
      this.draw();
    });
    new ResizeObserver(() => this.resize()).observe(canvas);

    canvas.addEventListener("pointerdown", this.handleDown);
    canvas.addEventListener("pointermove", this.handleMove);
    canvas.addEventListener("pointerup", this.handleUp);
    canvas.addEventListener("pointercancel", this.handleCancel);
    canvas.addEventListener("pointerleave", (e) => {
      if (e.pointerType === "mouse") this.onHover(null);
    });
    canvas.addEventListener("wheel", this.handleWheel, { passive: false });
  }

  setModel(moves: Cell[], line: Cell[] | null, winner: Mark | null): void {
    this.moves = moves;
    this.line = line;
    this.winner = winner;
    this.draw();
  }

  setPreview(cell: Cell | null, mark: Mark): void {
    const next = cell ? { cell, mark } : null;
    if (sameCell(this.preview?.cell, next?.cell) && this.preview?.mark === next?.mark) return;
    this.preview = next;
    this.draw();
  }

  /** Centres the view on the played moves (or the origin) at a comfortable zoom. */
  recenter(): void {
    const base = this.defaultScale();
    if (this.moves.length === 0) {
      this.cx = this.cy = 0.5;
      this.scale = base;
    } else {
      let [minX, minY] = this.moves[0];
      let [maxX, maxY] = this.moves[0];
      for (const [x, y] of this.moves) {
        minX = Math.min(minX, x);
        maxX = Math.max(maxX, x);
        minY = Math.min(minY, y);
        maxY = Math.max(maxY, y);
      }
      this.cx = (minX + maxX + 1) / 2;
      this.cy = (minY + maxY + 1) / 2;
      // Fit the played area with two spare cells on each side, but never zoom
      // in past the default.
      const fit = Math.min(this.width / (maxX - minX + 5), this.height / (maxY - minY + 5));
      this.scale = clamp(Math.min(base, fit), MIN_SCALE, MAX_SCALE);
    }
    this.draw();
  }

  resize(): void {
    const rect = this.canvas.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return;
    this.width = rect.width;
    this.height = rect.height;
    this.dpr = window.devicePixelRatio || 1;
    this.canvas.width = Math.round(rect.width * this.dpr);
    this.canvas.height = Math.round(rect.height * this.dpr);
    if (!this.sized) {
      this.sized = true;
      this.recenter();
    }
    this.draw();
  }

  // ---- input ----

  private pos(e: PointerEvent | WheelEvent): Point {
    const rect = this.canvas.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  }

  private cellAt(p: Point): Cell {
    return [
      Math.floor((p.x - this.width / 2) / this.scale + this.cx),
      Math.floor((p.y - this.height / 2) / this.scale + this.cy),
    ];
  }

  private handleDown = (e: PointerEvent) => {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    try {
      this.canvas.setPointerCapture(e.pointerId);
    } catch {
      // Capture is a nicety (keeps drags going outside the canvas); never fatal.
    }
    const p = this.pos(e);
    this.pointers.set(e.pointerId, p);
    if (this.pointers.size === 1) {
      this.tap = { ...p, pointerType: e.pointerType };
      this.startPan(p);
    } else {
      this.tap = null;
      this.startPinch();
    }
  };

  private handleMove = (e: PointerEvent) => {
    const p = this.pos(e);
    if (!this.pointers.has(e.pointerId)) {
      if (e.pointerType === "mouse") this.onHover(this.cellAt(p));
      return;
    }
    this.pointers.set(e.pointerId, p);

    if (this.pointers.size === 1) {
      if (this.tap) {
        const slop = this.tap.pointerType === "mouse" ? 4 : 10;
        if (Math.hypot(p.x - this.tap.x, p.y - this.tap.y) <= slop) return;
        this.tap = null;
      }
      this.cx = this.panAnchor.cx - (p.x - this.panAnchor.sx) / this.scale;
      this.cy = this.panAnchor.cy - (p.y - this.panAnchor.sy) / this.scale;
    } else {
      const [a, b] = [...this.pointers.values()];
      const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
      const dist = Math.max(1, Math.hypot(a.x - b.x, a.y - b.y));
      this.scale = clamp((this.pinch.scale * dist) / this.pinch.dist, MIN_SCALE, MAX_SCALE);
      this.cx = this.pinch.wx - (mid.x - this.width / 2) / this.scale;
      this.cy = this.pinch.wy - (mid.y - this.height / 2) / this.scale;
    }
    this.draw();
  };

  private handleUp = (e: PointerEvent) => {
    if (!this.pointers.delete(e.pointerId)) return;
    if (this.pointers.size === 0 && this.tap) {
      const { pointerType } = this.tap;
      const cell = this.cellAt(this.tap);
      this.tap = null;
      this.onTap(cell, pointerType);
    } else if (this.pointers.size === 1) {
      // Lifting one finger of a pinch: keep panning with the other, no jump.
      this.startPan([...this.pointers.values()][0]);
    } else if (this.pointers.size > 1) {
      this.startPinch();
    }
  };

  private handleCancel = (e: PointerEvent) => {
    this.pointers.delete(e.pointerId);
    this.tap = null;
  };

  private handleWheel = (e: WheelEvent) => {
    e.preventDefault();
    const delta = e.deltaMode === WheelEvent.DOM_DELTA_LINE ? e.deltaY * 16 : e.deltaY;
    // Trackpad pinches arrive as ctrl+wheel with small deltas.
    const factor = Math.exp(-delta * (e.ctrlKey ? 0.01 : 0.0015));
    this.zoomAt(this.pos(e), factor);
  };

  private startPan(p: Point): void {
    this.panAnchor = { sx: p.x, sy: p.y, cx: this.cx, cy: this.cy };
  }

  private startPinch(): void {
    const [a, b] = [...this.pointers.values()];
    const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
    this.pinch = {
      dist: Math.max(1, Math.hypot(a.x - b.x, a.y - b.y)),
      scale: this.scale,
      wx: (mid.x - this.width / 2) / this.scale + this.cx,
      wy: (mid.y - this.height / 2) / this.scale + this.cy,
    };
  }

  private zoomAt(p: Point, factor: number): void {
    const wx = (p.x - this.width / 2) / this.scale + this.cx;
    const wy = (p.y - this.height / 2) / this.scale + this.cy;
    this.scale = clamp(this.scale * factor, MIN_SCALE, MAX_SCALE);
    this.cx = wx - (p.x - this.width / 2) / this.scale;
    this.cy = wy - (p.y - this.height / 2) / this.scale;
    this.draw();
  }

  private defaultScale(): number {
    return clamp(Math.min(this.width, this.height) / 11, 30, 56);
  }

  // ---- drawing ----

  private readTheme(): void {
    const css = getComputedStyle(document.documentElement);
    const v = (name: string) => css.getPropertyValue(name).trim();
    this.theme = {
      bg: v("--board-bg"),
      grid: v("--board-grid"),
      lastMove: v("--board-last"),
      X: v("--x"),
      O: v("--o"),
      previewX: v("--x-preview"),
      previewO: v("--o-preview"),
    };
  }

  private draw(): void {
    if (this.frame || !this.sized) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = 0;
      this.render();
    });
  }

  private sx(wx: number): number {
    return (wx - this.cx) * this.scale + this.width / 2;
  }

  private sy(wy: number): number {
    return (wy - this.cy) * this.scale + this.height / 2;
  }

  private render(): void {
    const { ctx, width: w, height: h, scale: s, theme } = this;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.fillStyle = theme.bg;
    ctx.fillRect(0, 0, w, h);

    const x0 = Math.floor(this.cx - w / 2 / s) - 1;
    const x1 = Math.ceil(this.cx + w / 2 / s) + 1;
    const y0 = Math.floor(this.cy - h / 2 / s) - 1;
    const y1 = Math.ceil(this.cy + h / 2 / s) + 1;
    const visible = ([x, y]: Cell) => x >= x0 && x <= x1 && y >= y0 && y <= y1;

    // Highlights under the grid lines.
    const last = this.moves.at(-1);
    if (last && visible(last)) {
      ctx.fillStyle = theme.lastMove;
      ctx.fillRect(this.sx(last[0]), this.sy(last[1]), s, s);
    }
    if (this.preview) {
      const [px, py] = this.preview.cell;
      ctx.globalAlpha = 0.14;
      ctx.fillStyle = this.preview.mark === "X" ? theme.previewX : theme.previewO;
      ctx.fillRect(this.sx(px), this.sy(py), s, s);
      ctx.globalAlpha = 1;
    }

    ctx.beginPath();
    for (let x = x0; x <= x1; x++) {
      const px = Math.round(this.sx(x) * this.dpr) / this.dpr + 0.5 / this.dpr;
      ctx.moveTo(px, 0);
      ctx.lineTo(px, h);
    }
    for (let y = y0; y <= y1; y++) {
      const py = Math.round(this.sy(y) * this.dpr) / this.dpr + 0.5 / this.dpr;
      ctx.moveTo(0, py);
      ctx.lineTo(w, py);
    }
    ctx.strokeStyle = theme.grid;
    ctx.lineWidth = 1 / this.dpr;
    ctx.stroke();

    this.moves.forEach((cell, i) => {
      if (!visible(cell)) return;
      const mark: Mark = i % 2 === 0 ? "X" : "O";
      this.drawMark(cell, mark, theme[mark], false);
    });

    if (this.preview && visible(this.preview.cell)) {
      const color = this.preview.mark === "X" ? theme.previewX : theme.previewO;
      this.drawMark(this.preview.cell, this.preview.mark, color, true);
    }

    if (this.line && this.winner) {
      const first = this.line[0];
      const end = this.line[this.line.length - 1];
      ctx.beginPath();
      ctx.moveTo(this.sx(first[0] + 0.5), this.sy(first[1] + 0.5));
      ctx.lineTo(this.sx(end[0] + 0.5), this.sy(end[1] + 0.5));
      ctx.lineCap = "round";
      ctx.lineWidth = Math.max(4, s * 0.16);
      ctx.strokeStyle = theme[this.winner];
      ctx.globalAlpha = 0.55;
      ctx.stroke();
      ctx.globalAlpha = 1;
    }

    if (last && this.offscreen(last)) {
      this.drawPointer(last, this.moves.length % 2 === 1 ? theme.X : theme.O);
    }
  }

  private drawMark([x, y]: Cell, mark: Mark, color: string, dashed: boolean): void {
    const { ctx, scale: s } = this;
    const left = this.sx(x);
    const top = this.sy(y);
    ctx.save();
    ctx.strokeStyle = color;
    if (dashed) {
      // Thinner strokes with square ends: round caps would grow every dash by
      // half the line width on each side and close the gaps.
      ctx.lineWidth = Math.max(1.5, s * 0.07);
      ctx.lineCap = "butt";
      ctx.setLineDash([s * 0.08, s * 0.07]);
    } else {
      ctx.lineWidth = Math.max(2, s * 0.11);
      ctx.lineCap = "round";
    }
    ctx.beginPath();
    if (mark === "X") {
      const pad = s * 0.24;
      ctx.moveTo(left + pad, top + pad);
      ctx.lineTo(left + s - pad, top + s - pad);
      ctx.moveTo(left + s - pad, top + pad);
      ctx.lineTo(left + pad, top + s - pad);
    } else {
      ctx.arc(left + s / 2, top + s / 2, s * 0.29, 0, Math.PI * 2);
    }
    ctx.stroke();
    ctx.restore();
  }

  private offscreen([x, y]: Cell): boolean {
    const px = this.sx(x + 0.5);
    const py = this.sy(y + 0.5);
    return px < 0 || py < 0 || px > this.width || py > this.height;
  }

  /** Draws an arrow at the screen edge pointing towards an off-screen cell. */
  private drawPointer([x, y]: Cell, color: string): void {
    const { ctx, width: w, height: h } = this;
    const dx = this.sx(x + 0.5) - w / 2;
    const dy = this.sy(y + 0.5) - h / 2;
    const margin = 28;
    const t = Math.min((w / 2 - margin) / Math.abs(dx || 1e-9), (h / 2 - margin) / Math.abs(dy || 1e-9));
    const ax = w / 2 + dx * t;
    const ay = h / 2 + dy * t;
    const angle = Math.atan2(dy, dx);
    ctx.save();
    ctx.translate(ax, ay);
    ctx.rotate(angle);
    ctx.beginPath();
    ctx.moveTo(12, 0);
    ctx.lineTo(-8, -9);
    ctx.lineTo(-8, 9);
    ctx.closePath();
    ctx.fillStyle = color;
    ctx.globalAlpha = 0.85;
    ctx.fill();
    ctx.restore();
  }
}

function sameCell(a: Cell | undefined, b: Cell | undefined): boolean {
  if (!a || !b) return a === b;
  return a[0] === b[0] && a[1] === b[1];
}
