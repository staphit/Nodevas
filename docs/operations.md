# Operations

Running a Nodevas server: configuration, letting other machines connect, and reading its logs. To install Nodevas, see [Getting started](./getting-started.md).

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

Keep the SMTP password out of YAML. Set `NODEVAS_SMTP_PASSWORD` through a secret store or the process environment. Containers can instead set `NODEVAS_SMTP_PASSWORD_FILE` to a mounted secret file. Do not set both variables. Deployment environments can use `NODEVAS_SERVE_*` variables to override YAML values.

## Allow network access

A server that listens beyond loopback, or sits behind a proxy, needs all of the following, and refuses to start without the first three (the host name only when it listens on every interface):

- **A host name.** Listening on every interface (`0.0.0.0`) needs `-hostname`, the name browsers use, to prevent DNS rebinding.
- **TLS.** Pass `-tls-cert` and `-tls-key`, or listen on loopback behind an HTTPS reverse proxy with `-behind-proxy`. `-allow-plaintext` is for controlled testing, or a private hop to a proxy, only.
- **An account.** Create one before the first start.
- **Outgoing mail.** Web sign-in is the account's email address plus a one-time passcode sent to it, so without `smtp` settings nobody can sign in.

Create the account with its sign-in address:

```bash
printf '%s' 'PASSWORD' | ./nodevas user add \
  --project ./workspace --user USER_NAME --role admin \
  --email you@example.com --password-stdin
```

Use a secure stdin mechanism and do not put a real password in shell history. `nodevas user email --user USER_NAME --email ...` sets or changes the address later.

Then start the server, for example behind a reverse proxy on the same host that terminates TLS:

```bash
./nodevas serve -project ./workspace -port 5666 \
  -listen 127.0.0.1 -hostname nodevas.example.com -behind-proxy
```

Configure `trusted_proxy` to match the proxy addresses that are allowed to forward client information.

For the container image, see [Getting started: Run the hub with Docker](./getting-started.md#run-the-hub-with-docker).

## Version history and backup

Each project keeps its history in its own git repository (`<project>/.vised/git`), and backups can push it to a git remote. `NODEVAS_HISTORY=files|git|auto` chooses the backend. See [History and backup](./history-and-backup.md).

## Logs and audit events

`nodevas serve` writes one JSON object per line to stderr; `logging.format: text` prints the same fields for a terminal. Keys are flat dotted names. Every line has `@timestamp` (UTC, milliseconds), `log.level` (`debug`, `info`, `warn` or `error`), `message` and `service.name` (`nodevas`). A line written while serving a request also has `http.request.id`, the value the browser gets back as `X-Request-Id`.

**Requests.** One line per request: `event.category: web`, `event.action: http.request`, `http.request.method`, `url.path` (never the query string), `http.response.status_code`, `event.duration_ms` (and ECS `event.duration`, in nanoseconds), `client.ip`, `user_agent.original` (at most 256 characters), `user.id` and `user.name` once the request is signed in, and `user.email` when it is signed in with a session. Successful `GET` and `HEAD` requests for `/assets/` and the container's health probe are left out; a write and every 4xx or 5xx response are always logged.

**Audit events.** Every row of the audit trail is also one line, written whether or not the row reached the database: `info` for a success, `warn` for a failure, and `error` for a failure that was the server's fault (`event.reason: error`, except a client that stopped waiting); keep `logging.level` at `info` or lower to see them all. The line has `event.category`, `event.action`, `event.outcome` (`success` or `failure`), `event.reason` when it failed, `user.id`, `user.name`, `user.email` when the account's address is known (on sign-in events, and on a write made with a session), and `client.ip`; the row itself rides along as `nodevas.audit.action`, `nodevas.audit.target`, `nodevas.project` and `nodevas.audit.detail` (the row's sanitised detail, as JSON text). A refused sign-in's row names nobody, as it always has, but its line names the account the attempt was for when it named a registered one, or else the address typed, and gives a finer `event.reason` than the row: the client gets the same answer for an unknown address and a wrong passcode, and the log tells them apart.

A passcode request's line is written once the mail relay has answered, so each request is one line: `success` when the passcode went out, and when the relay refused it or never answered, `failure` with `event.reason: error` and the relay's `error.message`. Its row is written at once and says `delivered: true` either way, as such rows always have: a passcode was minted and handed to delivery.

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
| `account.invite`, `account.role`, `account.remove` | `iam` | `iam.account.update` | server accounts changed from *Settings → Server accounts*: `nodevas.audit.target` is the account's ID and `nodevas.audit.detail` its name and role (`from` for a role change; `emailSent` and `delivery` — `sent`, `failed` or `unconfigured` — for an invitation); server-wide, never under a project |
| `runner.access-token.set`, `runner.access-token.clear` | `configuration` | `config.change` | |
| `PUT notify/settings`, `PUT copilot/settings`, `PUT remote/config`, `PUT runs/budget`, `PUT runs/routing`, `PUT usage/prices` | `configuration` | `config.change` | who wrote the settings, not what changed |
| `POST control/pause-all` | `audit` | `POST control/pause-all` | Station's *Pause everything*: one row for the whole act, under the project it stopped. `nodevas.audit.detail` has `cancelRunning`, the counts `autopilotsPaused`, `runsCancelled` and `skipped` (someone else's autopilots and runs, running runs left alone, anything that moved on meanwhile), and the IDs paused and cancelled (`autopilot:<id>`, `run:<id>`, up to 50 each) |
| anything else (`auth.totp.enroll`, `auth.totp.recovery.used`, `POST nodes`, `run.merge.done`, ...) | `audit` | the stored action | `failure` when the action ends in `.failed` |

**Reading the trail.** Server administrators read it in the app under *Settings → Audit log* (`GET /api/audit`, admin-only): recent events, server-wide or the active project's writes, filtered by kind (sign-in, step-up, two-factor, devices and runners, accounts and members, settings, other) and by text. The same page shows audit health (`GET /api/audit/health`): when a row could not reach the database, its line above (`event.code: audit.persistence_fallback`) is the only record of it, and health stays degraded until an administrator has found those lines and pressed *Acknowledge* (`POST /api/audit/health/acknowledge`), which is refused while database writes are still failing or when more fallbacks arrived since the page loaded. It also shows the rate limiter's refusals per kind of route since the server started (`GET /api/audit/limiter`).

A step-up, a signed-in session proving itself again, is neither a sign-in nor a sign-in passcode, so it reads as this server's own `audit` event, and a count of `auth.signin` or `auth.code.request` lines counts sign-ins alone. Sign-in and step-up lines also carry `nodevas.auth.purpose` (`sign-in` or `step-up`). `nodevas account totp-reset` runs on the server host and writes its `auth.totp.reset` row directly, without a line in the server's log.

**Logged, not audited.** `auth.session.expired`, when an expired session cookie is presented and refused. With Cloudflare Access configured, `auth.access.granted` (at most once per identity and client address every 30 minutes) and `auth.access.denied` (`warn`, with `event.reason` `missing-jwt`, `invalid-jwt`, `bad-audience` or `expired-jwt`, and `event.code: access.invalid`): one line for every refused request while `access.required` is on, and in audit mode, where nothing is refused, `nodevas.access.required: false` and at most one line per address and reason a minute. A panic while serving a request is one `error` line with `event.code: http.panic`, `error.message` and `error.stack_trace`; the request is named by its `http.request.id`, never dumped.

**Never logged.** Passcodes, authenticator and recovery codes, the session, CSRF and sign-in ticket cookies, Access tokens, device links, runner tokens, passwords, secrets and query strings. Addresses are logged: an account's own, or, when the sign-in form named no account, the address typed, lower-cased, once it parses as an address. What does not parse as one, such as a passcode typed into the wrong box, is not logged.

**`client.ip`** is the connection's peer address. Forwarding headers are believed only from a peer inside `trusted_proxy` (with `behind_proxy`), and `X-Forwarded-For` is then read from the right, up to the first address that is not a trusted proxy.
