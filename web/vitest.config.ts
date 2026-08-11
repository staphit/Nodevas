import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// Separate from vite.config.ts so the app build stays free of test-only config.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./vitest.setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    restoreMocks: true,
  },
});
