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
- Visitors and read-only actors are forced to `plan`.
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
| `GET`/`PUT` | `/copilot/settings` | admin: provider kind, model, base URL, effort, API key (write-only), allowBypass |

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
