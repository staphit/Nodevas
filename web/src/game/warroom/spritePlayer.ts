// Plays one of a theme pack's pictures on a canvas. A scene with frames is a
// strip: frame 0 is the still, every other holds only what it changes, and is
// laid over the still once, when the strip loads. A frame is never cut to:
// one fades into the next from whatever the canvas shows at that moment, so
// an interrupted reaction cannot pop. On her own she only blinks, now and
// then; a click plays her reaction. A scene with a clip is played by a
// ClipPlayer on a video element instead, and a scene's click clip by a
// ClickClipPlayer on another, over either. Nothing here knows React:
// ThemeFigure owns the players of each picture.

import type { ThemeFrames } from "../../theme-pack/themeManifest";

export interface SpriteSource {
  /** The scene's image, or its strip of frames. */
  image: string;
  /** The room drawn under her, over the whole canvas; "" for none. */
  room: string;
  /** The pack's canvas, and where on it the scene's image goes. */
  canvas: readonly [number, number];
  rect: readonly [x: number, y: number, w: number, h: number];
  frames?: ThemeFrames;
}

const STILL = 0;
/** The hold after each click frame, for one frame and for two. */
const CLICK_HOLDS = [420, 650];

const sleep = (ms: number) => new Promise<void>((done) => setTimeout(done, ms));
const between = (a: number, b: number) => a + Math.random() * (b - a);

/** Every player in a fade shares one animation frame; none, and it stops. */
const fading = new Set<SpritePlayer>();
let looping = false;

function loop(now: number) {
  for (const player of fading) player.draw(now);
  if (fading.size > 0) requestAnimationFrame(loop);
  else looping = false;
}

function sheet(width: number, height: number): HTMLCanvasElement {
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  return canvas;
}

function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((done, fail) => {
    const image = new Image();
    image.decoding = "async";
    image.onload = () => done(image);
    image.onerror = () => fail(new Error("image"));
    image.src = url;
  });
}

/** The strip's frames, each at the size it is drawn: scaled once, well, so a
 * fade is two plain copies. */
async function slices(
  strip: HTMLImageElement,
  count: number,
  width: number,
  height: number,
): Promise<CanvasImageSource[]> {
  const fw = Math.floor(strip.naturalWidth / count);
  const fh = strip.naturalHeight;
  if (typeof createImageBitmap === "function") {
    try {
      return await Promise.all(
        Array.from({ length: count }, (_, i) =>
          createImageBitmap(strip, i * fw, 0, fw, fh, {
            resizeWidth: width,
            resizeHeight: height,
            resizeQuality: "high",
          }),
        ),
      );
    } catch {
      /* a browser that cannot scale a bitmap: the canvas does it */
    }
  }
  return Array.from({ length: count }, (_, i) => {
    const part = sheet(width, height);
    const g = part.getContext("2d");
    if (g) {
      g.imageSmoothingQuality = "high";
      g.drawImage(strip, i * fw, 0, fw, fh, 0, 0, width, height);
    }
    return part;
  });
}

export class SpritePlayer {
  private frames: HTMLCanvasElement[] | null = null;
  private readonly back = document.createElement("canvas");
  private fade: { index: number; ms: number; begin: number } | null = null;
  /** Bumped by whatever takes her over: an older loop sees it and stops. */
  private turn = 0;
  private loading = false;
  private wanted = false;
  private still = false;
  /** The canvas's width on the page when the frames were made. */
  private madeFor = 0;

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly source: SpriteSource,
    private readonly onBroken: () => void,
  ) {}

  /** Whether she may move at all: not under reduced motion, not in a
   * background tab. */
  setStill(still: boolean) {
    if (this.still === still) return;
    this.still = still;
    if (!this.frames) return;
    if (still) this.show(STILL);
    else void this.idle();
  }

  /** On screen, or near it: load; far off, drop what was loaded. */
  setWanted(wanted: boolean) {
    this.wanted = wanted;
    if (wanted) void this.load();
    else this.drop();
  }

  /** The canvas changed size: frames made for a much smaller or larger one
   * are made again. */
  resized() {
    if (!this.frames || !this.madeFor) return;
    const width = this.canvas.getBoundingClientRect().width;
    if (width > 0 && Math.abs(width - this.madeFor) / this.madeFor > 0.15) {
      this.drop(true);
      void this.load();
    }
  }

  dispose() {
    this.wanted = false;
    this.drop();
  }

  private drop(keepPicture = false) {
    this.turn++;
    this.fade = null;
    fading.delete(this);
    this.frames = null;
    this.madeFor = 0;
    if (!keepPicture) this.canvas.width = this.canvas.height = 0;
  }

  private async load() {
    if (this.frames || this.loading) return;
    const width = this.canvas.getBoundingClientRect().width;
    if (!(width > 0)) return;
    this.loading = true;
    const { source } = this;
    let strip: HTMLImageElement;
    let room: HTMLImageElement | undefined;
    try {
      [strip, room] = await Promise.all([loadImage(source.image), source.room ? loadImage(source.room) : undefined]);
    } catch {
      this.loading = false;
      this.onBroken();
      return;
    }
    const k = window.devicePixelRatio || 1;
    const [cw, ch] = source.canvas;
    const w = Math.max(1, Math.round(width * k));
    const h = Math.max(1, Math.round((w * ch) / cw));
    const scale = w / cw;
    const [x, y, sw, sh] = source.rect;
    const left = Math.round(x * scale);
    const top = Math.round(y * scale);
    const count = source.frames?.count ?? 1;
    let parts: CanvasImageSource[];
    try {
      parts = await slices(strip, count, Math.max(1, Math.round(sw * scale)), Math.max(1, Math.round(sh * scale)));
    } catch {
      this.loading = false;
      this.onBroken();
      return;
    }
    this.loading = false;
    const close = () => parts.forEach((part) => (part as Partial<ImageBitmap>).close?.());
    if (!this.wanted) {
      close();
      return;
    }
    const frames = parts.map((part, i) => {
      const whole = sheet(w, h);
      const g = whole.getContext("2d");
      if (g) {
        if (room) g.drawImage(room, 0, 0, w, h);
        g.drawImage(parts[STILL], left, top);
        if (i !== STILL) g.drawImage(part, left, top);
      }
      return whole;
    });
    close();
    this.canvas.width = this.back.width = w;
    this.canvas.height = this.back.height = h;
    this.frames = frames;
    this.madeFor = width;
    this.show(STILL);
    if (!this.still) void this.idle();
  }

  /** One frame, at once: whatever was playing is over. */
  private show(index: number) {
    this.turn++;
    this.fade = null;
    fading.delete(this);
    const g = this.canvas.getContext("2d");
    if (!g || !this.frames) return;
    g.globalAlpha = 1;
    g.clearRect(0, 0, this.canvas.width, this.canvas.height);
    g.drawImage(this.frames[index], 0, 0);
  }

  /** Fades from what the canvas shows right now, whatever that is, to one
   * frame. */
  private to(index: number, ms: number): Promise<void> {
    if (!this.frames) return sleep(0);
    const back = this.back.getContext("2d");
    if (back) {
      back.clearRect(0, 0, this.back.width, this.back.height);
      back.drawImage(this.canvas, 0, 0);
    }
    this.fade = { index, ms, begin: performance.now() };
    fading.add(this);
    if (!looping) {
      looping = true;
      requestAnimationFrame(loop);
    }
    return sleep(ms);
  }

  /** One step of a fade: the shared loop calls it. */
  draw(now: number) {
    const g = this.canvas.getContext("2d");
    if (!this.frames || !this.fade || !g) {
      fading.delete(this);
      return;
    }
    const t = Math.min(1, Math.max(0, (now - this.fade.begin) / this.fade.ms));
    g.globalAlpha = 1;
    g.clearRect(0, 0, this.canvas.width, this.canvas.height);
    g.drawImage(this.back, 0, 0);
    g.globalAlpha = t * t * (3 - 2 * t);
    g.drawImage(this.frames[this.fade.index], 0, 0);
    g.globalAlpha = 1;
    if (t >= 1) fading.delete(this);
  }

  /** At rest she only blinks, now and then. */
  private async idle() {
    const turn = ++this.turn;
    const mine = () => turn === this.turn && this.frames !== null;
    const blink = this.source.frames?.blink;
    await this.to(STILL, 260);
    while (mine() && blink !== undefined) {
      await sleep(between(1800, 5200));
      if (!mine()) return;
      await this.to(blink, 70);
      await sleep(70);
      if (!mine()) return;
      await this.to(STILL, 110);
    }
  }

  /** A click plays her click frames in order, then she rests again. */
  async click() {
    const frames = this.source.frames;
    if (this.still || !frames || frames.click.length === 0 || !this.frames) return;
    const turn = ++this.turn;
    const holds = CLICK_HOLDS.slice(-frames.click.length);
    for (let i = 0; i < frames.click.length; i++) {
      await this.to(frames.click[i], i ? 200 : 160);
      await sleep(holds[i] ?? CLICK_HOLDS[0]);
      // Something else took her over.
      if (turn !== this.turn) return;
    }
    void this.idle();
  }
}

/** Plays a scene's clip on a video element, silent and looping. It loads
 * nothing until she nears the screen, plays only there, and pauses when she
 * leaves it. Still, she shows the poster: the scene's still picture. */
export class ClipPlayer {
  private wanted = false;
  /** Still until told otherwise, so that nothing starts before it may. */
  private still = true;
  /** A click clip is playing over her. */
  private held = false;

  constructor(private readonly video: HTMLVideoElement) {}

  /** A click clip plays over her: the loop waits under it, and starts again
   * from its first frame, the still, when the click clip ends on the same. */
  hold(held: boolean) {
    if (this.held === held) return;
    this.held = held;
    if (!held) this.video.currentTime = 0;
    this.update();
  }

  setStill(still: boolean) {
    if (this.still === still) return;
    this.still = still;
    this.update();
  }

  /** On screen, or near it: play; far off, pause where she is. */
  setWanted(wanted: boolean) {
    if (this.wanted === wanted) return;
    this.wanted = wanted;
    this.update();
  }

  dispose() {
    this.wanted = false;
    this.video.pause();
  }

  private update() {
    const { video } = this;
    if (this.still) {
      // Paused, a clip holds whatever frame it was on: loading it again,
      // fetching nothing, puts the poster back.
      if (!video.paused || video.currentTime > 0) {
        video.pause();
        video.preload = "none";
        video.load();
      }
      return;
    }
    if (!this.wanted || this.held) {
      video.pause();
      return;
    }
    video.preload = "auto";
    // Refused when the browser starts nothing on its own, or when a pause
    // comes first: the poster stays. A clip that cannot play at all fires
    // error, which ThemeFigure hears. Some browsers return no promise.
    void video.play()?.catch(() => {});
  }
}

/** Plays a scene's click clip once, from its start, on a video element over
 * her, which shows only while it plays: the clip starts and ends on the
 * still, so the loop (or the canvas) under it comes back without a seam. It
 * loads only once she nears the screen. Still, a click plays nothing. */
export class ClickClipPlayer {
  private wanted = false;
  /** Still until told otherwise, so that nothing starts before it may. */
  private still = true;
  /** Shown over her: told to onPlaying. */
  private shown = false;
  /** Bumped by every start and stop: an older play refused late is ignored. */
  private turn = 0;

  constructor(
    private readonly video: HTMLVideoElement,
    private readonly onPlaying: (playing: boolean) => void,
  ) {
    video.addEventListener("playing", this.started);
    video.addEventListener("ended", this.ended);
  }

  setStill(still: boolean) {
    if (this.still === still) return;
    this.still = still;
    if (still) this.stop();
    this.preloadIfWanted();
  }

  /** On screen, or near it: load; far off, a reaction playing stops. */
  setWanted(wanted: boolean) {
    if (this.wanted === wanted) return;
    this.wanted = wanted;
    if (!wanted) this.stop();
    this.preloadIfWanted();
  }

  /** Plays the clip from its start: again from the start if it is playing. */
  click() {
    if (this.still) return;
    const { video } = this;
    const turn = ++this.turn;
    video.currentTime = 0;
    // Refused when a pause comes first, or when the browser starts nothing:
    // she stays as she was. A clip that cannot play at all fires error, which
    // ThemeFigure hears. Some browsers return no promise.
    void video.play()?.catch(() => {
      if (turn === this.turn) this.stop();
    });
  }

  dispose() {
    this.video.removeEventListener("playing", this.started);
    this.video.removeEventListener("ended", this.ended);
    this.wanted = false;
    this.stop();
  }

  /** Shown once it has a frame to show, not at the click: a clip still
   * loading would be a gap over her. */
  private readonly started = () => {
    if (this.shown || this.still) return;
    this.shown = true;
    this.onPlaying(true);
  };

  private readonly ended = () => this.stop();

  private preloadIfWanted() {
    if (this.wanted && !this.still) this.video.preload = "auto";
  }

  private stop() {
    this.turn++;
    this.video.pause();
    if (!this.shown) return;
    this.shown = false;
    this.onPlaying(false);
  }
}
