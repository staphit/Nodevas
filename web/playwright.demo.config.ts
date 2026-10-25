import { defineConfig } from "@playwright/test";

const PORT = Number(process.env.VISED_E2E_PORT ?? 5702);

/**
 * The README's feature videos.
 *
 * Not a test run: e2e/demo-videos.spec.ts drives the real app through one
 * feature per test while Playwright records the page, and e2e/demo-encode.mjs
 * turns the recordings into the GIF and MP4 files under docs/videos/. The
 * hub's runner, run, autopilot, usage and agent routes are answered by the
 * spec, so no runner or agent has to exist. Run it with:
 *
 *   npm run demo:videos
 */
export default defineConfig({
  testDir: "./e2e",
  testMatch: /demo-videos\.spec\.ts/,
  timeout: 180_000,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  outputDir: "demo-videos/.results",
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    viewport: { width: 1440, height: 900 },
    video: { mode: "on", size: { width: 1440, height: 900 } },
    colorScheme: "light",
    locale: "en-US",
  },
  webServer: {
    command: "node e2e/demo-serve.mjs",
    env: { VISED_E2E_PORT: String(PORT) },
    url: `http://127.0.0.1:${PORT}/api/projects`,
    timeout: 300_000,
    reuseExistingServer: false,
    stdout: "ignore",
    stderr: "pipe",
  },
});
