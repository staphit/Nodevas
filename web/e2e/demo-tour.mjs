// Turns the feature tour's frames (demo-stills.spec.ts) into the README's
// docs/screenshots/nodevas-feature-tour.gif: each frame cropped to its
// close-up, scaled to the GIF's size, captioned in a band along the bottom,
// and held for a moment. Needs ffmpeg on PATH.
//
//   node e2e/demo-tour.mjs

import { spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const webDir = resolve(import.meta.dirname, "..");
const source = join(webDir, "demo-videos", ".results", "demo-stills-tour-feature-tour-frames", "tour");
const gif = resolve(webDir, "..", "docs", "screenshots", "nodevas-feature-tour.gif");
const HOLD = 1.6; // seconds per frame
const BAND = 78; // the caption band's height

function ffmpeg(args, cwd) {
  const result = spawnSync("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", ...args], { stdio: "inherit", cwd });
  if (result.status !== 0) throw new Error(`ffmpeg ${args.join(" ")} failed (${result.status})`);
}

/** drawtext wants its text escaped; a file sidesteps that. */
function textFile(dir, name, text) {
  writeFileSync(join(dir, name), text, "utf8");
  return name;
}

if (!existsSync(join(source, "tour.json"))) throw new Error(`no tour in ${source}; run npm run demo:stills first`);
const tour = JSON.parse(readFileSync(join(source, "tour.json"), "utf8"));
const work = mkdtempSync(join(tmpdir(), "demo-tour-"));
try {
  const fonts = {
    "head.ttf": ["C:/Windows/Fonts/seguisb.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", "/System/Library/Fonts/SFNS.ttf"],
    "body.ttf": ["C:/Windows/Fonts/segoeui.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/System/Library/Fonts/SFNS.ttf"],
  };
  for (const [name, paths] of Object.entries(fonts)) {
    const found = paths.find((path) => existsSync(path));
    if (!found) throw new Error(`no font for ${name}`);
    copyFileSync(found, join(work, name));
  }
  const { width: W, height: H } = tour;
  const list = ["ffconcat version 1.0"];
  tour.frames.forEach((frame, index) => {
    // The screenshot is at twice the CSS pixels the crop is in.
    const c = frame.crop;
    const title = textFile(work, `t${index}.txt`, frame.title);
    const sub = textFile(work, `s${index}.txt`, frame.sub);
    const filter = [
      `crop=${c.w * 2}:${c.h * 2}:${c.x * 2}:${c.y * 2}`,
      `scale=${W}:${H}:flags=lanczos`,
      `drawbox=x=0:y=${H - BAND}:w=${W}:h=${BAND}:color=0x0b0d12@0.86:t=fill`,
      `drawbox=x=0:y=${H - BAND}:w=${W}:h=1:color=0xffffff@0.10:t=fill`,
      `drawtext=fontfile=head.ttf:textfile=${title}:fontsize=25:fontcolor=0xf2f4f8:x=(w-text_w)/2:y=${H - BAND + 12}`,
      `drawtext=fontfile=body.ttf:textfile=${sub}:fontsize=17:fontcolor=0xaab2c0:x=(w-text_w)/2:y=${H - BAND + 46}`,
    ].join(",");
    const out = `f${String(index).padStart(2, "0")}.png`;
    ffmpeg(["-i", join(source, frame.file), "-vf", filter, out], work);
    list.push(`file ${out}`, `duration ${HOLD}`);
  });
  list.push(`file f${String(tour.frames.length - 1).padStart(2, "0")}.png`);
  writeFileSync(join(work, "frames.ffconcat"), `${list.join("\n")}\n`);
  ffmpeg(
    [
      "-f", "concat", "-safe", "0", "-i", "frames.ffconcat",
      "-filter_complex",
      "[0:v]split[a][b];[a]palettegen=max_colors=192:stats_mode=full[p];[b][p]paletteuse=dither=none:diff_mode=rectangle",
      "-loop", "0",
      gif,
    ],
    work,
  );
  console.log(`nodevas-feature-tour.gif: ${tour.frames.length} frames, ${(statSync(gif).size / 1024 / 1024).toFixed(2)} MB`);
} finally {
  rmSync(work, { recursive: true, force: true });
}
