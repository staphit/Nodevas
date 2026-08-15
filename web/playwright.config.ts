import { defineConfig, devices } from "@playwright/test";

/**
 * End-to-end tests run against the real binary: vite build → go build (once,
 * in e2e/global-setup.ts) → each worker serves its own throwaway workspace on
 * its own port (e2e/fixture.ts). That covers the embedded bundle and the Go
 * API, which a jsdom test cannot — and the per-worker servers are what let
 * the suite run in parallel at all: one server only holds one active project
 * at a time.
 *
 * fullyParallel stays off so tests inside a file keep their order; whole files
 * are distributed across workers instead.
 */
export default defineConfig({
  testDir: "./e2e",
  // A screenshot run, not a test suite — it has its own config
  // (playwright.mobile.config.ts) and has no place in an e2e pass.
  testIgnore: /mobile-preview\.spec\.ts/,
  timeout: 30_000,
  expect: { timeout: 7_000 },
  fullyParallel: false,
  // CI runners have 4 vCPUs; three servers plus three browsers is the most
  // that fits without thrashing. Locally the Playwright default applies.
  workers: process.env.CI ? 3 : undefined,
  globalSetup: "./e2e/global-setup",
  reporter: process.env.CI ? "list" : [["list"], ["html", { open: "never" }]],
  use: {
    // baseURL is provided per worker by the workerServer fixture.
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    viewport: { width: 1440, height: 900 },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
