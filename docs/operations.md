# Operations

Running a Nodevas server: configuration, letting other machines connect, the database, and reading its logs. To install Nodevas, see [Getting started](./getting-started.md).

## Configuration

Configuration precedence is:

```text
built-in defaults < YAML < environment variables < command-line flags
```

By default, Nodevas reads `nodevas.yaml` from the workspace root (or, without `--project`, the current directory). Use `-config` or `NODEVAS_CONFIG` to select another file, which then replaces it. See [nodevas.yaml.example](../nodevas.yaml.example).

The workspace's own `nodevas.yaml` is workspace content, as easily shared or cloned as the notes beside it, so it may not change whom the server trusts. `serve` refuses to start when that file sets any of these:

- a `hostname` that is not this machine (`localhost` or a loopback address);
- `behind_proxy: true`, a `trusted_proxy` other than the default, `tls_cert` or `tls_key`, `allow_plaintext: true`;
- anything under `access`;
- `copilot.allow_bypass: true`, a `copilot.base_url` that is not plain http(s) to this machine without credentials, or an `smtp.host` that is not this machine.

Set those in a file named with `-config` or `NODEVAS_CONFIG` and kept outside the workspace (in Docker, outside the data volume), with `NODEVAS_SERVE_*` / `NODEVAS_COPILOT_*` variables, or with flags. `port`, `listen`, `logging`, the other `smtp` keys, `abuse` and a copilot model on this machine stay allowed in the workspace file.

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

Keep the SMTP password out of YAML. Set `NODEVAS_SMTP_PASSWORD` through a secret store or the process environment. Containers can instead set `NODEVAS_SMTP_PASSWORD_FILE` to a mounted secret file. Do not set both variables. Deployment environments can use `NODEVAS_SERVE_*` variables to override YAML values.

The file is strict: an unknown key stops `serve`. Rate limits (`abuse.*`) are set the same three ways; `nodevas serve -h` lists them.

**WebSocket connections.** The hub keeps at most 512 open, 16 per account and 32 per client address (each open browser tab holds one); one more gets `429`. Change the caps with `--ws-max-conns`, `--ws-max-conns-per-account` and `--ws-max-conns-per-ip`, or `NODEVAS_SERVE_WS_MAX_CONNS`, `NODEVAS_SERVE_WS_MAX_CONNS_PER_ACCOUNT` and `NODEVAS_SERVE_WS_MAX_CONNS_PER_IP`; they have no YAML key. People behind one address, such as an office network, share the per-address cap.

## Allow network access

A server that listens beyond loopback, sits behind a proxy, has a `-hostname` that is not this machine, or has Cloudflare Access configured is a networked hub. It needs all of the following, refuses to start without the first three (the host name only when it listens on every interface), and never starts a runner of its own:

- **A host name.** Listening on every interface (`0.0.0.0`) needs `-hostname`, the name browsers use, to prevent DNS rebinding.
- **TLS.** Pass `-tls-cert` and `-tls-key`, or listen on loopback behind an HTTPS reverse proxy with `-behind-proxy`. `-allow-plaintext` is for controlled testing, or a private hop to a proxy, only. A loopback `-listen` with a public `-hostname` or Access settings is reached through a proxy or tunnel on this machine, so it refuses to start unless `-behind-proxy`, TLS or `-allow-plaintext` is given.
- **An account.** Create one before the first start.
- **Outgoing mail.** Web sign-in is the account's email address plus a one-time passcode sent to it, so without `smtp` settings nobody can sign in.

Create the account with its sign-in address:

```bash
printf '%s' 'PASSWORD' | ./nodevas user add \
  --project ./workspace --user USER_NAME --role admin \
  --email you@example.com --password-stdin
```

Use a secure stdin mechanism and do not put a real password in shell history. `nodevas user email --user USER_NAME --email ...` sets or changes the address later. Sign-in takes the address as it was registered: one that differs by more than letter case (an invisible or full-width character) is treated as unknown.

Then start the server, for example behind a reverse proxy on the same host that terminates TLS:

```bash
./nodevas serve -project ./workspace -port 5666 \
  -listen 127.0.0.1 -hostname nodevas.example.com -behind-proxy
```

Configure `trusted_proxy` to match the proxy addresses that are allowed to forward client information. Get it right: with the proxy's network missing, every user has the proxy's address and shares its rate-limit buckets and WebSocket allowance, and the hub takes the proxy's HTTPS requests for plain HTTP, so its cookies lose `Secure` and their `__Host-` names and it sends no HSTS header. The server warns once per peer (`event.code: http.untrusted_forwarder`) when a request carries `X-Forwarded-For` or `X-Forwarded-Proto` from a peer that is not a trusted proxy (that proxy's https does not make cookies `Secure` or turn on HSTS); behind Cloudflare Tunnel, that peer is the `cloudflared` container, so include its network.

Rate limits: requests without a signed-in identity share a per-address pre-auth bucket (40 a second, bursts of 80; every address of one IPv6 `/64` counts as one) and a hub-wide one (200 a second, bursts of 400). The sign-in door (`/api/auth/status`, the passcode request, sign-in and its authenticator step) this machine's health probe and device-link redeems (`POST /api/runner/link`) are charged to their own address only, so a flood from elsewhere cannot keep members from signing in; sign-in is also limited per address typed, per passcode and per account. Requests with a session cookie or a runner token skip the pre-auth buckets and are held to per-account limits by kind of route instead; a cookie or token that does not sign in is charged after it is refused. Step-ups and authenticator changes are limited to 10 per 5 minutes per account.

**Cookies.** Over HTTPS (TLS on the hub, or a trusted proxy saying `X-Forwarded-Proto: https`) the session, CSRF and sign-in cookies are `__Host-nodevas_session`, `__Host-nodevas_csrf` and `__Host-nodevas_signin` (`Secure`, `Path=/`, no `Domain`), so a sibling subdomain cannot plant one. The upgrade to this release signs every member of such a hub out once, and a sign-in waiting for its authenticator code starts again. A request with two session cookies gets `401`, and two sign-in cookies `401 SIGNIN_EXPIRED`.

**Step-up.** Inviting an account, changing its role or removing it, setting up two-factor sign-in, making a device link, adding a runner (**Settings → Agents → Add runner**, `POST /api/runners`, which otherwise answers `403 STEP_UP_REQUIRED`), and storing or clearing the runner access token first ask the signed-in person to confirm it is them (a mailed passcode, or their authenticator code once two-factor sign-in is on). Each new runner also mails its owner a "new device linked" notice, when the hub can send mail. A runner set to require it auto-accepts only runs whose dispatcher did so. A step-up lasts 10 minutes; `--step-up-minutes` (or `NODEVAS_STEP_UP_MINUTES`) sets 1 to 720.

For the container image, see [Getting started: Run the hub with Docker](./getting-started.md#run-the-hub-with-docker).

### Cloudflare Access

Behind Cloudflare Access, the hub can check Access's token on every request as well. Give it your team domain and the Access application's audience tag, in a file named with `-config` or `NODEVAS_CONFIG` (the workspace's own `nodevas.yaml` may not set them) or with `NODEVAS_SERVE_ACCESS_TEAM_DOMAIN`, `NODEVAS_SERVE_ACCESS_AUD` and `NODEVAS_SERVE_ACCESS_REQUIRED` (there are no flags):

```yaml
access:
  team_domain: myteam.cloudflareaccess.com
  aud: "<the application's AUD tag>"
  required: false
```

`serve` refuses one value without the other, or `required` without both. Until `required` is `true`, a request without a valid token is only logged (`auth.access.denied`, below), so you can check the setup first; then it gets `403`. Only `/api/healthz` from this machine, such as the container's health check, is not checked; every other request, `/api/metrics` included, needs the token. A loopback request that carries a forwarding header (`X-Forwarded-*`, `Forwarded`, `Via`, `X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`) comes through a proxy and is checked like any other. Access does not sign anyone in: people still sign in to Nodevas. With Access configured, `serve` never starts a runner of its own; link runners as on a team server ([Install a runner](./install.md)).

## Database

Accounts, sessions, the audit trail, settings, runs, the copilot's conversations and the search index live in PostgreSQL. Node bodies and the graph stay Markdown and YAML in the workspace.

| Setting | Meaning |
|---|---|
| `NODEVAS_DATABASE_URL` / `NODEVAS_DATABASE_URL_FILE` | An existing server (`postgres://user:password@host:5432/db`). Environment only, never `nodevas.yaml`: it carries a password. |
| `NODEVAS_DATABASE_WAIT` | How long an unreachable server is retried at start (default `30s`). Past it, `serve` exits 1; there is no fallback database. |
| `NODEVAS_REQUIRE_DATABASE_URL=1` | Refuse to start without a URL (set in the container image). |
| `NODEVAS_PG_HOME` | Where the embedded server keeps its binaries and data: `%LOCALAPPDATA%\nodevas\pg` on Windows, `~/Library/Application Support/nodevas/pg` on macOS, `$XDG_CONFIG_HOME/nodevas/pg` elsewhere. |
| `NODEVAS_PG_BINARIES` | Preinstalled binaries (the desktop app's bundle). Refused unless their stamp names the pinned version and archive hash. |
| `NODEVAS_PG_OFFLINE=1`, `NODEVAS_PG_MIRROR` | Never download; or download from another base URL (the hash is still enforced). |

**Embedded server (no URL).** `serve` starts the workspace's own PostgreSQL 17.11.1 before the hub and stops it on exit, Ctrl+C, a closed console or, with `--stop-on-stdin-close`, end of stdin (how the desktop app and `nodevas-dev` stop it on Windows). It listens on `127.0.0.1` only, on a free port, with a random password kept in owner-only files, and Nodevas logs in with SCRAM only, so a program that took the port first never sees the password; the app connects as a role that is not a superuser. The binaries are a pinned [theseus-rs/postgresql-binaries](https://github.com/theseus-rs/postgresql-binaries) build whose SHA-256 per platform is compiled into Nodevas; a first CLI start downloads and verifies them once (Linux about 40 MB, Windows 48 MB, macOS 90–93 MB; about 65–110 MB unpacked after unused parts are dropped). Offline, `nodevas db install-postgres --archive <file>` installs the archive fetched elsewhere, checked against the same hash. Each workspace has its own data directory, named by `<workspace>/.vised/database-id`, so moving the folder keeps its database. A first start takes a few seconds (initdb; longer on Windows while Defender scans the new files), later starts under a second; `db.embedded_ready` in the log has the measured `initdb_ms`, `start_ms` and `data_bytes`. If the server dies it is restarted on the same port (three times a minute at most, then `serve` exits). A crash leaves a stale `postmaster.pid`, which the next start recovers. A command such as `nodevas user add` run beside a running `serve` connects to its server. A copied workspace carries the original's id: run `nodevas db detach --project <copy>` to give it its own database. Linux builds need glibc 2.36 or newer; on older systems use `NODEVAS_DATABASE_URL`.

**Backups.** The database is not a file in the workspace, so copying the workspace folder does not back it up. Use `nodevas db dump --out nodevas.dump` (custom format) and `nodevas db restore --in nodevas.dump` (drops and recreates what the dump holds, in one transaction). Both run the pinned PostgreSQL 17.11.1 `pg_dump` and `pg_restore`, downloaded on first use like the embedded server, against whichever server the workspace uses, embedded or external. Those Linux builds need glibc, so in the container image use the PostgreSQL server's own `pg_dump`, as in the upgrade below. `nodevas db status` shows which server a workspace uses and its last schema step. Dumps you make yourself can leave out `search_documents` (rebuilt from the Markdown) and `legacy_imports`.

**Machine key.** Two-factor secrets (in the database) and the secrets saved in the app (copilot key, runner access token, git backup token, notification mail password; files beside the key) are encrypted with `nodevas/secrets/master.key` in your user config directory (`%AppData%` on Windows, `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` elsewhere; `/data/.config` in the container examples), created on first use, or with `NODEVAS_SECRET_KEY` (32 bytes, base64) when it is set, or with the key in the file `NODEVAS_SECRET_KEY_FILE` names (a Docker or Kubernetes secret). The two variables cannot both be set, and a named file that is missing, empty or not a 32-byte base64 key is an error the first time a secret is read or saved (copilot key, two-factor sign-in, runner access token, backup token); `serve` itself still starts, so check the key right after a change. In the container, where `master.key` would sit in the data volume beside the secrets it opens, prefer `NODEVAS_SECRET_KEY_FILE`; for an existing hub, copy the content of `master.key` into that secret, or the secrets saved so far and the accounts' two-factor secrets cannot be read. Move that `secrets` folder (or the key) with the database.

**Run history retention.** By default, every 6 hours the hub deletes raw history older than 180 days: the event log of each run that finished before then (what its agent said and did), agent session activity, auto-routing picks and the `audit_events` rows `auth.signin.failed`, `auth.stepup.failed`, `auth.totp.failed` and `runner.link.failed` (refused attempts), and it empties the diffs of older changes, keeping their file lists and line counts; the diff view then says history retention cleared the diff, apart from a diff too large to keep. Runs, with their results and usage, verdicts, evidence, scorecards and the rest of the audit trail stay. An administrator sets the period in **Settings → Agents → Advanced → Agent performance → Tiers & routing → Keep raw events for** (30 to 3650 days, or Forever); a pass that removes anything writes an `analytics.retention.prune` audit row, whose detail also counts the `refusals` it removed.

**Search index freshness.** A project's index is built in the background when the project is opened; only a search that arrives before any index exists waits for it. After that, searches read the last committed index and never wait on a reindex: a save, a card move or an outside edit is reindexed in the background (just the nodes it touched) and is searchable within about a tenth of a second, and a walk every quarter second of searching catches anything nobody reported. One search reads at most 64 MiB of document text per project: in a project past that, the largest documents may be missing from the results. At most 4 searches run at once on the hub; more wait their turn, and a search stops when its request ends.

**Upgrading from a SQLite release.** Earlier releases kept this data in `<workspace>/.vised/nodevas.db`. On the first start of this release, a workspace whose new database is empty imports that file in one transaction and renames it `nodevas.db.imported` (with its `-wal` and `-shm` files); it is never deleted. If the import fails, `serve` stops without changing anything: `nodevas db import-sqlite --report` compares the file with the database, and `NODEVAS_SKIP_SQLITE_IMPORT=1` starts without it (`nodevas db import-sqlite` imports it later). The importer is removed in the next release.

**Upgrading a server that already uses PostgreSQL.**

1. Back up first: `pg_dump -Fc nodevas > nodevas-before-upgrade.dump`.
2. Start the new release. Pending schema steps apply in place at start, each in one transaction under an advisory lock; they are matched by name and never renumbered. `nodevas db status` names the last one.
3. A leftover `<workspace>/.vised/nodevas.db` is not imported into a database that already holds data (the log says `db.legacy_sqlite_present`). It may hold rows an earlier release wrote while it fell back to SQLite: run `nodevas db import-sqlite --report` and keep the file until the report shows nothing missing.
4. A database that is down makes `serve` exit; a container restart policy starts it again once the database is back.

## Version history and backup

Each project keeps its history in its own git repository (`<project>/.vised/git`), and backups can push it to a git remote. `NODEVAS_HISTORY=files|git|auto` chooses the backend. See [History and backup](./history-and-backup.md).

## Health and metrics

`GET /api/healthz` (or `HEAD`) needs no sign-in and pings the database: `200 {"status":"ok","database":"ok"}`, or `503` with `"database":"down"` when it does not answer within two seconds. The container image's `HEALTHCHECK` asks it through `nodevas healthcheck`, so a hub that lost its database is reported unhealthy; its successful probes from the container itself are left out of the request log. It is rate-limited like any read.

`nodevas healthcheck [--project <dir>]` exits 0 when this machine's hub answers `/api/healthz` with a 2xx and 1 otherwise. It is a separate process, so it cannot see the flags `serve` was started with: it finds the port, listen address and scheme the way `serve` does from everything else (`NODEVAS_SERVE_PORT`, `NODEVAS_SERVE_LISTEN`, `NODEVAS_SERVE_TLS_CERT`, then `nodevas.yaml` or `NODEVAS_CONFIG`, then the defaults), asking a hub that listens on every interface on `127.0.0.1`. In the image, move the hub off port 5666 with `NODEVAS_SERVE_PORT` (or `port:` in `/data/nodevas.yaml`), not with `--port` on the command line, or pass the same `--port` to the probe.

`GET /api/metrics` is JSON for an administrator. On a hub without accounts (standalone), a scraper on the same machine needs nothing (a direct loopback request without forwarding headers). On a hub with accounts there is no such exception, since a proxy beside the hub looks just like this machine: a scraper needs an administrator's session, and Access's token when Access is on; without a session it gets `401`, a member `403`. The JSON: `db` has each connection pool's `sql.DBStats` (`write`, `read`, `search`: `max_open`, `open`, `in_use`, `idle`, `wait_count`, `wait_duration_ms`, ...), `limiter` the rate limiter's refusals by kind of route since start (`rejected_by_class`) and its concurrency slots, `runs` the number of runs in each state, `realtime.connections` the open WebSocket connections when the hub reports them, `audit_queue` the audit writes waiting for the database, and `goroutines`.

## Logs and audit events

`nodevas serve` writes one JSON object per line to stderr; `logging.format: text` prints the same fields for a terminal. Keys are flat dotted names. Every line has `@timestamp` (UTC, milliseconds), `log.level` (`debug`, `info`, `warn` or `error`), `message` and `service.name` (`nodevas`). A line written while serving a request also has `http.request.id`, the value the browser gets back as `X-Request-Id`. A standalone hub (loopback, no proxy) also writes its log, and any crash report, to `nodevas/logs/serve.log` in your user config directory, moved to `serve.log.1` at 10 MB; `serve` prints the path at start.

**Requests.** One line per request: `event.category: web`, `event.action: http.request`, `http.request.method`, `url.path` (never the query string), `http.response.status_code`, `event.duration_ms` (and ECS `event.duration`, in nanoseconds), `client.ip`, `user_agent.original` (at most 256 characters), `user.id` and `user.name` once the request is signed in, and `user.email` when it is signed in with a session. Successful `GET` and `HEAD` requests for `/assets/` and the container's health probe are left out; a write, every 5xx, and every `401` and `403` are always logged. Other 4xx responses (a `429` from the rate limiter above all, which a client can cause by the tens of thousands a second) are sampled: at most five lines a second, and one `warn` line with `event.action: http.request.suppressed` in the next second that has a request, counting what was left out (`nodevas.suppressed_count`, and per status in `nodevas.suppressed_statuses`, such as `429:9995,404:3`). The rate limiter's own `rate limit exceeded` lines have a cap of their own: 10 a second, bursts of 20, with no count of the rest. A read past its rate limit is not refused but waits its turn, so a person clicking fast in a few tabs is slowed for a moment rather than shown an error; only a read that would wait more than 10 seconds gets the `429`. Writes, exports, reports and other costly routes are still refused at once, except that the Agent performance reports wait up to 5 seconds for a free slot.

**Log size in Docker.** The default `json-file` log driver keeps every line forever. Cap it in the service's `logging:` block (the [Compose example](./getting-started.md#behind-a-reverse-proxy-with-docker-compose) does), or with `--log-opt max-size=10m --log-opt max-file=5` on `docker run`, or once for every container in `/etc/docker/daemon.json` (`"log-opts": {"max-size": "10m", "max-file": "5"}`).

**Audit events.** The row of each write a request makes is written by a background writer, in order, so the request does not wait for it; when more than 1,024 rows are waiting, the request writes its own after the ones ahead of it, and shutdown writes every queued row before closing the database (`audit_queue` in `/api/metrics`). Every row of the audit trail is also one line, written whether or not the row reached the database: `info` for a success, `warn` for a failure, and `error` for a failure that was the server's fault (`event.reason: error`, except a client that stopped waiting); keep `logging.level` at `info` or lower to see them all. The line has `event.category`, `event.action`, `event.outcome` (`success` or `failure`), `event.reason` when it failed, `user.id`, `user.name`, `user.email` when the account's address is known (on sign-in events, and on a write made with a session), and `client.ip`; the row itself rides along as `nodevas.audit.action`, `nodevas.audit.target`, `nodevas.project` and `nodevas.audit.detail` (the row's sanitised detail, as JSON text). A refused sign-in's row names nobody, as it always has, but its line names the account the attempt was for when it named a registered one, or else the address typed, and gives a finer `event.reason` than the row: the client gets the same answer for an unknown address and a wrong passcode, and the log tells them apart.

A passcode request's line is written once the mail relay has answered, so each request is one line: `success` when the passcode went out, and when the relay refused it or never answered, `failure` with `event.reason: error` and the relay's `error.message`. Its row is written at once and says `delivered: true` either way, as such rows always have: a passcode was minted and handed to delivery.

A refusal for being over a budget (`throttled`: a sign-in, a passcode request, a device-link redeem) is written at most once per action and source a minute, row and line; the next one written has `suppressed` in `nodevas.audit.detail`, the number left out since. A sign-in or passcode request for an address that differs from a registered one by more than letter case is `unknown-account`. An anonymous write on a public route is recorded with an empty actor, and agent heartbeats are not audited.

| Stored action | `event.category` | `event.action` | Notes |
|---|---|---|---|
| `auth.passcode.request` | `authentication` | `auth.code.request` | failure reasons: `throttled`, `unknown-account`, `cancelled`, `error` (no passcode could be made, or the relay did not take it) |
| `auth.signin` | `authentication` | `auth.signin` | `nodevas.auth.method`: `otp`, `totp` or `recovery-code` |
| `auth.signin.failed` | `authentication` | `auth.signin` | reasons, the ones every service on the host shares: `bad-credentials`, `unknown-account`, `expired-code`, `too-many-attempts`, `throttled`, `seat-limit`, `error`; a client that went away before the attempt was judged is `error`, though its row says `cancelled` |
| `auth.signin.pending` | `audit` | `auth.signin.pending` | passcode accepted, authenticator code still to come |
| `auth.signout` | `authentication` | `auth.signout` | |
| `auth.stepup.request` | `audit` | `auth.stepup.code.request` | `nodevas.auth.purpose: step-up`; written once the relay has answered, like a sign-in passcode's; failure reasons: `throttled`, `no-email`, `no-session`, `cancelled`, `error` |
| `auth.stepup`, `auth.stepup.failed` | `audit` | `auth.stepup` | `nodevas.auth.purpose: step-up`; the sign-in reasons, `expired-code` when the session ended first |
| `runner.link.create` | `audit` | `runner.link.create` | `nodevas.auth.method: device-link`; the link is handed back, not mailed |
| `runner.link.redeem`, `runner.link.failed` | `authentication` | `auth.signin` | `nodevas.auth.method: device-link` |
| `auth.totp.enable`, `auth.totp.disable`, `auth.totp.recovery.regenerate` | `iam` | `iam.account.update` | |
| `auth.totp.failed` | `iam` | `iam.account.update` | failure: a refused code while changing the second factor |
| `PUT projects/members`, `DELETE projects/members`, `POST projects/members/open` | `iam` | `iam.account.update` | project membership: `nodevas.project` is the project changed, `nodevas.audit.target` the member's account ID and `nodevas.audit.detail` the role given; opening a project drops every member and names no account |
| `account.invite`, `account.role`, `account.remove` | `iam` | `iam.account.update` | server accounts changed from *Settings → Account → Server accounts*: `nodevas.audit.target` is the account's ID and `nodevas.audit.detail` its name and role (`from` for a role change; `emailSent` and `delivery` — `sent`, `failed` or `unconfigured` — for an invitation); server-wide, never under a project |
| `runner.access-token.set`, `runner.access-token.clear` | `configuration` | `config.change` | |
| `PUT notify/settings`, `PUT copilot/settings`, `PUT remote/config`, `PUT runs/budget`, `PUT runs/routing`, `PUT usage/prices` | `configuration` | `config.change` | who wrote the settings, not what changed |
| `POST control/pause-all` | `audit` | `POST control/pause-all` | Station's *Pause everything*: one row for the whole act, under the project it stopped. `nodevas.audit.detail` has `cancelRunning`, the counts `autopilotsPaused`, `runsCancelled` and `skipped` (someone else's autopilots and runs, running runs left alone, anything that moved on meanwhile), and the IDs paused and cancelled (`autopilot:<id>`, `run:<id>`, up to 50 each) |
| anything else (`auth.totp.enroll`, `auth.totp.recovery.used`, `POST nodes`, `run.merge.approve`, `run.merge.merged`, `run.merge.failed`, `run.terminal.attach`, `analytics.retention.prune`, ...) | `audit` | the stored action | `failure` when the action ends in `.failed` |

**Reading the trail.** Server administrators read it in the app under *Settings → Account → Audit log* (`GET /api/audit`, admin-only; an agent gets `403` even when it acts for an administrator): recent events, server-wide or the active project's writes, filtered by kind (sign-in, step-up, two-factor, devices and runners, accounts and members, settings, other) and by text. The same page shows audit health (`GET /api/audit/health`): when a row could not reach the database, its line above (`event.code: audit.persistence_fallback`) is the only record of it, and health stays degraded until an administrator has found those lines and pressed *Acknowledge* (`POST /api/audit/health/acknowledge`), which is refused while database writes are still failing or when more fallbacks arrived since the page loaded. It also shows the rate limiter's refusals per kind of route since the server started (`GET /api/audit/limiter`).

A step-up, a signed-in session proving itself again, is neither a sign-in nor a sign-in passcode, so it reads as this server's own `audit` event, and a count of `auth.signin` or `auth.code.request` lines counts sign-ins alone. Sign-in and step-up lines also carry `nodevas.auth.purpose` (`sign-in` or `step-up`). `nodevas account totp-reset` runs on the server host and writes its `auth.totp.reset` row directly, without a line in the server's log.

**Logged, not audited.** `auth.session.expired`, when an expired session cookie is presented and refused. With Cloudflare Access configured, `auth.access.granted` (at most once per identity and client address every 30 minutes) and `auth.access.denied` (`warn`, with `event.reason` `missing-jwt`, `invalid-jwt`, `bad-audience` or `expired-jwt`, and `event.code: access.invalid`): one line for every refused request while `access.required` is on, and in audit mode, where nothing is refused, `nodevas.access.required: false` and at most one line per address and reason a minute. A panic while serving a request is one `error` line with `event.code: http.panic`, `error.message` and `error.stack_trace`; the request is named by its `http.request.id`, never dumped.

**Never logged.** Passcodes, authenticator and recovery codes, the session, CSRF and sign-in ticket cookies, Access tokens, device links, runner tokens, passwords, secrets and query strings. Addresses are logged: an account's own, or, when the sign-in form named no account, the address typed, lower-cased, once it parses as an address. What does not parse as one, such as a passcode typed into the wrong box, is not logged.

**`client.ip`** is the connection's peer address. Forwarding headers are believed only from a peer inside `trusted_proxy` (with `behind_proxy`), and `X-Forwarded-For` is then read from the right, up to the first address that is not a trusted proxy. A loopback peer that sends any forwarding header (`X-Forwarded-For`, `-Proto`, `-Host` or `-Port`, `Forwarded`, `Via`, `X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`, Access's header) is a proxy or tunnel on this machine, not this machine: it never skips Access or the metrics sign-in, and a hub without accounts answers it `401` when a header names another client or host, and always for `Via`. One with only `X-Forwarded-Proto` or `-Port`, or a proxy that adds no header at all (nginx with its default headers, ssh -L, socat), is served as this machine.
