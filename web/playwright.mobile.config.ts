import { defineConfig } from "@playwright/test";

const PORT = Number(process.env.VISED_E2E_PORT ?? 5701);

/**
 * Screenshots of the real app at phone and tablet sizes.
 *
 * Separate from playwright.config.ts because this is not a test run: nothing
 * here asserts, and the point is the PNGs it leaves in web/mobile-preview/.
 * It exists because the alternative — build the iOS framework on a Mac, sign,
 * install, look — is a long round trip to answer "is the drawer covering the
 * board again", and the layout being checked is the web UI, which needs no
 * device to render.
 *
 * A device profile is not a device. Touch gestures are synthesised, iOS Safari
 * is not Chromium, and the safe-area insets are zero here because there is no
 * notch. For those, open the app on the phone itself over the LAN — see
 * scripts/serve-lan.md.
 */
export default defineConfig({
  testDir: "./e2e",
  testMatch: /mobile-preview\.spec\.ts/,
  timeout: 60_000,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    // A phone screenshot at 1x is unreadable on a desktop monitor, and 3x is
    // what an iPhone actually renders at.
    deviceScaleFactor: 2,
    hasTouch: true,
    isMobile: true,
  },
  projects: [
    {
      name: "iphone-se",
      use: { viewport: { width: 375, height: 667 } },
    },
    {
      name: "iphone-15",
      use: { viewport: { width: 393, height: 852 } },
    },
    {
      name: "ipad-portrait",
      // An iPad reports a fine pointer only once something is paired with it,
      // so the touch controls are expected here too.
      use: { viewport: { width: 834, height: 1194 } },
    },
    {
      name: "ipad-landscape",
      use: { viewport: { width: 1194, height: 834 } },
    },
  ],
  webServer: {
    command: "node e2e/serve.mjs",
    // A different port from the test suite's, so a preview run and an e2e run
    // do not fight over the listener or each other's workspace.
    env: { VISED_E2E_PORT: String(PORT) },
    url: `http://127.0.0.1:${PORT}/api/projects`,
    timeout: 300_000,
    reuseExistingServer: false,
    stdout: "pipe",
    stderr: "pipe",
  },
});
