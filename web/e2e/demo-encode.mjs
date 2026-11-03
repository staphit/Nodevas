// Turns the demo recordings (playwright.demo.config.ts) into the README's
// videos: MP4s, uploaded to GitHub as attachments so the README plays them in
// GitHub's video player (see docs/contributing.md). Needs ffmpeg on PATH.
//
//   node e2e/demo-encode.mjs [clip ...]
//
// Each recording comes with a film.json (e2e/demo-videos.spec.ts): a title
// card, camera keyframes in viewport pixels, captions, and cuts. The finished
// frame is the app as a window with rounded corners on a dark backdrop; the
// camera crops the recording and eases between keyframes; captions are set
// below the window at output resolution, so zooming never shrinks them; a cut
// becomes a cross-fade.

import { spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const webDir = resolve(import.meta.dirname, "..");
const results = join(webDir, "demo-videos", ".results");
const out = resolve(webDir, "..", "docs", "videos");
mkdirSync(out, { recursive: true });

// The output frame (16:10) and the window in it (16:9, like the camera).
const OW = 1280;
const OH = 800;
const WIN = { x: 80, y: 28, w: 1120, h: 630, r: 14 };
const FPS = 25;
const TITLE = 2.2; // seconds of title card before the window fades in
const FADE = 0.5; // the window's fade in and out
const XFADE = 0.5; // a cut's cross-fade
const TEXT_FADE = 0.25;
const END_HOLD = 1.0;

function ffmpeg(args, cwd) {
  const result = spawnSync("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", ...args], { stdio: "inherit", cwd });
  if (result.status !== 0) throw new Error(`ffmpeg ${args.join(" ")} failed (${result.status})`);
}

function probeSize(file) {
  const result = spawnSync("ffprobe", ["-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", file], { encoding: "utf8" });
  const [w, h] = result.stdout.trim().split(",").map(Number);
  return { w, h };
}

/** How long an ffconcat list plays: the sum of its durations. */
function listDuration(file) {
  return readFileSync(file, "utf8")
    .split(/\r?\n/)
    .filter((line) => line.startsWith("duration "))
    .reduce((sum, line) => sum + Number(line.slice(9)), 0);
}

// ---------------------------------------------------------------------------
// The backdrop, the window's shadow, mask and edge, drawn once per run.

const smooth = (a, b, x) => {
  const k = Math.min(1, Math.max(0, (x - a) / (b - a)));
  return k * k * (3 - 2 * k);
};

/** Signed distance to the window's rounded rectangle (negative inside). */
function sdf(px, py, rect) {
  const cx = rect.x + rect.w / 2;
  const cy = rect.y + rect.h / 2;
  const qx = Math.abs(px - cx) - (rect.w / 2 - rect.r);
  const qy = Math.abs(py - cy) - (rect.h / 2 - rect.r);
  return Math.hypot(Math.max(qx, 0), Math.max(qy, 0)) + Math.min(Math.max(qx, qy), 0) - rect.r;
}

function drawPlates(dir) {
  const backdrop = Buffer.alloc(OW * OH * 3);
  const shadow = Buffer.alloc(OW * OH * 4);
  const edge = Buffer.alloc(WIN.w * WIN.h * 4);
  const mask = Buffer.alloc(WIN.w * WIN.h);
  const below = { ...WIN, y: WIN.y + 16 };
  for (let y = 0; y < OH; y++) {
    for (let x = 0; x < OW; x++) {
      // Near-black, lit softly from above the window.
      const dx = (x - OW / 2) / (OW * 0.62);
      const dy = (y - OH * 0.3) / (OH * 0.75);
      const glow = Math.exp(-(dx * dx + dy * dy) * 1.6);
      const i = (y * OW + x) * 3;
      backdrop[i] = 9 + 17 * glow;
      backdrop[i + 1] = 10 + 19 * glow;
      backdrop[i + 2] = 13 + 27 * glow;
      // The window's shadow: wide and soft, a little below it.
      shadow[(y * OW + x) * 4 + 3] = 255 * 0.62 * (1 - smooth(-24, 70, sdf(x + 0.5, y + 0.5, below)));
    }
  }
  const local = { ...WIN, x: 0, y: 0 };
  for (let y = 0; y < WIN.h; y++) {
    for (let x = 0; x < WIN.w; x++) {
      // The rounded corners, and a faint light edge just inside the outline.
      const d = sdf(x + 0.5, y + 0.5, local);
      const inside = Math.min(1, Math.max(0, 0.5 - d));
      mask[y * WIN.w + x] = 255 * inside;
      const j = (y * WIN.w + x) * 4;
      edge[j] = edge[j + 1] = edge[j + 2] = 255;
      edge[j + 3] = 255 * inside * (1 - smooth(0.5, 1.6, -d)) * 0.16;
    }
  }
  const raw = (file, data, format, size) => {
    writeFileSync(join(dir, `${file}.raw`), data);
    ffmpeg(["-f", "rawvideo", "-pix_fmt", format, "-s", size, "-i", `${file}.raw`, `${file}.png`], dir);
  };
  raw("backdrop", backdrop, "rgb24", `${OW}x${OH}`);
  raw("shadow", shadow, "rgba", `${OW}x${OH}`);
  raw("edge", edge, "rgba", `${WIN.w}x${WIN.h}`);
  raw("mask", mask, "gray", `${WIN.w}x${WIN.h}`);
}

/** Copies a font next to the filter script, so its path needs no escaping. */
function fonts(dir) {
  const candidates = {
    "head.ttf": ["C:/Windows/Fonts/seguisb.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", "/System/Library/Fonts/SFNS.ttf"],
    "body.ttf": ["C:/Windows/Fonts/segoeui.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/System/Library/Fonts/SFNS.ttf"],
  };
  for (const [name, paths] of Object.entries(candidates)) {
    const found = paths.find((path) => existsSync(path));
    if (!found) throw new Error(`no font for ${name}; tried ${paths.join(", ")}`);
    copyFileSync(found, join(dir, name));
  }
}

// ---------------------------------------------------------------------------
// The film.

const easeInOut = (k) => (k < 0.5 ? 4 * k * k * k : 1 - Math.pow(-2 * k + 2, 3) / 2);

/** The camera at time t: eased from the previous keyframe over d seconds,
 * zoom interpolated in log space so a push-in feels even. */
function cameraAt(keys, t, view) {
  let current = { x: 0, y: 0, w: view.w, h: view.h };
  for (let i = 0; i < keys.length; i++) {
    const key = keys[i];
    if (t < key.t) break;
    const k = key.d > 0 ? Math.min(1, (t - key.t) / key.d) : 1;
    if (k >= 1) {
      current = key;
      continue;
    }
    const e = easeInOut(k);
    const from = current;
    const fw = Math.log(from.w);
    const w = Math.exp(fw + (Math.log(key.w) - fw) * e);
    const cx = from.x + from.w / 2 + (key.x + key.w / 2 - from.x - from.w / 2) * e;
    const cy = from.y + from.h / 2 + (key.y + key.h / 2 - from.y - from.h / 2) * e;
    const h = (w * view.h) / view.w;
    current = {
      x: Math.min(view.w - w, Math.max(0, cx - w / 2)),
      y: Math.min(view.h - h, Math.max(0, cy - h / 2)),
      w,
      h,
    };
    break;
  }
  return current;
}

/** The parts of the recording that make the video, and a map from recording
 * time to video time. */
function timeline(film, duration) {
  const start = film.begin ?? 1.5;
  const end = Math.min(duration - 0.1, film.end ?? duration);
  const segments = [];
  let from = start;
  for (const cut of [...film.cuts].sort((a, b) => a.from - b.from)) {
    if (cut.from <= from || cut.to >= end) continue;
    segments.push({ from, to: cut.from });
    from = cut.to;
  }
  segments.push({ from, to: end });
  // Each segment starts where the previous one's cross-fade begins.
  let at = TITLE;
  for (const [index, segment] of segments.entries()) {
    segment.at = at;
    at += segment.to - segment.from - (index < segments.length - 1 ? XFADE : 0);
  }
  const map = (t) => {
    for (const segment of segments) {
      if (t < segment.from) return segment.at;
      if (t <= segment.to) return segment.at + t - segment.from;
    }
    return at;
  };
  return { segments, length: at, map };
}

const q = (n) => n.toFixed(3);

function encode(name, dir, plates) {
  const film = JSON.parse(readFileSync(join(dir, "film.json"), "utf8"));
  // The spec's own 2x recording: timed frames listed in an ffconcat file.
  const source = join(dir, film.frames);
  const input = ["-f", "concat", "-safe", "0", "-i", source];
  const duration = listDuration(source);
  // Film coordinates are the recorded viewport's pixels.
  const view = film.viewport ?? { w: 1440, h: 810 };
  const px = probeSize(join(dirname(source), "00000.jpg"));
  const scale = px.w / view.w;
  const { segments, length, map } = timeline(film, duration);
  for (const segment of segments) {
    if (segment.to - segment.from < XFADE + 0.2) throw new Error(`${name}: a segment of ${q(segment.to - segment.from)}s is too short to cross-fade`);
  }
  const work = mkdtempSync(join(tmpdir(), `demo-${name}-`));
  for (const file of ["backdrop.png", "shadow.png", "edge.png", "mask.png", "head.ttf", "body.ttf"]) copyFileSync(join(plates, file), join(work, file));

  // The camera, frame by frame, as crop commands.
  const lines = [];
  let last = "";
  const first = segments[0].from;
  const lastT = segments[segments.length - 1].to;
  for (let frame = Math.floor(first * FPS); frame <= Math.ceil(lastT * FPS) + 1; frame++) {
    const t = frame / FPS;
    const c = cameraAt(film.camera, t, view);
    const w = Math.round((c.w * scale) / 2) * 2;
    const h = Math.round((w * px.h) / px.w / 2) * 2;
    const x = Math.round(Math.min(px.w - w, Math.max(0, c.x * scale)));
    const y = Math.round(Math.min(px.h - h, Math.max(0, c.y * scale)));
    const command = `crop w ${w}, crop h ${h}, crop x ${x}, crop y ${y}`;
    if (command === last) continue;
    // Just before the frame's time, so the frame is cut with it.
    lines.push(`${q(Math.max(0, t - 0.5 / FPS))} ${command};`);
    last = command;
  }
  writeFileSync(join(work, "camera.txt"), lines.join("\n") + "\n");

  // Text: the title card, then the captions under the window.
  const texts = [];
  const textFile = (value) => {
    const file = `text${texts.length}.txt`;
    writeFileSync(join(work, file), value, "utf8");
    texts.push(file);
    return file;
  };
  const alpha = (a, b) => `'if(lt(t,${q(a + TEXT_FADE)}),(t-${q(a)})/${TEXT_FADE},if(gt(t,${q(b - TEXT_FADE)}),(${q(b)}-t)/${TEXT_FADE},1))'`;
  const draw = (value, font, size, color, y, a, b) =>
    `drawtext=fontfile=${font}:textfile=${textFile(value)}:expansion=none:fontsize=${size}:fontcolor=${color}:x=(w-text_w)/2:y=${y}:enable='between(t,${q(a)},${q(b)})':alpha=${alpha(a, b)}`;
  const drawn = [];
  const end = length + END_HOLD;
  if (film.title) {
    drawn.push(draw(film.title.title, "head.ttf", 58, "white", 318, 0.15, TITLE + 0.15));
    if (film.title.sub) drawn.push(draw(film.title.sub, "body.ttf", 26, "white@0.62", 402, 0.35, TITLE + 0.15));
  }
  const captions = film.captions.map((c) => ({ ...c, at: Math.max(TITLE + 0.2, map(c.t)) }));
  for (const [index, caption] of captions.entries()) {
    if (!caption.title) continue;
    const a = caption.at;
    const b = Math.min(end - FADE, index + 1 < captions.length ? captions[index + 1].at : end - FADE);
    if (b - a < 0.6) continue;
    drawn.push(draw(caption.title, "head.ttf", 32, "white", WIN.y + WIN.h + 30, a, b));
    if (caption.sub) drawn.push(draw(caption.sub, "body.ttf", 21, "white@0.6", WIN.y + WIN.h + 78, a, b));
  }

  // The graph: camera → window → cuts cross-faded → on the backdrop → text.
  const graph = [];
  graph.push(
    `[0:v]setpts=PTS-STARTPTS,fps=${FPS},format=yuv444p,sendcmd=f=camera.txt,crop=w=${px.w}:h=${px.h}:x=0:y=0:exact=1,` +
      `scale=${WIN.w}:${WIN.h}:flags=lanczos,setsar=1,split=${segments.length}${segments.map((_, i) => `[c${i}]`).join("")}`,
  );
  segments.forEach((segment, i) => {
    graph.push(`[c${i}]trim=start=${q(segment.from)}:end=${q(segment.to)},setpts=PTS-STARTPTS[s${i}]`);
  });
  let chain = "s0";
  for (let i = 1; i < segments.length; i++) {
    const offset = segments[i].at - TITLE - XFADE;
    graph.push(`[${chain}][s${i}]xfade=transition=fade:duration=${XFADE}:offset=${q(offset)}[x${i}]`);
    chain = `x${i}`;
  }
  // The window (rounded, edged, on its shadow) fades in after the title
  // card and out at the end, over the bare backdrop.
  graph.push(`[${chain}]tpad=stop_mode=clone:stop_duration=${END_HOLD},tpad=start_duration=${TITLE}[held]`);
  graph.push(`[held][3:v]alphamerge[rounded]`);
  graph.push(`[rounded][4:v]overlay=0:0:shortest=1:format=yuv444[edged]`);
  graph.push(`[2:v][edged]overlay=x=${WIN.x}:y=${WIN.y}:eof_action=endall:format=yuv444,format=yuva444p,` +
    `fade=t=in:st=${q(TITLE - 0.1)}:d=${FADE}:alpha=1,fade=t=out:st=${q(end - FADE)}:d=${FADE}:alpha=1[window]`);
  graph.push(`[1:v][window]overlay=0:0:eof_action=endall:format=yuv444,${[...drawn, "format=yuv444p"].join(",")}[out]`);
  writeFileSync(join(work, "graph.txt"), graph.join(";\n"));

  const master = join(work, "master.mkv");
  ffmpeg(
    [
      ...input,
      ...["backdrop.png", "shadow.png", "mask.png", "edge.png"].flatMap((file) => ["-loop", "1", "-framerate", String(FPS), "-i", file]),
      "-/filter_complex", "graph.txt", "-map", "[out]", "-t", q(end),
      "-c:v", "ffv1", "-level", "3", "master.mkv",
    ],
    work,
  );

  const mp4 = join(out, `${name}.mp4`);
  ffmpeg(["-i", master, "-c:v", "libx264", "-preset", "slow", "-crf", "22", "-tune", "animation", "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", mp4]);
  rmSync(work, { recursive: true, force: true });

  const size = (file) => `${(statSync(file).size / 1024 / 1024).toFixed(1)} MB`;
  console.log(`${name}: ${q(end)}s, ${size(mp4)} mp4`);
}

const prefix = "demo-videos-";
const only = process.argv.slice(2);
const clips = existsSync(results)
  ? readdirSync(results).filter((name) => name.startsWith(prefix) && !name.endsWith("-retry1"))
  : [];
if (clips.length === 0) throw new Error(`no recordings in ${results}; run the demo config first`);

const plates = mkdtempSync(join(tmpdir(), "demo-plates-"));
try {
  drawPlates(plates);
  fonts(plates);
  for (const dir of clips) {
    const name = dir.slice(prefix.length);
    if (only.length > 0 && !only.includes(name)) continue;
    if (!existsSync(join(results, dir, "film.json"))) continue;
    encode(name, join(results, dir), plates);
  }
} finally {
  rmSync(plates, { recursive: true, force: true });
}
