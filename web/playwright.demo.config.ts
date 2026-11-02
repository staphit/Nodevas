import { defineConfig } from "@playwright/test";

const PORT = Number(process.env.VISED_E2E_PORT ?? 5702);

/**
 * The README's feature videos.
 *
 * Not a test run: e2e/demo-videos.spec.ts drives the real app through one
 * feature per test while it records the page, and e2e/demo-encode.mjs turns
 * the recordings into the MP4 files under docs/videos/. The
 * hub's runner, run, autopilot, usage and agent routes are answered by the
 * spec, so no runner or agent has to exist. Run it with:
 *
 *   npm run demo:videos
 *
 * e2e/demo-stills.spec.ts takes the README's Station stills and the feature
 * tour's frames the same way; e2e/demo-tour.mjs makes the tour's GIF:
 *
 *   npm run demo:stills
 *
 * Each run empties demo-videos/.results, so encode a run's footage before
 * starting the next.
 */
export default defineConfig({
  testDir: "./e2e",
  testMatch: /demo-(videos|stills)\.spec\.ts/,
  timeout: 180_000,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  outputDir: "demo-videos/.results",
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    // Drawn at twice the pixels, so the camera can push in without blurring
    // the UI. The spec records the page itself (Playwright's video is in CSS
    // pixels).
    viewport: { width: 1440, height: 810 },
    deviceScaleFactor: 2,
    video: "off",
    colorScheme: "dark",
    locale: "en-US",
  },
  webServer: {
    command: "node e2e/demo-serve.mjs",
    env: { VISED_E2E_PORT: String(PORT) },
    url: `http://127.0.0.1:${PORT}/api/projects`,
    timeout: 300_000,
    // DEMO_REUSE_SERVER=1: a demo-serve.mjs already running on the port (re-takes).
    reuseExistingServer: process.env.DEMO_REUSE_SERVER === "1",
    stdout: "ignore",
    stderr: "pipe",
  },
});
