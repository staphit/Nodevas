# Nodevas Security & Deployment Guide

This document outlines the security architecture, abuse protection mechanisms, and deployment best practices for running Nodevas in production environments.

---

## 1. Network Topology & Reverse Proxy Architecture

Nodevas is designed to operate behind a secure reverse proxy (such as Nginx, Caddy, Cloudflare, or AWS ALB) terminating TLS and enforcing edge-level connection policies.

```
+------------------+       +-------------------+       +-----------------------+
|  Public Internet | ----> |   Reverse Proxy   | ----> |    Nodevas Server     |
|   (Web / MCP)    | HTTPS | (Nginx / Caddy)   | HTTP  | (127.0.0.1:5666 loop) |
+------------------+       +-------------------+       +-----------------------+
```

### Key Principles

1. **Private/Loopback Binding**: Nodevas should bind to `127.0.0.1` or an isolated private Docker/VPC network (`10.0.0.0/8`, `172.16.0.0/12`), never directly on a public IP.
2. **TLS Termination**: The reverse proxy terminates TLS with modern cipher suites and redirects plaintext HTTP to HTTPS.
3. **Strict Proxy Trust**: Nodevas only trusts `X-Forwarded-For` headers when the immediate connecting peer matches configured CIDR ranges (`--trusted-proxy`).

---

## 2. Proxy Configuration

Production-ready configurations are provided in `deploy/`:

- **Nginx**: [`deploy/nginx/nodevas.conf`](../deploy/nginx/nodevas.conf)
- **Caddy**: [`deploy/caddy/Caddyfile`](../deploy/caddy/Caddyfile)
- **Docker Compose Security Lab**: [`compose.security.yaml`](../compose.security.yaml)

### Required Reverse Proxy Capabilities

- **Rate Limiting**: Apply connection limits (e.g. `limit_conn 30`) and request burst zones (e.g. `60r/s`, burst `120`).
- **Body & Header Limits**: Limit `client_max_body_size` to 32MB and large header buffers to prevent Slowloris and memory exhaustion.
- **WebSocket Upgrade**: Pass `Upgrade` and `Connection` headers for `/ws` with unbuffered streaming (`proxy_buffering off;`).
- **Security Headers**: Emit `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Cross-Origin-Opener-Policy: same-origin`, and `Referrer-Policy: strict-origin-when-cross-origin`.

---

## 3. Nodevas Configuration Flags & Environment Variables

When running behind a reverse proxy, launch Nodevas with:

```bash
nodevas serve \
  --project /var/lib/nodevas/workspace \
  --listen 127.0.0.1 \
  --port 5666 \
  --hostname nodevas.example.com \
  --behind-proxy=true \
  --trusted-proxy 127.0.0.1/32,::1/128,172.16.0.0/12 \
  --allow-plaintext=true
```

Or configure via environment variables:

| Environment Variable | Description | Recommended Production Value |
|----------------------|-------------|------------------------------|
| `NODEVAS_SERVE_LISTEN` | Bind IP address | `127.0.0.1` |
| `NODEVAS_SERVE_PORT` | Bind port | `5666` |
| `NODEVAS_SERVE_HOSTNAME` | Public FQDN | `nodevas.example.com` |
| `NODEVAS_SERVE_BEHIND_PROXY` | Enable reverse-proxy mode | `true` |
| `NODEVAS_SERVE_TRUSTED_PROXY` | Comma-separated trusted CIDRs | `127.0.0.1/32,::1/128` |
| `NODEVAS_SERVE_ALLOW_PLAINTEXT` | Allow HTTP on local proxy hop | `true` |
| `NODEVAS_SERVE_LOG_LEVEL` | Log level (`info`, `warn`, `error`) | `info` |
| `NODEVAS_SERVE_LOG_FORMAT` | Log format (`json`, `text`) | `json` |

> [!CAUTION]
> Never set `--trusted-proxy` to `0.0.0.0/0` or wildcard ranges. Setting broad CIDRs allows arbitrary clients to spoof their client IP via `X-Forwarded-For` and bypass IP-based rate limiting and audit trails.

---

## 4. Multi-Tier Application Abuse Protection

Nodevas includes internal multi-dimensional rate limiters and concurrency gates to protect server resources:

1. **Pre-Auth Limiter (Edge Shield)**:
   - Intercepts requests to `/api/` before authentication parsing to prevent bcrypt/DB load from unauthenticated flooders.
   - Global bucket: 200 req/s, burst 400.
   - Per-IP bucket: 40 req/s, burst 80.
   - SPA static assets (`/`, `/index.html`, `/assets/*`) bypass pre-auth rate limiting so legitimate users can always load the UI.

2. **Route Cost & Classification**:
   - `ClassPublicAuth` (`cost 1.0`): Sign-in, OTP requests, auth status.
   - `ClassRead` (`cost 1.0`): Graph queries, state polling, page reads.
   - `ClassWrite` (`cost 1.0`): Graph modifications, page saves.
   - `ClassExpensive` (`cost 2.0`): DSL validation, workspace search, history diff, audit queries.
   - `ClassHeavy` (`cost 2.0`): DOCX/ZIP imports and exports, remote cloud synchronizations.
   - `ClassWSUpgrade` (`cost 1.0`): WebSocket handshake requests.

3. **Multi-Dimensional Enforcement**:
   - Requests are checked against **Global**, **IP**, and **Actor** buckets simultaneously.
   - Rate limit responses return `429 Too Many Requests`, a `Retry-After: <seconds>` header, and a standardized JSON payload: `{"error": "too many requests"}`.

4. **Concurrency Semaphores**:
   - Heavy imports/exports and DOCX conversions run under bounded concurrency gates (`MaxConcurrentHeavy: 4`, `MaxConcurrentDOCX: 2`, `MaxConcurrentRemote: 2`).
   - Saturated gates fail fast with `429 Too Many Requests` (`{"error": "server is busy"}`), preventing server thread exhaustion.

---

## 5. Volumetric DDoS vs. Application Abuse Protection

| Threat Type | Primary Defense Layer | Why Application Limiting Alone Is Insufficient |
|-------------|-----------------------|------------------------------------------------|
| **Volumetric DDoS** (L3/L4 SYN flood, UDP flood, multi-Gbps amplification) | **Upstream CDN / WAF / Cloud Provider** (Cloudflare, AWS Shield, GCP Cloud Armor) | Saturated network interfaces and OS socket buffers block packets before Go's `net/http` server can process them. |
| **HTTP Request Floods (L7)** | **Reverse Proxy + Nodevas Pre-Auth Limiter** | Nginx/Caddy absorbs TCP handshakes; Nodevas rejects unauthenticated `/api/` spam before auth queries run. |
| **Resource Exhaustion** (CPU-heavy DOCX parsing, heavy SQLite queries) | **Nodevas Route Class Limiter & Concurrency Semaphores** | Limits concurrent expensive tasks and token budgets per actor and per IP. |
| **Credential Stuffing / OTP Floods** | **Nodevas OTP Cooldown + PublicAuth Bucket** | Strict 30-second cooldown per account and rate-limited OTP generation. |

---

## 6. Verification & Security Testing

### Verifying Spoofed `X-Forwarded-For` Headers Are Safely Ignored

Nodevas uses right-to-left IP extraction (`auth.ClientIP`). It walks back through proxy IPs and stops at the first IP not in `--trusted-proxy`.

#### Test Case 1: Direct connection attempting to spoof `X-Forwarded-For`
```bash
# Connect directly to Nodevas bypassing trusted proxy
curl -s -i -H "X-Forwarded-For: 8.8.8.8" http://127.0.0.1:5666/api/auth/status
```
*Result*: The server logs and records `127.0.0.1` (the actual TCP peer), NOT `8.8.8.8`.

#### Test Case 2: Proxy hop within trusted CIDR
When the request originates from a trusted reverse proxy IP (e.g. `127.0.0.1`), Nodevas trusts the immediate upstream header and attributes the client IP accurately to the real client IP.

### Inspecting Limiter Health (Admin Only)

Administrators can inspect live rate limiter statistics at `GET /api/audit/limiter`:

```bash
curl -s -H "Cookie: nodevas_session=<ADMIN_SESSION>" https://nodevas.example.com/api/audit/limiter | jq .
```

Example Response:
```json
{
  "active_buckets": 12,
  "rejected_total": 4,
  "rejected_by_class": {
    "heavy": 2,
    "read": 1,
    "public-auth": 1
  },
  "semaphores": {
    "heavy": { "capacity": 4, "in_use": 0 },
    "docx": { "capacity": 2, "in_use": 0 },
    "remote": { "capacity": 2, "in_use": 0 }
  }
}
```
