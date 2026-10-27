# Getting started

Nodevas runs in one of two ways:

- [**Standalone**](#standalone): one machine, nothing to set up. The server listens on loopback only, so there are no accounts, no mail, and no two-factor sign-in; you own every project. Start here to try it.
- [**Team server**](#team-server): a hub on a server or NAS that people open in a browser, with accounts, emailed sign-in passcodes, and runners on their own PCs.

## Standalone

### Build and run

Requirements: Go 1.26.10, Node.js 22 with npm, and Git.

```bash
git clone https://github.com/staphit/Nodevas.git
cd Nodevas
npm ci --prefix web
npm run build --prefix web
go build -o nodevas ./cmd/nodevas
./nodevas serve --project ./workspace
```

On Windows, build `nodevas.exe` and run `.\nodevas.exe serve --project .\workspace`.

Open <http://127.0.0.1:5666>. There is no sign-in page: on a loopback server the local user owns everything.

`--project` points to a workspace, not a single project. Every directory below that workspace containing `graph.yaml` appears as a project. Nodevas creates the required workspace data when the path does not exist. The frontend is built into `web/dist` and embedded into the binary.

Accounts, the audit trail, settings and the search index live in a PostgreSQL database that `serve` starts for you on `127.0.0.1` and stops with it. The first start downloads the pinned PostgreSQL build once (about 40–90 MB, checked against a SHA-256 compiled into the binary) and creates the database, which takes a few seconds; the desktop app ships the binaries and downloads nothing. Linux needs glibc 2.36 or newer (Debian 12, Ubuntu 23.04); elsewhere point `NODEVAS_DATABASE_URL` at a server. Back up with `nodevas db dump`. See [Operations: Database](./operations.md#database).

### Run coding agents too

The same command starts a runner for this server on this machine. Name the repositories agents may work in:

```bash
./nodevas serve --project ./workspace --repo ~/code/my-app
```

`--repo` is needed once: the runner remembers it. You still accept each run in the terminal running `serve`. `--no-runner` starts the server alone. See [Hub and runner on one machine](./install.md#hub-and-runner-on-one-machine).

### With Docker on Linux

The image never starts a database of its own: give it a PostgreSQL 17 server with `NODEVAS_DATABASE_URL` (or `NODEVAS_DATABASE_URL_FILE`).

```bash
docker run -d --name nodevas-db --network host \
  -e POSTGRES_USER=nodevas -e POSTGRES_PASSWORD="$DB_PASSWORD" -e POSTGRES_DB=nodevas \
  -e POSTGRES_HOST_AUTH_METHOD=scram-sha-256 -v nodevas-pg:/var/lib/postgresql/data \
  postgres:17 -c listen_addresses=127.0.0.1
docker run -d --name nodevas --network host \
  -e XDG_CONFIG_HOME=/data/.config -v nodevas-data:/data \
  -e NODEVAS_DATABASE_URL="postgres://nodevas:$DB_PASSWORD@127.0.0.1:5432/nodevas?sslmode=disable" \
  ghcr.io/staphit/nodevas:latest
```

Open <http://127.0.0.1:5666>. `--network host` keeps the server's default loopback listener on the host itself, so it stays standalone. Host networking needs Linux. `XDG_CONFIG_HOME` is required because the container user has no home directory. A plain `-p 5666:5666` does not work: a port mapping reaches the container from outside its loopback, which makes it a [team server](#team-server). The image starts without a runner (`--no-runner`): it has no coding agents. Use the binary to run them.

### Development mode

Run the backend and frontend separately:

```bash
# Terminal 1
go run ./cmd/nodevas serve -project ./workspace -port 5666

# Terminal 2
npm run dev --prefix web
```

Open <http://127.0.0.1:5173>. Vite proxies `/api` and `/ws` to port `5666`.

## Team server

The hub runs on a server or NAS and the browser is the client. Coding agents run on each person's PC through a [runner](#add-a-runner).

### Run the hub with Docker

The image is `ghcr.io/staphit/nodevas`. A hub other machines can reach needs:

- **A public host name and HTTPS.** Put it behind Cloudflare Tunnel or a reverse proxy, or give the container a certificate.
- **An admin account with a sign-in email**, created with `nodevas user add` before the first start.
- **An SMTP relay.** Web sign-in is the account's email address plus a one-time passcode sent to it, so without mail nobody can sign in.

The server refuses to start on the network without a host name, TLS (or a proxy), and an account. [Operations: Allow network access](./operations.md#allow-network-access) explains each check.

The image's default command serves `/data` on loopback only, which a port mapping cannot reach, so a hub sets its own `serve` flags or the matching `NODEVAS_SERVE_*` variables. Every container that uses the data volume needs `XDG_CONFIG_HOME=/data/.config`, because the container user (uid `568`) has no home directory, and a PostgreSQL 17 server named by `NODEVAS_DATABASE_URL_FILE` (or `NODEVAS_DATABASE_URL`): the image refuses to start without one (`NODEVAS_REQUIRE_DATABASE_URL=1`).

#### Behind a reverse proxy, with Docker Compose

The hub serves plain HTTP on a private Compose network, and your HTTPS reverse proxy (Caddy, nginx, Traefik, `cloudflared`, ...) on the same network forwards to it. Save this as `compose.yaml`:

```yaml
services:
  nodevas:
    image: ghcr.io/staphit/nodevas:latest
    restart: unless-stopped
    depends_on:
      - postgres
    environment:
      XDG_CONFIG_HOME: /data/.config
      # postgres://nodevas:PASSWORD@postgres:5432/nodevas?sslmode=disable
      NODEVAS_DATABASE_URL_FILE: /run/secrets/database_url
      NODEVAS_SERVE_LISTEN: 0.0.0.0
      NODEVAS_SERVE_HOSTNAME: nodevas.example.com
      NODEVAS_SERVE_BEHIND_PROXY: "true"
      NODEVAS_SERVE_TRUSTED_PROXY: 172.31.250.0/24
      # Plain HTTP only on the private hop from the proxy.
      NODEVAS_SERVE_ALLOW_PLAINTEXT: "true"
      NODEVAS_SERVE_SMTP_HOST: smtp.example.com
      NODEVAS_SERVE_SMTP_USER: nodevas@example.com
      NODEVAS_SERVE_SMTP_FROM: Nodevas <nodevas@example.com>
      NODEVAS_SMTP_PASSWORD_FILE: /run/secrets/smtp_password
    volumes:
      - nodevas-data:/data
    secrets:
      - smtp_password
      - database_url
    networks:
      - nodevas
    # No "ports:": only the proxy reaches the hub, at http://nodevas:5666.

  postgres:
    image: postgres:17
    restart: unless-stopped
    environment:
      POSTGRES_USER: nodevas
      POSTGRES_DB: nodevas
      POSTGRES_PASSWORD_FILE: /run/secrets/database_password
    volumes:
      - nodevas-pg:/var/lib/postgresql/data
    secrets:
      - database_password
    networks:
      - nodevas

  # Add your HTTPS reverse proxy here, on the nodevas network.

networks:
  nodevas:
    ipam:
      config:
        - subnet: 172.31.250.0/24

volumes:
  nodevas-data:
  nodevas-pg:

secrets:
  smtp_password:
    file: ./smtp-password
  database_password:
    file: ./database-password
  database_url:
    file: ./database-url
```

1. Put the SMTP password in `smtp-password` and the database URL in `database-url`, both readable by uid `568`, and the database password in `database-password`.
2. Create the admin account (the password is read from stdin):

   ```bash
   printf '%s' "$ADMIN_PASSWORD" | docker compose run --rm -T nodevas \
     user add --project /data --user admin --role admin \
     --email you@example.com --password-stdin
   ```

3. Add your proxy, pointing `https://nodevas.example.com` at `http://nodevas:5666`, and run `docker compose up -d`.

`NODEVAS_SERVE_TRUSTED_PROXY` trusts forwarding headers from the whole Compose network, so put only the hub and the proxy on it. Each `NODEVAS_SERVE_*` variable matches a `serve` flag and a `nodevas.yaml` key; see [Operations: Configuration](./operations.md#configuration).

#### With your own certificate

1. Create the account (`-i` passes the password on stdin):

   ```bash
   printf '%s' "$ADMIN_PASSWORD" | docker run --rm -i \
     -e XDG_CONFIG_HOME=/data/.config -v nodevas-data:/data \
     -e NODEVAS_DATABASE_URL_FILE=/run/secrets/database-url \
     -v /etc/nodevas/database-url:/run/secrets/database-url:ro \
     ghcr.io/staphit/nodevas:latest \
     user add --project /data --user admin --role admin \
     --email you@example.com --password-stdin
   ```

2. Start the hub with the certificate, host name, and mail settings. The certificate, key, and password files must be readable by uid `568`:

   ```bash
   docker run -d --name nodevas --restart unless-stopped -p 443:5666 --no-healthcheck \
     -e XDG_CONFIG_HOME=/data/.config -v nodevas-data:/data \
     -v /etc/nodevas/tls:/tls:ro \
     -v /etc/nodevas/smtp-password:/run/secrets/smtp-password:ro \
     -v /etc/nodevas/database-url:/run/secrets/database-url:ro \
     -e NODEVAS_DATABASE_URL_FILE=/run/secrets/database-url \
     -e NODEVAS_SERVE_SMTP_HOST=smtp.example.com \
     -e NODEVAS_SERVE_SMTP_USER=nodevas@example.com \
     -e 'NODEVAS_SERVE_SMTP_FROM=Nodevas <nodevas@example.com>' \
     -e NODEVAS_SMTP_PASSWORD_FILE=/run/secrets/smtp-password \
     ghcr.io/staphit/nodevas:latest \
     serve --project /data --listen 0.0.0.0 --hostname nodevas.example.com \
     --tls-cert /tls/cert.pem --tls-key /tls/key.pem
   ```

   `/etc/nodevas/database-url` holds the `postgres://` URL of a PostgreSQL 17 server. The image's health check probes plain HTTP, so it is turned off here; with TLS it would always report unhealthy.

Without Docker, run the binary as a service and follow [Operations: Allow network access](./operations.md#allow-network-access).

### Open the hub

Open `https://nodevas.example.com` in a browser, enter the account's email address, and type the passcode it receives. There is nothing else to install to use the board. Turn on an authenticator app under **Settings → Your account** for a second factor.

### Add a runner

A runner runs coding agents on your PC with your own logins and connects out to the hub. You need it only for agent runs.

`scripts/install.sh` downloads the `nodevas` build that matches your hub and checks it against GitHub, or builds it from a checkout with Go 1.26 or newer (see [Install a runner](./install.md)). This PC also needs Git, the agent CLIs you want to use, and `gh` for pull requests. On Windows, run it in Git Bash.

1. On the hub, open **Settings → Runners → Link a new device** and copy the one-time link. It works once, for ten minutes.
2. On your PC:

   ```bash
   git clone https://github.com/staphit/Nodevas.git
   cd Nodevas
   scripts/install.sh --hub https://nodevas.example.com --link --repo ~/code/my-app
   ```

3. Paste the link when asked. The script logs the runner in, allows runs in each `--repo`, and starts the runner at login.

Options, runner tokens, and Cloudflare Access: [Install a runner](./install.md).

## Configuration

Settings come from built-in defaults, then `nodevas.yaml` in the workspace root, then environment variables (`NODEVAS_SERVE_*`), then command-line flags. See [nodevas.yaml.example](../nodevas.yaml.example) and [Operations: Configuration](./operations.md#configuration).

Log format and audit events: [Operations: Logs and audit events](./operations.md#logs-and-audit-events).

## Next steps

- [Concepts](./concepts.md) — understand nodes, dependencies, and readiness.
- [MCP integration](./mcp.md) — connect an AI agent.
- [Install a runner](./install.md) — every option of the runner install script.
- [Operations](./operations.md) — configuration, network access, the database, and logs.
- [Storage and collaboration](./collaboration.md) — share a workspace safely.
