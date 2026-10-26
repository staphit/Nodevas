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
| Model | Pluggable provider: `anthropic` (official Go SDK), `openai` (any OpenAI-compatible Chat Completions endpoint: OpenAI, Ollama, LM Studio, vLLM, OpenRouter), or `auto`, the default: a free model with no key (the browser's built-in model, or a local Ollama / LM Studio), see "Which model answers". |
| Tools | The existing MCP tools, run in-process (see Tool bridge) plus browser-side UI tools. No tool is implemented twice. |
| Permissions | Four modes modelled on Claude Code: `plan`, `default`, `acceptEdits`, `bypassPermissions`. |
| Transcript | Persisted per account and project in the workspace database; shown as a chat log in the panel. |

## Which model answers

Explicit configuration always wins. With none of it, the copilot works out of
the box with a free model and no key.

| Source | Provider | Where the model runs | Key |
| --- | --- | --- | --- |
| Administrator chose `anthropic` / `openai` (settings, `nodevas.yaml`, `NODEVAS_COPILOT_PROVIDER`) | that one | server, or the runner with `keySource: runner` | server or runner key |
| A key is configured (stored, `NODEVAS_COPILOT_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`) with `provider: auto` | the key's vendor (Anthropic for a key that does not say) | server | that key |
| `copilotProvider` set on the person's runner | the runner's | runner | the runner's |
| Automatic, 1: the browser's built-in model (Chrome's Prompt API, Gemini Nano), when the speaker's browser reports it `available` | `browser` / `gemini-nano` | the speaker's browser | none |
| Automatic, 2: Ollama (`127.0.0.1:11434`) or LM Studio (`127.0.0.1:1234`) found on the speaker's runner, else on the server | `openai` at the loopback URL | runner, else server | none |
| Automatic, 3: the browser's model reports `downloadable` / `downloading` | `browser` | the browser, after the person clicks "Download the on-device model" | none |
| Nothing | — | — | the panel says what to do: Chrome 148+ on a desktop, or install Ollama and `ollama pull qwen3:8b`, or set a key |

`provider: auto` is the default (`internal/copilot/choose.go`). `keySource:
runner` limits the automatic choice to a runner (its configured provider or
the local model it found); `keySource: server` never asks a runner. The order
puts a ready local model before a browser model that still has to download.

### Local model servers (`internal/copilot/localllm`)

- Probes only the fixed loopback candidates (`127.0.0.1:11434`,
  `127.0.0.1:1234`); a candidate whose host is not loopback is skipped, and a
  found base URL that is not loopback is ignored. No proxy, no redirects,
  700 ms per probe, all candidates at once.
- Lists models with Ollama's `/api/tags`, else `/v1/models`. Picks a model
  known to call tools (qwen3, qwen2.5, llama3.1+, mistral-nemo, gpt-oss, …),
  else the first that is not an embedding model.
- Caches the answer: a found server for 30 s (a newly pulled model shows up),
  "nothing" for 10 s (starting Ollama is noticed without a restart).
- Runs where the model call runs. On a runner, with no `copilotProvider`, the
  runner offers what it finds in its hello (`LLMInfo.auto`, `local`), keyless:
  a copilot key on that PC is never sent to it. `nodevas runner set
  copilotProvider off` turns this off. The server probes its own loopback,
  which on a NAS is the NAS: the panel says which machine the model is on
  ("on this PC", "on the server", "on <runner>").

### The browser's built-in model

The turn loop, the tools and the permission gate stay on the server; only the
model call runs in the browser of the person who started the turn
(`internal/copilot/browser.go`, `web/src/copilot/browserModel.ts`).

- **Relay.** The server emits `model_request` on that turn's own NDJSON
  stream (`requestId`, `modelRequest: {system, messages[], schema, attempt}`);
  the browser runs it and posts `POST …/turns/:turn/model-results`
  `{requestId, output}` or `{requestId, error}`. An answer is accepted only
  for a request still waiting, on a turn of the same account and
  conversation, from the same sign-in session (a hash of its session cookie),
  with the random request id that only that stream carried; everything else
  is 404. No answer within 120 s ends the turn with an error; a stopped turn
  aborts the browser's call.
- **Tool calls.** The Prompt API has none. The browser prompts with
  `responseConstraint` set to the server's JSON Schema, so the model answers
  one object: `{"type":"text","text":…}` or `{"type":"tool_call","name":…,
  "input":{…}}` with `name` limited to the offered tools. The server parses it
  like any provider's output: an unknown tool, a non-object input or broken
  JSON is asked for once more (the browser also retries once locally), plain
  prose is taken as the reply, a second failure ends the turn. A valid call
  then goes through the permission gate like any other.
- **Small context.** A short system prompt and one line per tool; the newest
  16 messages, each cut to 2 KB, tool results to 1.5 KB. The browser drops the
  oldest further until the input fits its measured quota.
- **Download.** Creating a session may download the model; the Prompt API
  allows that only from a user gesture, so the panel shows a "Download the
  on-device model" button with progress (`downloadprogress`).
- **No bypass.** Node content can carry prompt injection and a small model is
  the easiest to mislead, so `bypassPermissions` is not offered with the
  browser's model and a turn that asks for it is refused (400) with the reason.
  Default still asks before every write.
- Chrome 148+ on desktop only; Edge, Firefox, Safari and mobile browsers
  report no model and fall through to the next choice.

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
model through confirm → `start_breakdown` → (follow-up, below) → ask →
`start_autopilot`.

### Follow-up when the plan run ends (`internal/copilot/followup.go`)

The copilot comes back to the conversation by itself; the person need not ask.

1. `start_breakdown` succeeds → the turn records a follow-up
   `{conversation, account, goalNodeId, runId, language}` in
   `copilot_followups` (persisted: a hub restart while the plan run goes on
   loses nothing; `StartFollowups` delivers whatever came due at startup).
   The tool's result tells the model to say it will come back.
2. The run dispatcher's observer (`runs.Dispatcher.Observe`, wired in
   `cmd/nodevas/copilot_followups.go`) calls `Service.RunChanged` for every
   run change; only plan runs that reached a final state (succeeded, failed,
   cancelled, declined) matter. A failed run that falls back is not reported:
   the follow-up moves to the fallback run (`RetryOf`).
3. Delivery takes the conversation's turn lock. A turn in progress keeps it;
   the follow-up is written right after that turn ends, never between its
   entries. It is written only when the run was dispatched by the
   conversation's owner, on the recorded goal, in the conversation's project;
   anything else is dropped.
4. Two entries, no model call and no tool (so a follow-up cannot write or
   dispatch, and it works with the browser's model while the browser is
   closed):
   - `event`: a user-role note for the model, from the server, never the
     person: `[Server event, not the person: the plan run … for the goal node
     … succeeded.]`, the goal's tasks in dependency order with ids and
     dependencies (read server-side with `autopilot.GoalMembers`; titles
     marked as data), and what to do on a yes. The panel shows its short text
     ("Breakdown finished: 3 tasks") as a notice.
   - `assistant`: a fixed template in the panel's language (en, zh-TW): the
     task titles with "(after 1, 2)", then "Shall I start the autopilot? By
     default it works on 2 tasks at a time, reviews each pull request for up
     to 2 rounds, and you approve every pull request before it merges." A
     failed, cancelled or declined run says so (with the error) and offers to
     break the goal down again.
5. The account's browsers get the realtime event `copilot-followup` (id: the
   conversation), to that account only (`Hub.BroadcastActor`).

The person's next turn replays both entries. The system prompt says the note
is not the person's go-ahead; on a yes the model calls `start_autopilot`,
which still asks for permission like every dispatch.

In the panel: with that conversation open, the follow-up is fetched and shown
at once (after a running turn, if any) and spoken when spoken replies are on.
Otherwise the conversation is listed as `unread` (● in the picker, a dot on the
mic button, a system notification when the browser allows one, reusing the
agent-ask plumbing); the mic button then opens that conversation. Opening it
(`GET /copilot/conversations/:id`) marks it read.

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
  accounts), and never with the browser's built-in model.
- The mode is per conversation, chosen in the panel (Shift+Tab cycles, as in
  Claude Code), and sent with each turn.
- Voice can answer a permission request: "確認 / 允許 / 好 / yes / allow" and
  "取消 / 拒絕 / 不要 / no / deny".

## HTTP API (`internal/httpapi/copilot`)

All under `/api`, authenticated like every other route, JSON in.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/copilot/status?browserModel=` | `{enabled, provider, model, modes[], allowBypass, reason?, keySource, modelHost, automatic?, localServer?, serverIsLocal?, runner?, needsRunner?, needsDownload?, needsSetup?, reasonCode?}`; `browserModel` is the browser's Prompt API availability |
| `GET` | `/copilot/conversations?project=` | the caller's conversations, newest first; `unread: true` on one with a follow-up not yet opened |
| `POST` | `/copilot/conversations` | `{project, mode}` → conversation |
| `GET` | `/copilot/conversations/:id` | conversation + transcript entries; marks its follow-ups read |
| `DELETE` | `/copilot/conversations/:id` | delete (own only) |
| `PATCH` | `/copilot/conversations/:id` | `{mode?, title?}` |
| `POST` | `/copilot/conversations/:id/turns` | start a turn, streamed response (below) |
| `POST` | `/copilot/conversations/:id/turns/:turn/decisions` | `{requestId, decision, message?}` |
| `POST` | `/copilot/conversations/:id/turns/:turn/client-results` | `{callId, output, isError}` |
| `POST` | `/copilot/conversations/:id/turns/:turn/model-results` | `{requestId, output}` or `{requestId, error}`: the browser model's answer (same session only) |
| `POST` | `/copilot/conversations/:id/turns/:turn/cancel` | stop the turn |
| `GET`/`PUT` | `/copilot/settings` | admin: provider (`auto` \| `anthropic` \| `openai`), model, base URL, effort, API key (write-only), allowBypass, keySource (`runner` \| `server` \| `auto`); the view adds `automatic` |

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
  "clientTools": ["ui_open_node", "ui_focus_nodes", "ui_set_view", "ui_set_filter"],
  "browserModel": "available"
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
| `model_request` | `requestId`, `modelRequest` | the browser's built-in model must answer (see above) |

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
    kind             TEXT NOT NULL,      -- user | assistant | tool | notice | event
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

Migration `copilot_followups` (0022) adds the plan runs a conversation waits on:

```sql
CREATE TABLE copilot_followups (
    id               TEXT PRIMARY KEY,
    conversation_id  TEXT NOT NULL REFERENCES copilot_conversations(id) ON DELETE CASCADE,
    account_id       TEXT NOT NULL,
    goal_node_id     TEXT NOT NULL,
    run_id           TEXT NOT NULL,
    language         TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending', -- pending | delivered | dropped
    seen             INTEGER NOT NULL DEFAULT 0,       -- 1 once the conversation was opened
    created_at       INTEGER NOT NULL,
    delivered_at     INTEGER NOT NULL DEFAULT 0
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
`auto`, the default, whenever the server has no key and the runner has a
provider set or a local model), the turn loop, the
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

- **Laya**, open source (Apache-2.0), on your PC. Install a pinned version in
  a virtual environment of its own, and start it with `LAYA_HOST=127.0.0.1`
  (by default it listens on `0.0.0.0:8000`, open to your whole network). The
  first call downloads its checkpoint (the three together are about 2.3 GB).
  No key unless you set `LAYA_API_KEY`.

  ```sh
  python -m venv ~/.laya
  ~/.laya/bin/pip install "laya[serve]==0.4.14"   # Windows: ~/.laya/Scripts/pip
  LAYA_HOST=127.0.0.1 ~/.laya/bin/laya-serve
  ```

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
  provider: auto             # auto (free: browser or local model, or the key's vendor) | anthropic | openai
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
- The header names the model in use: "Browser (Gemini Nano)", "Ollama ·
  qwen3:8b on this PC", or the configured provider and model. When the browser
  model has to be downloaded, or nothing free is available, the microphone
  button opens a help panel instead (download button with progress, or what
  to install or set).
- Permission card inline in the transcript: Allow / Allow for this conversation
  / Deny, keyboard `1`/`2`/`3`, or voice.
- A follow-up (a plan run ended) shows live in its open conversation, else
  as a dot on the mic button and ● in the conversation picker.
- Strings in `web/src/i18n.ts` (`copilot.*`, en and zh-TW).
- Speech recognition language follows the UI language (`zh-TW`, `en-US`).

## Out of scope for this release

Server-side STT, wake word, streaming tool-input display, multi-project turns.
