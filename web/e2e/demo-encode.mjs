// Turns the demo recordings (playwright.demo.config.ts) into the README's
// videos: an MP4 to watch at full size and a GIF that GitHub plays inline.
// Needs ffmpeg on PATH.
//
//   node e2e/demo-encode.mjs

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const webDir = resolve(import.meta.dirname, "..");
const results = join(webDir, "demo-videos", ".results");
const out = resolve(webDir, "..", "docs", "videos");
mkdirSync(out, { recursive: true });

// The recording starts before the page has loaded; skip the blank lead-in.
const LEAD_IN = "1.5";

function ffmpeg(args) {
  const result = spawnSync("ffmpeg", ["-loglevel", "error", "-y", ...args], { stdio: "inherit" });
  if (result.status !== 0) throw new Error(`ffmpeg ${args.join(" ")} failed (${result.status})`);
}

const prefix = "demo-videos-";
const clips = existsSync(results)
  ? readdirSync(results).filter((name) => name.startsWith(prefix) && !name.endsWith("-retry1"))
  : [];
if (clips.length === 0) throw new Error(`no recordings in ${results}; run the demo config first`);

for (const dir of clips) {
  const name = dir.slice(prefix.length);
  const source = join(results, dir, "video.webm");
  if (!existsSync(source)) continue;

  const mp4 = join(out, `${name}.mp4`);
  ffmpeg([
    "-ss", LEAD_IN, "-i", source,
    "-c:v", "libx264", "-preset", "slow", "-crf", "26", "-pix_fmt", "yuv420p",
    "-movflags", "+faststart", "-an", mp4,
  ]);

  const gif = join(out, `${name}.gif`);
  ffmpeg([
    "-ss", LEAD_IN, "-i", source,
    "-filter_complex",
    "fps=8,scale=800:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle",
    gif,
  ]);

  const size = (file) => `${(statSync(file).size / 1024 / 1024).toFixed(1)} MB`;
  console.log(`${name}: ${size(mp4)} mp4, ${size(gif)} gif`);
}
