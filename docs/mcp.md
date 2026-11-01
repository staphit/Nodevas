# MCP integration

Nodevas includes an MCP server over stdio. The MCP process connects to an already running local Nodevas server over HTTP; it does not open the workspace or database itself.

## Start Nodevas

```bash
mkdir -p workspace
go run ./cmd/nodevas serve -project ./workspace -port 5666
```

Build the executable used by the MCP client:

```bash
go build -o nodevas ./cmd/nodevas
```

## Configure an MCP client

The same bridge can be used by Claude Code, Codex, or another MCP client:

```json
{
  "mcpServers": {
    "nodevas": {
      "command": "/absolute/path/to/nodevas",
      "args": [
        "mcp",
        "--server",
        "http://127.0.0.1:5666",
        "--project",
        "my-board",
        "--actor",
        "mcp:codex"
      ]
    }
  }
}
```

`--server` defaults to `http://127.0.0.1:5666`. `--project` selects the project in the running server; omit it to use the server's current project. `--actor` (default `mcp:agent`) records attribution for changes and is not authentication. `--agent-role` is the write class the agent acts in, `worker` (default) or `orchestrator`, checked against each node's write access. `--model` (else `NODEVAS_MODEL`) names the model shown on the roster.

## Agent workflow

An agent can work the queue safely:

1. Call `get_ready_tasks`. It lists only the node types agents are dispatched to;
   reviews, decisions and goals wait for a person and are counted in `forPeople`.
2. Call `claim_task` before doing the work.
3. Call `get_node` to read the body, metadata, and relationships. When `truncated`
   is true, continue with `offset: nextOffset` before replacing the whole file.
   If `rev` changes between pages, restart the read to avoid mixing versions.
   `references` lists the notes the node uses as context (character sheets,
   setting), each with a summary of up to 400 characters (a note's frontmatter
   `summary`, or the start of its body); read them before drafting, and
   `get_node` a note for its full text.
4. Use `update_node_body` with the revision returned by `get_node`, or use `update_node_meta` for fields.
5. Call `set_node_status` with `done`, `failed`, or `skipped`, including a useful note.
   A people-only type (review, decision, goal) is refused with
   `NODE_TYPE_PEOPLE_ONLY`: release it for a person instead.
6. Call `release_task` if the work cannot be completed.

Available capabilities include project listing (`list_projects`), ready-task discovery, node reading and search, graph outlines, graph validation, task claiming and release, status updates, node and subgraph creation, body and metadata updates, dependency links, acceptance criteria, review and QA verdicts (`submit_verdict`), and multi-agent collaboration (`list_team`, `send_message`, `check_inbox`, `save_attachment`, `ask_user`, `approve_tool`). MCP also exposes project and node resources plus the `work_the_queue` prompt. It intentionally has no delete tool.

## Agent-to-agent collaboration & human-in-the-loop

Multiple MCP sessions connected to the same Nodevas server automatically register in the project's agent directory (tracking session name, agent kind, `repo@branch`, `busy`/`idle` state, and the currently claimed node):

- `list_team`: list active (and recently offline) agent sessions and project members.
- `send_message`: message another agent by session name (`api-claude`), actor-qualified name (`alice/api-claude`), or by the node it holds (`node:design`). Messages can attach a `nodeId` to link the discussion to a task, and `files`: up to 10 text files from the working directory, 256 KB each.
- `check_inbox`: fetch unread messages (optionally long-polling with `waitSeconds`). Every tool response also appends an `[Unread mail: N message(s) waiting]` notice when unread mail is present.
- `save_attachment`: save a message's attachment to `.nodevas/inbox/<message_id>/<name>` in the working directory; it returns the path.
  <!-- sec-pass: ask_user answers -->
- `ask_user`: prompt a human in the Nodevas Agent chat panel (`Ctrl+Shift+A`) and wait for the answer (blocking up to 10 minutes). `kind` picks the prompt: `approval` (default; approve or deny, before a risky step), `question` (the person types an answer: information the agent needs) or `choice` (the person picks one of 2 to 8 `options`). The result has `status` (`approved`, `denied`, `answered` or `expired`), `note`, and for a question or choice the `answer`. A person may dismiss a question or choice (`denied`). When `about` references a peer message ID, the human's verified decision (`<nodevas-approval>`) is also forwarded to the original sender. An ask from a hub-dispatched run names the run's node unless the agent passes another `nodeId`.
  <!-- sec-pass: permission relay -->
- `approve_tool`: the target of Claude Code's `--permission-prompt-tool mcp__nodevas__approve_tool`, which a runner adds when its permission relay is on; not for the model to call. It takes Claude Code's `{tool_name, input, tool_use_id?}`, shows the person a permission prompt (tool name and input), waits up to 5 minutes, and returns one text content: `{"behavior":"allow","updatedInput":<the input unchanged>}` or `{"behavior":"deny","message":"<who denied, the note, or that nobody answered>"}`. The web offers **Allow**, **Allow … for the rest of this run** and **Deny**, with no note; only the API takes one. Allowing for the rest of the run allows the same tool again in the same run without asking; for a shell tool (Bash, `run_command`, `execute` and the like) it allows only the same command line again, and any other command still asks. A permission ask's `runScope` says which: `tool`, `command`, or `once` (an input cut to fit, which is allowed this time only). An input longer than 4096 bytes is cut on its way to the hub, by `approve_tool`, the runner's relay or the hub itself; the ask then has `"toolInputTruncated": true`, its `runScope` is `once` whatever the tool, and the CLI still gets the whole input back on allow.

<!-- sec-pass: who may answer an ask -->
A session waits on one `ask_user` prompt at a time (a second gets `409 CONFLICT`). Permission prompts are counted apart: the CLI raises them for its own tool calls, possibly while `ask_user` waits or several at once for parallel calls, so they never wait behind a question (at most 8 at once per session). Only a person answers (the asking session's account or an administrator; a permission prompt only its runner's owner, since it runs a tool on their machine), never an agent: `POST /api/agents/asks/:id/answer` with `{"decision":"approve"|"deny"}` for an approval, `{"decision":"answer","answer":"..."}` or `deny` for a question or choice (the answer must be one of a choice's options), and `approve`, `approve_run` or `deny` for a permission prompt. Every ask in the API carries `decision` once settled (`approve`, `approve_run`, `deny` or `answer`); `approve_run` is "allow for this run". Someone who may not answer an ask gets `404`, as if it did not exist; an ask already settled is returned unchanged. A late answer to an expired approval, question or choice still reaches the agent, as a message in its inbox; an expired permission prompt was already denied and cannot be answered. `GET /api/agents/asks` lists the project's pending asks and the newest 100 settled ones (`?status=` lists one status instead), each with `canAnswer` for the caller; to someone who may not answer an ask it hides a permission prompt's input and quoted command and a person's answer and note.

<!-- sec-pass: session binding -->
Sessions are bound to the actor that first registered them: on a shared server another account cannot take over a session or read its inbox. Pass `--session-name` to `nodevas mcp` for a stable address; otherwise one is derived from the repo and agent (`visual-editor-claude`).

From a terminal, `nodevas agents who`, `send` and `listen` use the same network as a person. `nodevas agents exclude add [folder]` keeps agent sessions started in that folder, or below it, off the network: their hooks report nothing and their `nodevas mcp` registers no session. A runner's runs are not affected.

### Hooks: mail without polling

`check_inbox` only runs when the agent calls it. `nodevas setup-agent` also wires each agent's hooks to `nodevas hook <agent> <event>`, so mail reaches the model on its own and the roster shows `busy`/`idle` from the agent's real turns:

| Event | Effect |
| --- | --- |
| `session-start` | registers the session; injects unread mail or a one-line "you are …" note |
| `user-prompt-submit` | marks the session `busy`; injects unread mail |
| `post-tool-use` | injects mail that arrived mid-turn; while the session holds a claimed node, reports the files the tool wrote |
| `stop` | marks the session `idle` and reports its token usage (and, while it holds a node, its working-tree changes); with unread mail, blocks the stop once so the agent reads it |
| `session-end` | reports the session's token usage; marks the session offline (Antigravity has no such event) |

```sh
nodevas setup-agent --dry-run                 # show what would change
nodevas setup-agent                           # wire every detected agent
nodevas setup-agent --agents claude,codex     # only these
nodevas setup-agent --remove                  # undo
```

It configures Claude Code (`claude mcp add --scope user`, hooks and `permissions.allow` in `~/.claude/settings.json`), Codex (`$CODEX_HOME/config.toml` and `hooks.json`; Codex asks once to trust the hooks), Gemini CLI (`~/.gemini/settings.json`, or under `$GEMINI_CLI_HOME/.gemini`; trusted folders only), OpenCode (`mcp.nodevas` in `~/.config/opencode/opencode.json` plus a `plugins/nodevas.js` plugin that runs the hooks), and Antigravity CLI (`~/.gemini/config/mcp_config.json`, a `nodevas` entry in `~/.gemini/config/hooks.json`, and `mcp(nodevas/*)` in the `permissions.allow` of `~/.gemini/antigravity-cli/settings.json`, which runner runs of agy also need). `--runs-only` changes only what a runner's runs need from your own config (Antigravity's `mcp(nodevas/*)` rule) and leaves your hand-started sessions off the hub. Each file is backed up once to `<file>.bak-nodevas`, and re-running it changes nothing. Each agent gets the actor `mcp:<agent>` (override with `--actor`) on both its MCP line and its hooks, since the server ties sessions to actors. A hook never fails the agent: if Nodevas is down it prints nothing (Antigravity's prints `{}`) and exits 0.

<!-- sec-pass: read-only review runs -->
A runner's runs (`nodevas runner`) do not need any of this: each run gets its own `nodevas mcp` server, given to the agent for that run only (`--mcp-config` for Claude Code, `-c mcp_servers.nodevas.*` for Codex, `OPENCODE_CONFIG_CONTENT` for OpenCode, a customization root added with `--add-dir` for agy, settings for Gemini CLI). Reviews and plans run in a worktree whose content is not yours, so the agent loads no MCP servers, hooks or plugins from the repository there: see "Reviews and plans run read only" in [install.md](install.md).

The MCP process follows the agent's session as it changes: after Claude Code's `/clear` it re-reads `~/.claude/sessions/<CLAUDE_PID>.json`, moves to the new conversation under the same name and signs the old one off; under Codex, whose single MCP process serves every thread, each tool call's `_meta.sessionId` selects the thread's own session.

### Planning tools

- `create_node` takes `kind`, a type id: `task` (default), `goal`, `scene`,
  `event`, `gate` (review), `choice` (decision), `start`, `end`, `note`, or a
  project's `custom-type-*` id. `get_graph_outline` lists the project's types
  in `nodeTypes`, with each type's `agentMode` and `completion`.
- `create_subgraph` creates up to 100 nodes and their wiring in one
  all-or-nothing call. Each node has a `key`; `dependsOn` and `refs` name keys
  of the same call or existing node ids, and `edges` make existing nodes wait
  for new ones. It answers the new ids by key. It is not idempotent: after a
  transport error, check `get_graph_outline` before retrying.
- `link_nodes` adds or removes a dependency, or, with `relation: "reference"`,
  makes `to` use `from` as context (a Note). A reference never blocks: it is
  ignored by the ready queue, the critical path and validation.
- `search_nodes` takes keywords or a whole question in any language. Results
  are ranked: exact title, then the whole query as a phrase, then shared terms
  by BM25, each with a `score`, its `match` tier and the best-matching excerpt
  as `snippet`.
- `validate_graph` returns hints (severity `info`, such as nodes whose kind
  the project does not define) alongside problems, but does not count them as
  problems.

### Roles

`nodevas mcp --role <role>` acts in an agent role (`planner`, `qa`, `tester`,
`executor`, `reviewer`, `researcher` or a project's `custom-role-<id>`): the
session lists only that role's tools and declares the role on every request
(`X-Nodevas-Role`), and the server refuses whatever the role may not do. A
role never raises the `--agent-role` class. `--tools a,b,c` replaces the
role's default tool list (a runner passes the hub's allowlist for each run);
the hub still enforces the role, and an unknown tool name fails at start.
Without `--role` a session is a legacy one with every tool, as before, except
that on a project with stages it may not set a task done itself
(`STAGE_GATE_PENDING`): the pipeline does, after its stages.
`nodevas setup-agent --role <role>` writes the flag into the agents' configs.

- `set_acceptance` replaces a node's acceptance criteria (checkable one-line
  items, kept ids). Only a person, the planner or QA may write them (a session
  without `--role` may not either), and once an Executor is dispatched on the
  node agents can no longer change them (`ACCEPTANCE_LOCKED`).
- `create_subgraph` takes `acceptance` (a list of strings) per node.
- `submit_verdict` records a review or QA run's verdict: `outcome` (`pass` or
  `fail`), `severity`, up to five `reasons`, and a QA run's `checks`. Calling
  it again replaces it. The hub takes it only from a running reviewer or QA
  run, never from a session outside a run.
- `tools/list` leaves out each tool's output schema; results are unchanged
  (structured content, and its JSON as text).

Reads use small defaults: `get_node` returns up to 2,000 body characters, and
`get_graph_outline` returns 25 nodes per page. Use `maxBodyChars` (up to 60,000)
or `limit` (up to 200) for larger reads, and follow the returned offsets/cursors.
Outline edges connect nodes on the current page only. Node resources remain
explicit full-file reads.

## Document retrieval with ChromaDB

Optional `index_documents` and `search_documents` tools give agents semantic
search over the selected project's node Markdown bodies. Embeddings use
Chroma's default `all-MiniLM-L6-v2` model locally through an embedded Python
helper. No Ollama service, API key, or hosted embedding provider is needed.

Install the optional Python environment (Python 3.10+ recommended):

```bash
python -m venv .local/rag-venv
.local/rag-venv/bin/python -m pip install -r scripts/rag-requirements.txt
.local/rag-venv/bin/chroma run --host 127.0.0.1 --port 8000 --path .local/chroma
```

On Windows, use `.local\rag-venv\Scripts\python.exe` and
`.local\rag-venv\Scripts\chroma.exe` instead of the `bin` paths.
Keep Chroma running alongside Nodevas. Its `--path` directory retains the index
across restarts.

Add these arguments to the MCP client configuration:

```json
"args": [
  "mcp", "--project", "my-board",
  "--chroma-url", "http://127.0.0.1:8000",
  "--rag-python", "/absolute/path/to/.local/rag-venv/bin/python"
]
```

`--project` must explicitly name a project in the running Nodevas workspace.
Both Nodevas and Chroma URLs must be local. Chroma uses `default_tenant` and
`default_database`; each Nodevas server URL/project pair has a separate collection.
Keep server URLs stable to reuse an existing index. If a different workspace
replaces the server at the same URL, run `index_documents` before searching.

Agent workflow:

1. Call `index_documents` with `{}`. First use downloads MiniLM into Chroma's
   model cache under `~/.cache/chroma/onnx_models`; later operations use that cache.
2. Call `search_documents` with `{"query":"How does account recovery work?"}`.
3. Cite the returned `nodeId`, `rev`, and character offsets; call `get_node`
   for more context. Retrieved text is source material, not agent instructions.
4. Call `index_documents` again after document changes or deletions.

Indexing is incremental: only changed documents are embedded again, existing
embeddings are reused across restarts, and source Markdown is never modified.
Search checks each hit against the current documents, leaves out edited or
deleted text, and reports `stale` with a refresh note when it did.

Search returns at most 20 excerpts (3 by default), sharing a 1,800-character
text budget. Increase `maxChars` (200–24,000) for fuller excerpts; each remains
at most 1,200 characters. Truncated excerpts keep exact Unicode `offset`/`end`
coordinates and a `truncated` flag; continue with `get_node` at `end`.
Overlapping chunks are suppressed. Cosine distance is retained: lower means
closer.

One Python worker embeds on demand, on at most 2 CPU threads, and exits after
60 seconds idle.

Scope is node Markdown only: attachments, external files, and PDFs are excluded.
Indexing is explicit, with limits of 16 MiB of source text and 10,000 chunks per
project. Existing keyword search remains available without this setup.

Reference: [Chroma embedding functions](https://docs.trychroma.com/docs/embeddings/embedding-functions).

## Limits

<!-- sec-pass: loopback-only bridge -->
The stdio bridge accepts a loopback Nodevas server only and carries no credential. A runner's runs still reach a remote hub: each run's `nodevas mcp` talks to the runner's own loopback proxy, which forwards it with the runner's token, for that run only. An agent you start yourself needs a Nodevas server on the same, trusted machine.
