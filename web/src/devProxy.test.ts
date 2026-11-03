// @vitest-environment node
import { EventEmitter } from "node:events";
import type { IncomingMessage } from "node:http";
import type { ProxyOptions } from "vite";
import { describe, expect, it } from "vitest";
import config from "../vite.config";

// The dev server proxies /api and /ws to the Go server on loopback, where a
// request is the local person with no sign-in. Whatever the proxy forwards
// therefore has to be what the developer's own page sent: a page from another
// site, or a name rebound to this machine, must not ride the proxy in.

const proxies = (config.server?.proxy ?? {}) as Record<string, ProxyOptions>;
const entries = ["/api", "/ws"].map((path) => [path, proxies[path]] as const);

function request(headers: Record<string, string>, remoteAddress = "127.0.0.1"): IncomingMessage {
  return { url: "/ws", headers, socket: { remoteAddress } } as unknown as IncomingMessage;
}

async function refused(entry: ProxyOptions, req: IncomingMessage): Promise<boolean> {
  if (!entry.bypass) return false;
  return (await entry.bypass(req, undefined, entry)) === false;
}

// fakeProxyReq is the outgoing request http-proxy hands to proxyReq and
// proxyReqWs listeners, carrying the headers it has already set.
function fakeProxyReq(headers: Record<string, string>) {
  const out = new Map(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]));
  return {
    out,
    setHeader: (name: string, value: string) => out.set(name.toLowerCase(), value),
    removeHeader: (name: string) => out.delete(name.toLowerCase()),
    getHeader: (name: string) => out.get(name.toLowerCase()),
  };
}

describe.each(entries)("dev proxy %s", (_path, entry) => {
  it("forges no Origin of its own and names the client to the backend", () => {
    expect(entry).toBeDefined();
    expect((entry.headers as Record<string, string> | undefined)?.Origin).toBeUndefined();
    expect(entry.xfwd).toBe(true);
  });

  it("refuses a page from another site", async () => {
    const evil = request({ host: "localhost:5173", origin: "https://evil.example", "sec-fetch-site": "cross-site" });
    expect(await refused(entry, evil)).toBe(true);
    const sibling = request({ host: "localhost:5173", origin: "http://localhost:3000", "sec-fetch-site": "same-site" });
    expect(await refused(entry, sibling)).toBe(true);
    const nullOrigin = request({ host: "localhost:5173", origin: "null" });
    expect(await refused(entry, nullOrigin)).toBe(true);
    // A no-cors request carries no Origin, but the browser still says where it came from.
    const noCors = request({ host: "localhost:5173", "sec-fetch-site": "cross-site" });
    expect(await refused(entry, noCors)).toBe(true);
  });

  it("refuses a name rebound to this machine", async () => {
    const rebound = request({
      host: "rebind.attacker.example:5173",
      origin: "http://rebind.attacker.example:5173",
      "sec-fetch-site": "same-origin",
    });
    expect(await refused(entry, rebound)).toBe(true);
  });

  // A tunnel or reverse proxy on this machine (ngrok with --host-header, say)
  // reaches the dev server from loopback, but says in its headers that the
  // request came from elsewhere. The proxy makes a loopback peer's request
  // look direct to the backend, so it must not let such a request through.
  it("refuses a request a local tunnel forwarded from elsewhere", async () => {
    const forwarded: Record<string, string>[] = [
      { "x-forwarded-for": "203.0.113.9" },
      { "x-forwarded-for": "203.0.113.9, 127.0.0.1" },
      { "x-forwarded-host": "shared.ngrok.example" },
      { forwarded: "for=203.0.113.9" },
      { "x-real-ip": "203.0.113.9" },
      { "cf-connecting-ip": "203.0.113.9" },
    ];
    for (const headers of forwarded) {
      const tunnelled = request({ host: "localhost:5173", "sec-fetch-site": "same-origin", ...headers });
      expect(await refused(entry, tunnelled), JSON.stringify(headers)).toBe(true);
      const ipv6 = request({ host: "localhost:5173", ...headers }, "::1");
      expect(await refused(entry, ipv6), JSON.stringify(headers)).toBe(true);
    }
  });

  it("passes the dev server's own pages, a phone on the LAN and curl", async () => {
    const own = request({ host: "localhost:5173", origin: "http://localhost:5173", "sec-fetch-site": "same-origin" });
    expect(await refused(entry, own)).toBe(false);
    const loopbackIP = request({ host: "127.0.0.1:5173", origin: "http://127.0.0.1:5173" });
    expect(await refused(entry, loopbackIP)).toBe(false);
    const ipv6 = request({ host: "[::1]:5173", origin: "http://[::1]:5173" });
    expect(await refused(entry, ipv6)).toBe(false);
    const phone = request(
      { host: "192.168.1.23:5173", origin: "http://192.168.1.23:5173", "sec-fetch-site": "same-origin" },
      "192.168.1.50",
    );
    expect(await refused(entry, phone)).toBe(false);
    expect(await refused(entry, request({ host: "localhost:5173" }))).toBe(false);
  });

  it("presents an allowed page to the backend as the backend's own origin", () => {
    const proxy = new EventEmitter();
    entry.configure?.(proxy as never, entry);
    for (const event of ["proxyReq", "proxyReqWs"]) {
      const own = fakeProxyReq({ origin: "http://localhost:5173", "x-forwarded-for": "127.0.0.1" });
      proxy.emit(event, own, request({ host: "localhost:5173", origin: "http://localhost:5173" }), undefined, entry);
      expect(own.getHeader("origin")).toBe("http://127.0.0.1:5666");
      // The developer's own browser reaches the backend as a direct local request.
      expect(own.getHeader("x-forwarded-for")).toBeUndefined();

      const phone = fakeProxyReq({ origin: "http://192.168.1.23:5173", "x-forwarded-for": "192.168.1.50" });
      proxy.emit(
        event,
        phone,
        request({ host: "192.168.1.23:5173", origin: "http://192.168.1.23:5173" }, "192.168.1.50"),
        undefined,
        entry,
      );
      expect(phone.getHeader("origin")).toBe("http://127.0.0.1:5666");
      // Anyone else stays visible as forwarded.
      expect(phone.getHeader("x-forwarded-for")).toBe("192.168.1.50");

      const bare = fakeProxyReq({});
      proxy.emit(event, bare, request({ host: "localhost:5173" }), undefined, entry);
      expect(bare.getHeader("origin")).toBeUndefined();
    }
  });
});
