import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes("/node_modules/@lezer/")) {
            return "editor-parser";
          }
          if (id.includes("/node_modules/@codemirror/")) {
            return "editor-core";
          }
          if (id.includes("/node_modules/@uiw/react-codemirror/")) {
            return "editor-react";
          }
          if (id.includes("/node_modules/marked/") || id.includes("/node_modules/dompurify/")) {
            return "markdown";
          }
        },
      },
    },
  },
  server: {
    port: 5173,
    // The backend only trusts a loopback Origin on its own port, so the proxy
    // presents itself as the backend instead of forwarding the dev server's
    // origin on :5173, which the server has no reason to trust.
    proxy: {
      "/api": {
        target: "http://127.0.0.1:5666",
        changeOrigin: true,
        headers: { Origin: "http://127.0.0.1:5666" },
      },
      "/ws": {
        target: "ws://127.0.0.1:5666",
        ws: true,
        changeOrigin: true,
        headers: { Origin: "http://127.0.0.1:5666" },
      },
    },
  },
});
