# Voice Copilot

A real-time AI copilot inside the Nodevas UI, driven by voice or text. It reads
and edits the graph through the same tools the MCP integration exposes, asks
before changing things according to a permission mode, and keeps a transcript
of what the person said and what the AI answered and did.

## Decisions

| Topic | Decision |
| --- | --- |
| Speech to text | Browser Web Speech API (`SpeechRecognition` / `webkitSpeechRecognition`). Chrome and Edge; elsewhere the panel falls back to typing. |
| Text to speech | `speechSynthesis`, off by default, toggle in the panel. |
| Model | Pluggable provider: `anthropic` (official Go SDK) or `openai` (any OpenAI-compatible Chat Completions endpoint: OpenAI, Ollama, LM Studio, vLLM, OpenRouter). |
| Tools | The existing MCP tools, run in-process (see Tool bridge) plus browser-side UI tools. No tool is implemented twice. |
| Permissions | Four modes modelled on Claude Code: `plan`, `default`, `acceptEdits`, `bypassPermissions`. |
| Transcript | Persisted per account and project in the workspace database; shown as a chat log in the panel. |

## Architecture

```
browser                                         nodevas serve
───────                                         ─────────────
mic ─▶ SpeechRecognition ─▶ text ─┐
keyboard ─────────────────────────┤
                                  ▼
                 POST /api/copilot/conversations/:id/turns   (streamed NDJSON)
                                  │                    ┌─────────────────────────┐
                                  └──────────────────▶ │ copilot.Service.Turn    │
                                                       │  provider.Stream  ◀──▶ LLM
 panel ◀── text deltas / tool events / approvals ◀──── │  permission gate        │
   │                                                   │  toolbridge.Call        │
   ├─ POST …/turns/:turn/decisions  (approve / deny) ─▶│   └▶ in-process MCP     │
   └─ POST …/turns/:turn/client-results (UI tools)  ─▶ │       └▶ s.Handler()    │
                                                       └───────────┬─────────────┘
 canvas ◀──────────── existing WebSocket broadcast ◀───────────────┘
```

### Tool bridge (`internal/copilot/toolbridge`)

`toolbridge.Open` builds the MCP server from `internal/mcp` (`NewServer`) with a
`mcp.Client` whose `http.Client` uses an in-process `RoundTripper`: each request
is served by the server's own `http.Handler` instead of a socket. The round
tripper

- copies the person's `Cookie` and `Host` from the copilot request, so the call
  authenticates as that person and passes the Host allow-list;
- marks the request context with `auth.WithInProcessAgent(ctx, class)`. A Go
  context value cannot arrive from the network, so this is the one trustworthy
  way to say "this request is the copilot". `withAuth` sees it, authenticates
  the cookie as usual, sets `actor.Agent = class` (default `orchestrator`, so
  node `write_access` applies to the copilot as to any agent) and skips the
  CSRF double-submit check (there is no browser in the loop to forge).

The bridge connects to that MCP server over `mcp.NewInMemoryTransports()`,
lists the tools and maps them to `provider.Tool` plus a permission `Kind`:

| Kind | Tools |
| --- | --- |
| `read` | every tool with `ReadOnlyHint` |
| `write` | `create_node`, `link_nodes`, `update_node_meta`, `set_node_status`, `claim_task`, `release_task`, `index_documents` |
| `risky` | `update_node_body` (replaces a page wholesale) |

Unknown non-read-only tools default to `risky`.

### Client tools (browser)

UI actions the model may take, executed in the browser and answered through
`client-results`. All are `read` kind (they change the view, not the data):

| Tool | Input | Effect |
| --- | --- | --- |
| `ui_open_node` | `{id}` | select the node and open its drawer |
| `ui_focus_nodes` | `{ids[]}` | select the nodes and fit the canvas to them |
| `ui_set_view` | `{view: "graph" \| "board" \| "timeline"}` | switch the main view |
| `ui_set_filter` | `{status?, assignee?, label?, priority?, text?}` | apply a filter; empty clears it |
| `ui_switch_project` | `{name}` | switch the browser's project (the copilot conversation ends) |

The browser declares which of these it supports in the turn request, so a
server never offers a tool the client cannot run.

### Dispatch tools (W7, `internal/copilot/servertools.go`)

The voice entry point of the autopilot: the person dictates a requirement, the
copilot confirms it, files it and has it broken down, then starts the
orchestrator once the person agrees with the tasks.

| Tool | Input | Effect |
| --- | --- | --- |
| `start_breakdown` | `{requirement, title?, goalNodeId?, runnerId?, agent?, model?, repoId?}` | creates the goal node (unless `goalNodeId` names one) and dispatches a plan run (`POST /api/runs`, `kind: "plan"`) on the speaker's online runner: first runner of theirs with the repo, agent order claude, codex, opencode, gemini, antigravity, the agent's default model |
| `start_autopilot` | `{goalNodeId, maxParallel?, reviewMode?, reviewers?, quorum?, anyEditor?, autoMerge?, implement?, advisor?, runnerId?, repoId?}` | `POST /api/autopilot`; defaults: 2 tasks at a time, 2 review rounds, a person approves each PR, no auto-merge. Who implements: the `implement` chain when given, else `advisor`; with neither, the advisor the speaker's runner offers (System One, then its fast model), else the routing rules, whose last resort is the first installed agent's default model (often the most expensive). The result's `implementedBy` says which, and the prompt has the copilot tell the person. |

They are copilot-only: not MCP tools, so no agent (and nothing through the
runner proxy) can call them, and they exist only on a server that gives the
copilot its router. Because only a person may dispatch runs, they call the API
as the person rather than as an in-process agent, and so they **always ask**,
in every mode that runs tools, `bypassPermissions` included (`plan` refuses
them). The runner still applies its own acceptance rules (auto-accept, a
person at its terminal) to what they dispatch. The system prompt walks the
model through confirm → `start_breakdown` → summarize the tasks → ask →
`start_autopilot`.

### Permission modes

| Mode | `read` | `write` | `risky` |
| --- | --- | --- | --- |
| `plan` | run | refused, the model is told to propose instead | refused |
| `default` | run | ask | ask |
| `acceptEdits` | run | run | ask |
| `bypassPermissions` | run | run | run |

- "Ask" emits a `permission_request` event and parks the turn until the browser
  posts a decision or 120 s pass (timeout = deny). Decisions: `allow`,
  `allow_session` (this tool, no more asking for the rest of the conversation),
  `deny` (optional message to the model).
- `bypassPermissions` is available only when the server config allows it
  (`copilot.allow_bypass`, default true on a loopback-only server, false with
  accounts).
- The mode is per conversation, chosen in the panel (Shift+Tab cycles, as in
  Claude Code), and sent with each turn.
- Voice can answer a permission request: "確認 / 允許 / 好 / yes / allow" and
  "取消 / 拒絕 / 不要 / no / deny".

## HTTP API (`internal/httpapi/copilot`)

All under `/api`, authenticated like every other route, JSON in.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/copilot/status` | `{enabled, provider, model, modes[], allowBypass, reason?}` |
| `GET` | `/copilot/conversations?project=` | the caller's conversations, newest first |
| `POST` | `/copilot/conversations` | `{project, mode}` → conversation |
| `GET` | `/copilot/conversations/:id` | conversation + transcript entries |
| `DELETE` | `/copilot/conversations/:id` | delete (own only) |
| `PATCH` | `/copilot/conversations/:id` | `{mode?, title?}` |
| `POST` | `/copilot/conversations/:id/turns` | start a turn, streamed response (below) |
| `POST` | `/copilot/conversations/:id/turns/:turn/decisions` | `{requestId, decision, message?}` |
| `POST` | `/copilot/conversations/:id/turns/:turn/client-results` | `{callId, output, isError}` |
| `POST` | `/copilot/conversations/:id/turns/:turn/cancel` | stop the turn |
| `GET`/`PUT` | `/copilot/settings` | admin: provider kind, model, base URL, effort, API key (write-only), allowBypass, keySource (`runner` \| `server` \| `auto`) |

A conversation, transcript entry and turn belong to the account that created
them; every other account gets 404.

### Turn request

```json
{
  "text": "把登入相關的任務都標成進行中",
  "source": "voice",
  "mode": "default",
  "context": {
    "selectedNodeIds": ["n12"],
    "view": "graph",
    "language": "zh-TW"
  },
  "clientTools": ["ui_open_node", "ui_focus_nodes", "ui_set_view", "ui_set_filter"]
}
```

### Turn stream

`Content-Type: application/x-ndjson`, one JSON event per line, flushed as it
happens. `fetch` + `ReadableStream` on the client (EventSource cannot POST or
send the CSRF header).

| `type` | Fields | Meaning |
| --- | --- | --- |
| `turn_started` | `turnId`, `entryId` | the user's entry is stored |
| `text_delta` | `text` | assistant text as it streams |
| `tool_call` | `callId`, `name`, `title`, `kind`, `input` | the model wants a tool |
| `permission_request` | `requestId`, `callId`, `name`, `kind`, `input`, `summary` | waiting for a decision |
| `permission_resolved` | `requestId`, `decision` | decision applied |
| `client_tool_call` | `callId`, `name`, `input` | the browser must run a UI tool |
| `tool_result` | `callId`, `output` (truncated to 2 KB), `isError` | tool finished |
| `entry` | `entry` | a transcript entry was stored (user, assistant, tool) |
| `error` | `message`, `code` | the turn failed |
| `turn_finished` | `stop`, `usage` | done |

## Storage

Migration `copilot` in `internal/db` (SQLite and PostgreSQL):

```sql
CREATE TABLE copilot_conversations (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL,
    project     TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    mode        TEXT NOT NULL DEFAULT 'default',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX copilot_conversations_owner ON copilot_conversations (account_id, project, updated_at);

CREATE TABLE copilot_entries (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL REFERENCES copilot_conversations(id) ON DELETE CASCADE,
    seq              INTEGER NOT NULL,
    kind             TEXT NOT NULL,      -- user | assistant | tool | notice
    source           TEXT NOT NULL DEFAULT '', -- voice | text (user entries)
    text             TEXT NOT NULL DEFAULT '',
    tool_name        TEXT NOT NULL DEFAULT '',
    tool_input       TEXT NOT NULL DEFAULT '',
    tool_output      TEXT NOT NULL DEFAULT '',
    tool_status      TEXT NOT NULL DEFAULT '', -- ok | error | denied
    message          TEXT NOT NULL DEFAULT '', -- provider.Message JSON, for replay
    created_at       INTEGER NOT NULL,
    UNIQUE (conversation_id, seq)
);
```

`message` holds the provider-neutral `provider.Message` (with the vendor's raw
content) that the next turn replays. The display columns are what the panel
reads, so the transcript renders without decoding provider payloads.

The provider API key is stored with `internal/secrets` (AES-GCM, machine key),
never in the database or `nodevas.yaml`. Environment overrides:
`NODEVAS_COPILOT_PROVIDER`, `NODEVAS_COPILOT_MODEL`, `NODEVAS_COPILOT_BASE_URL`,
`NODEVAS_COPILOT_API_KEY` (falls back to `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`).

## Key on the runner (`keySource`)

The server need not hold a model key at all. With `keySource: runner` (and with
`auto`, the default, whenever the server has no key), the turn loop, the
permission gate and the tools stay on the server, but each **model call** goes
to the speaker's own runner over its existing socket
(`internal/runnerwire/llm.go`): `llm_request` carries the history and tool
list, the runner calls the provider with a key that only that PC holds, and
streams `llm_delta` fragments and one `llm_done` back. The server relays them
into the turn (`internal/runners/llm.go`, `internal/copilot/remote.go`).

On the PC:

```sh
nodevas runner set copilotProvider anthropic   # or openai
nodevas runner set copilotModel claude-opus-5-5 # optional; the provider's default otherwise
nodevas runner copilot-key                      # the key on stdin, never a flag
nodevas runner                                  # restart; the hello now offers "llm"
```

`NODEVAS_COPILOT_API_KEY` in the runner's environment works instead of a
stored key. `copilotBaseURL` points at an OpenAI-compatible server (Ollama,
LM Studio: no key needed) or a proxy; it must be https, or http to the machine
itself or the local network.

What this guarantees, and what it does not:

- The key never reaches the server: the hello names the provider and model,
  never the key, and nothing on the wire carries one.
- The runner decides the provider, base URL and model. A request that names
  others is refused, so a server cannot point the key at another endpoint.
- A call goes only to the speaker's own runner (the server picks among that
  account's online runners, the most recently connected), and the runner
  refuses a call for any account but its owner's (`Welcome.OwnerID`).
- A server that is broken into can still make the runner spend its owner's
  quota. The runner bounds that: two calls at once, 40 a minute, 64 000
  output tokens per call, ten minutes per call, and a log line for each.
- When the runner is offline, the copilot button stays and explains how to
  start it (`status.needsRunner`); `runners-changed` refreshes the status.

`keySource: server` keeps the old behaviour (the server's stored or
environment key). The settings tab warns when the server holds a key.

### The autopilot's advisor model

The same relay carries a second kind of model call (`purpose: "advisor"`): the
W7 autopilot asking a fast model which agent and model should implement each
task (`AdvisorLLM`, `internal/autopilot/llmadvisor.go`). It has its own
settings on the runner, so a cheap, quick model can pick while the copilot
talks with a stronger one:

```sh
nodevas runner set advisorProvider openai        # or anthropic
nodevas runner set advisorModel <fast-model-id>
nodevas runner set advisorBaseURL https://…/v1   # optional: any OpenAI-compatible endpoint
nodevas runner advisor-key                       # stdin; or NODEVAS_ADVISOR_API_KEY
```

With the copilot's provider, the copilot key serves both. The hello reports it
as `Info.Advisor` with capability `llm-advisor`; a request for one purpose is
never served with the other's settings, and the same refusal rules and limits
apply. The advisor answers JSON choosing only among the agents and models the
owner's runners report; anything else (an invented model, a malformed answer,
more than 6 s, an offline runner) is an error, and the autopilot falls back to
its rules.

#### A System One advisor (Laya or Jev)

`advisorProvider systemone` points the advisor at a System One endpoint
instead: a model that answers typed questions (pick one option, score on a
scale, yes or no) with calibrated probabilities in a fraction of a second, and
generates no text. Two speak the same protocol (`POST /v1/systemone`):

- **Laya**, open source (Apache-2.0), on your PC. `pip install "laya[serve]"`,
  then `laya-serve` (listens on `0.0.0.0:8000`; set `LAYA_HOST=127.0.0.1` to
  keep it on this machine). The first call downloads its checkpoint (the three
  together are about 2.3 GB). No key unless you set `LAYA_API_KEY`.

  ```sh
  nodevas runner set advisorProvider systemone   # advisorBaseURL defaults to http://127.0.0.1:8000
  nodevas runner advisor-key                      # only if laya-serve has LAYA_API_KEY
  ```

- **Jev**, TypeSafe's hosted model (early access; key from its console).

  ```sh
  nodevas runner set advisorProvider systemone
  nodevas runner set advisorBaseURL https://api.typesafe.ai
  nodevas runner set advisorModel jev-latest
  nodevas runner advisor-key                      # stdin; sent as a bearer token
  ```

Then pick advisor **systemone** in the autopilot's policy. The hub sends a
`decide_request` (`internal/runnerwire/decide.go`) to the owner's runner with
capability `decide`, never to anyone else's; the runner checks it against its
settings and asks its endpoint, the key never leaving the PC. The question is
a choice among the installed agent/model pairs (options `o1…`, each described
by agent and model), plus the task's complexity on a five-level score for the
reason. The chain is the options by probability, at most three, a fallback
only above 5 %. An answer naming no option given, an endpoint error or more
than 10 s means the rules decide instead (`internal/autopilot/systemone.go`).

When the choice itself is unsure (confidence below 0.35), the complexity score
picks a tier instead: trivial and small tasks go to the fast tier (Haiku, free
and flash models), medium to the balanced one (Sonnet), large and risky to the
strong one (Opus, Fable, pro and high models), each tier in the order the
choice ranked its options, with one fallback from the next tier. Models whose
name does not tell their tier are left out of this path.

Measured against a real local Laya 0.4.14 on this project's Windows PC (CPU
build of PyTorch, 2026-10-24):

- **Latency** 4–6 s a decision on CPU, within the 10 s limit but far from the
  0.1–0.5 s System One promises; a CUDA build of PyTorch on the GPU is what
  gets there.
- **The implementer choice is spread evenly** (confidence 0.01–0.15 across
  checkpoints) and once picked Haiku for a 30-file OAuth migration, which is
  why the tier path above exists.
- **The complexity score separates tasks**: 1.19–1.59 for one-line file
  changes, 2.47–2.81 for the migration. A real autopilot with Laya deciding
  sent a one-line task to a free flash model and a README-and-file task to
  Sonnet.

Jev is the hosted model the protocol was made for; a confident Jev choice is
taken as it is.

## Configuration (`nodevas.yaml`)

```yaml
copilot:
  enabled: true
  provider: anthropic        # anthropic | openai
  model: claude-opus-5-5
  base_url: ""               # required for openai-compatible servers other than OpenAI
  effort: low                # voice favours latency
  allow_bypass: false
  max_tool_calls_per_turn: 25
  agent_role: orchestrator
```

## Web UI (`web/src/copilot`)

- Floating microphone button (bottom right), `Ctrl+Shift+Space` toggles the
  panel, hold `Alt+Space` to talk (push-to-talk).
- Panel: conversation list, transcript (user bubbles marked 🎤 when spoken,
  assistant markdown, collapsible tool cards with input/output and status),
  live interim transcript while listening, text input fallback, mode selector,
  speak-replies toggle, stop button.
- Permission card inline in the transcript: Allow / Allow for this conversation
  / Deny, keyboard `1`/`2`/`3`, or voice.
- Strings in `web/src/i18n.ts` (`copilot.*`, en and zh-TW).
- Speech recognition language follows the UI language (`zh-TW`, `en-US`).

## Out of scope for this release

Server-side STT, wake word, streaming tool-input display, multi-project turns.
