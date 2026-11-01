import type { ClientRequest, IncomingMessage } from "node:http";
import { isIP } from "node:net";
import { defineConfig, type ProxyOptions } from "vite";
import react from "@vitejs/plugin-react";

// The Go server the dev server proxies /api and /ws to. On loopback it has no
// sign-in: every request it gets is the local person. So the proxy passes on
// only what the dev server's own pages send.
const backend = "http://127.0.0.1:5666";

// hostName is a Host header's name, without the port or IPv6 brackets.
function hostName(host: string): string {
  const name = host.startsWith("[") ? host.slice(1, host.indexOf("]")) : host.replace(/:\d*$/, "");
  return name.toLowerCase();
}

// devPageOnly refuses (404) a request that is not from a page this dev
// server served: a Host that is not this machine or an IP literal (a name
// rebound to this machine; Vite checks Host on plain requests but not on
// proxied WebSocket upgrades), an Origin other than the dev server's own,
// or a browser saying the request came from another site.
const devPageOnly: ProxyOptions["bypass"] = (req) => {
  const host = req.headers.host ?? "";
  const name = hostName(host);
  if (name !== "localhost" && !name.endsWith(".localhost") && isIP(name) === 0) return false;
  const origin = req.headers.origin;
  if (origin !== undefined && origin !== `http://${host}` && origin !== `https://${host}`) return false;
  const site = req.headers["sec-fetch-site"];
  if (site !== undefined && site !== "same-origin" && site !== "none") return false;
  return undefined;
};

function loopbackPeer(req: IncomingMessage): boolean {
  const address = req.socket?.remoteAddress ?? "";
  return address === "::1" || address.startsWith("127.") || address.startsWith("::ffff:127.");
}

// presentToBackend runs on every request devPageOnly let through. The backend
// trusts only its own origin, so a page's Origin becomes the backend's.
// xfwd has named the client in X-Forwarded-For: a peer on the network stays
// visible as forwarded, while the developer's own browser reaches the backend
// as the direct local request it is.
const presentToBackend: ProxyOptions["configure"] = (proxy) => {
  const present = (proxyReq: ClientRequest, req: IncomingMessage) => {
    if (req.headers.origin !== undefined) proxyReq.setHeader("Origin", backend);
    if (loopbackPeer(req)) {
      for (const header of ["x-forwarded-for", "x-forwarded-host", "x-forwarded-port", "x-forwarded-proto"]) {
        proxyReq.removeHeader(header);
      }
    }
  };
  proxy.on("proxyReq", present);
  proxy.on("proxyReqWs", present);
};

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          // React first, in a chunk of its own. Left to Rollup it lands in
          // whichever chunk imports it first — the CodeMirror bindings — and
          // index.html then modulepreloads the whole editor on first paint.
          if (/\/node_modules\/(react|react-dom|scheduler)\//.test(id)) {
            return "react";
          }
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
          // Only an interactive run's terminal loads it (RunModeTabs).
          if (id.includes("/node_modules/@xterm/")) {
            return "terminal";
          }
        },
      },
    },
  },
  server: {
    port: 5173,
    // The backend only trusts a loopback Origin on its own port, so the proxy
    // presents the dev server's own pages as the backend (presentToBackend)
    // and refuses everything else (devPageOnly).
    proxy: {
      "/api": {
        target: backend,
        // An interactive run's terminal is a WebSocket under /api/runs/:id/terminal.
        ws: true,
        changeOrigin: true,
        xfwd: true,
        bypass: devPageOnly,
        configure: presentToBackend,
      },
      "/ws": {
        target: "ws://127.0.0.1:5666",
        ws: true,
        changeOrigin: true,
        xfwd: true,
        bypass: devPageOnly,
        configure: presentToBackend,
      },
    },
  },
});
