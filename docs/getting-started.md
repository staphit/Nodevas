# Getting started

This guide covers building Nodevas from source, running it locally, and configuring a server.

## Requirements

- Go 1.25.13
- Node.js 22 and npm
- Git

## Build from source

```bash
git clone https://github.com/staphit/Nodevas.git
cd Nodevas

npm ci --prefix web
npm run build --prefix web
```

The frontend is built into `web/dist` and embedded into the Go binary.

Windows:

```powershell
go build -o nodevas.exe ./cmd/nodevas
```

macOS or Linux:

```bash
go build -o nodevas ./cmd/nodevas
```

## Start a local server

`-project` points to a workspace, not a single project. Every directory below that workspace containing `graph.yaml` appears as a project. Nodevas creates the required workspace data when the path does not exist.

```bash
go run ./cmd/nodevas serve -project ./workspace -port 5666
```

Open <http://127.0.0.1:5666>.

With a built executable:

```bash
./nodevas serve -project ./workspace -port 5666
```

On Windows:

```powershell
.\nodevas.exe serve -project .\workspace -port 5666
```

The default listener is loopback-only. For collaboration, use a shared deployment or place Nodevas behind a correctly configured HTTPS reverse proxy.

## Development mode

Run the backend and frontend separately:

```bash
# Terminal 1
go run ./cmd/nodevas serve -project ./workspace -port 5666

# Terminal 2
npm run dev --prefix web
```

Open <http://127.0.0.1:5173>. Vite proxies `/api` and `/ws` to port `5666`.

## Configuration

Configuration precedence is:

```text
built-in defaults < YAML < environment variables < command-line flags
```

By default, Nodevas reads `nodevas.yaml` from the workspace root. Use `-config` or `NODEVAS_CONFIG` to select another file. See [nodevas.yaml.example](../nodevas.yaml.example).

```yaml
listen: 127.0.0.1
port: 5666
hostname: ""
behind_proxy: false
trusted_proxy: 127.0.0.1/32,::1/128
tls_cert: ""
tls_key: ""
allow_plaintext: false
max_active_users: 0

smtp:
  host: ""
  port: 587
  user: ""
  from: ""
  security: starttls

logging:
  level: info
  format: json
```

Keep the SMTP password out of YAML. Set `NODEVAS_SMTP_PASSWORD` through a secret store or the process environment. Deployment environments can use `NODEVAS_SERVE_*` variables to override YAML values.

## Allow network access

Create an account before allowing another machine to connect:

```bash
printf '%s' 'PASSWORD' | ./nodevas user add \
  --project ./workspace --user USER_NAME --role admin --password-stdin
```

Use a secure stdin mechanism and do not put a real password in shell history. For production, use HTTPS directly or put Nodevas behind a reverse proxy that terminates TLS:

```bash
./nodevas serve -project ./workspace -port 5666 \
  -listen 127.0.0.1 -hostname nodevas.example.com -behind-proxy
```

`-allow-plaintext` is for controlled testing only. Configure `trusted_proxy` to match the proxy addresses that are allowed to forward client information.

## Next steps

- [Concepts](./concepts.md) — understand nodes, dependencies, and readiness.
- [MCP integration](./mcp.md) — connect an AI agent.
- [Storage and collaboration](./collaboration.md) — share a workspace safely.
- [OCI deployment](../deploy/oci/README.md) — run a shared cloud server.
