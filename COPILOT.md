# Copilot

A copilot inside the Nodevas UI that you type to (or talk to). Describe what you want and it drafts the nodes for you to check before anything is created; or ask it about the project. It reads and edits the board with the same tools agents get over MCP, less those that need files or a run (it cannot attach files to `send_message`, and has no `save_attachment`, `set_acceptance` or `submit_verdict`, nor acceptance criteria in `create_subgraph`; its roster entry shows no repository or folder) and those agents use to coordinate while working tasks (`claim_task`, `release_task`, `check_inbox`, `list_team`, `ask_user`, `approve_tool`), asks before changing things as its permission mode says, and keeps each conversation's transcript, per account and project. With one of your runners online, it also turns a requirement you dictate into tasks and starts the autopilot on them.

## Using it

- **Copilot** at the bottom right, or `Ctrl+Shift+Space`, opens and closes the panel with the cursor in the text box. `Enter` sends, `Shift+Enter` adds a line; a list or a pasted spec is fine, and the box grows to eight lines.
- The switch above the box says what sending does:
  - **Make nodes** (the default) drafts nodes from what you wrote. Nothing is created yet: the draft shows in the panel, and as dashed ghost nodes on the board, placed after the nodes you selected or to the right of the board. With canvas nodes selected, the box says the new steps go after them.
  - **Ask** is the chat: questions about the project, and changes to existing nodes, with the permission card as the mode says. Asked there to add several nodes, it drafts them as **Make nodes** would (its `propose_nodes` tool, which writes nothing and needs no permission, so it runs in every mode, plan included): the draft card appears in the conversation. It writes nodes straight to the board (`create_subgraph`, behind the permission card) only when you ask for a direct write.
  The switch is remembered for your account. Three example requests in the empty panel fill the box when clicked; they are not sent.
- **The draft.** Its header counts the nodes and dependencies and names the model that drafted it. Rename a step (double-click), remove it (its dependents then wait for what it waited for, so the chain stays connected), merge two, change a kind, or change what a step waits for (the 「在…之後」 picker, or drag a ghost's handle onto another ghost or a node). Problems show on the step: a cycle, an unknown kind or link, or a likely duplicate of a node already on the board, with **Link to it instead**. Each edit is checked against the board, as you, after a short pause.
  - To change a copilot draft by typing, send another request while it is open (the box then says **Change this draft…**; a coding agent's plan is not changed by typing, see below): the copilot gets the draft with your edits, and the card marks what was added, changed and removed.
  - **Create** (or `Enter` in the empty box) creates the nodes where the ghosts were, as you, under your permissions; a viewer of the project sees why it is disabled. **Discard** drops the draft. A copilot draft is at most 40 nodes (a coding agent's plan, below, up to 100 steps).
  - A draft is created once. If it already was, from another tab on the same conversation say, **Create** creates nothing: the card shows it as created and the toast appears without Undo.
  - The card keeps your edits: after a reload it shows the draft as you left it and, once created, what was created.
  - Each created node's history says "created from a Copilot draft (copilot-draft:…, ollama · qwen3:8b)": where it came from and the model label of who drafted it (for a plan, the agent and its model), which stays when the conversation is deleted.
  - **Plan with my coding agent** hands the request to the chat, which offers your coding agent's plan run (deeper, a few minutes, your quota); it is highlighted for a large draft or one that depends on the code.
- **Undo.** After Create, 「已建立 n 個節點 · 復原」 shows for ten seconds, and `Ctrl+Z` undoes while it is the last change; the server's undo has no time limit while the nodes are unchanged (a project keeps its last 200 batches). Undo moves the created nodes to the trash, except the ones that changed since (their fields or their status), that someone has claimed or started a run on, or that you may not delete. Those are kept, and the toast and the card list them by title with the reason, each with **Delete anyway** (「仍要刪除」), which deletes it the normal way, with that delete's usual checks. The draft opens again for editing only when every node went to the trash, or once Delete anyway has removed the last kept one; until then the card stays created and says which. The kept list belongs to the project the undo ran in and goes when another project opens. The toast stays while it lists kept nodes. Redo puts the trashed nodes back with their links into existing nodes; when none could be trashed, there is nothing to redo.
- **Voice.** Hold `Alt+Space` (or the microphone in the box) to dictate; it keeps listening while held, and the text lands in the box for you to check and send. **Send when I stop talking** sends it as it is. A spoken yes or no still answers a permission card. Speech recognition is the browser's own (Chrome and Edge) and follows the interface language. **Speak replies aloud** is off by default.
- The panel header names the model in use, such as "Browser (Gemini Nano)" or "Ollama · qwen3:8b on this PC".
- Besides the board's tools, it can open a node, show the graph or the timeline, and switch to another project, which ends the conversation.
- Its tokens count in **Settings → Agents → Advanced → Usage details** (shown once **Let agents work here** is on) as the agent `copilot`: your own turns, or everyone's for an administrator.

## Which model answers

A configured provider or key always wins: **Anthropic** or **OpenAI-compatible** chosen by an administrator as the **Model** in **Settings → General → Voice copilot** (or with `copilot.provider` or `NODEVAS_COPILOT_PROVIDER`); a key on the server, whose vendor answers while the provider is left automatic (Anthropic for a key that does not say); or `copilotProvider` set on your runner (unless **API key lives on**, below, is **This server**). With none of them, the default, it uses a free model and no key, in this order:

1. Ollama (`127.0.0.1:11434`) or LM Studio (`127.0.0.1:1234`) on your runner's PC, else on the server, preferring a model that can call tools;
2. the browser's built-in model (Gemini Nano in Chrome 148 or later on a desktop), when it is ready: it is the smallest and is declared for English (and your interface language when Chrome's model takes it), so it comes last;
3. the browser's model again, once you click **Download the on-device model**.

A draft is one structured call: the model answers JSON in the draft's schema (the provider's structured output where it has one), reading up to 150 of the board's nodes and answering in at most 12 000 tokens. An OpenAI-compatible model that is not a reasoning model is asked for temperature 0.2; Anthropic's models think, so they take none. An answer that fails the board's checks gets one repair call naming the problems; after that the draft shows them per step.

Ollama's default context window is small for a board listing; start it with `OLLAMA_CONTEXT_LENGTH=16384` (for example `OLLAMA_CONTEXT_LENGTH=16384 ollama serve`) so a draft sees the whole board around your request.

When none is available, the button opens a help panel: use Chrome on a desktop, install Ollama and `ollama pull qwen3:8b`, or set a key on the server or your runner.

### A key on the server

Set `NODEVAS_COPILOT_API_KEY` (or `NODEVAS_COPILOT_API_KEY_FILE`), `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in `serve`'s environment, or have an administrator store one with `PUT /api/copilot/settings` (`{"apiKey": "…"}`), which keeps it encrypted under the machine key (or `NODEVAS_SECRET_KEY` / `NODEVAS_SECRET_KEY_FILE`; see [Operations](./docs/operations.md#database)), never in the database or `nodevas.yaml`. Anyone who breaks into the server can take a key kept there. The runner `serve --with-runner` starts gets `NODEVAS_COPILOT_API_KEY`, `ANTHROPIC_API_KEY` and `OPENAI_API_KEY` from `serve`'s environment, since its copilot and agents use them; it, git and the PostgreSQL tools get none of the hub's own secrets (`NODEVAS_SECRET_KEY*`, `NODEVAS_DATABASE_URL*`, `NODEVAS_SMTP_PASSWORD*`, `PGPASSWORD`, `PGPASSFILE` and the like). A key does not follow an endpoint change: saving another `baseUrl` while a key is stored answers `400` until the key is entered again, and a key from the environment is used only for the vendor's own endpoint or the base URL the operator set (`copilot.base_url` or `NODEVAS_COPILOT_BASE_URL`). On a server with accounts, each account runs at most two turns at once on the server's model (its key, or a model on the server's machine); a third answers `429` `copilot_busy` until one ends, and the web says the copilot is busy with your other turns and to send again once one finishes. An OpenAI-compatible server that needs no key (Ollama or vLLM on another machine) takes `provider: openai` and its `base_url` instead.

### Key on the runner (`keySource`)

**API key lives on**, in the same Settings section, decides where the model calls run: **Each person's runner (recommended)**, **Automatic** (the default: the server's key when it has one, otherwise each person's runner) or **This server**. Only an administrator changes it. With **Each person's runner**, only the runner's model answers: the browser's model and a local model on the server are not used. With the runner, the turn, the tools and the permission checks stay on the server; each model call goes to the speaker's own runner, which makes it with a key kept on that PC:

```sh
nodevas runner set copilotProvider anthropic   # or openai
nodevas runner set copilotModel <model>        # optional; the provider's default otherwise
nodevas runner copilot-key                     # the key on stdin, never a flag
```

Then restart the runner. `NODEVAS_COPILOT_API_KEY` in the runner's environment works instead of a stored key. `copilotBaseURL` points at an OpenAI-compatible server (Ollama and LM Studio need no key) or a proxy; it must be https, or http to this machine or the local network. With no `copilotProvider`, the runner offers a local Ollama or LM Studio it finds; `nodevas runner set copilotProvider off` stops that.

The key never reaches the server: the runner names its provider and model, never the key. It refuses a call that names another provider, base URL or model, or that is for anyone but its owner. A server that is broken into can still spend the owner's quota, within limits: two calls at once, 40 a minute, 64 000 output tokens and ten minutes per call, each logged. While the runner is offline, the button stays and says how to start it.

## Permission modes

Reads always run; whatever changes something follows the conversation's mode. Pick it in the panel; `Shift+Tab` cycles.

| Mode | `read` | `write` | `risky` |
| --- | --- | --- | --- |
| **Plan** (`plan`) | run | refused; it proposes the change instead | refused |
| **Ask before edits** (`default`) | run | ask | ask |
| **Accept edits** (`acceptEdits`) | run | run | ask |
| **Bypass permissions** (`bypassPermissions`) | run | run | run |

Writes are ordinary edits: creating and linking nodes, changing a node's fields or status. A draft is not a write: `propose_nodes` and **Make nodes** only propose, so they run in every mode, plan included, and only your **Create** writes, as you. No mode, bypass included, creates a draft's nodes without your **Create**. Replacing a page wholesale, or any tool not classed as read or write, is risky. An ask is a card in the transcript: **Allow**, **Allow for this conversation** or **Deny** (keys `1`, `2`, `3`), or say "yes" or "no" ("確認" or "取消" in Chinese); after a spoken request it listens for the answer. No answer within 120 seconds is a deny.

`bypassPermissions` is offered only when the server allows it (`copilot.allow_bypass`; unset, it is allowed on a loopback-only server and refused with accounts). A bypass request the server does not allow runs as `acceptEdits`. `allow_bypass: true` is refused in the workspace's own `nodevas.yaml`: an administrator sets it in a file given with `--config` (or `NODEVAS_CONFIG`).

With the browser's built-in model, bypass is never offered and a turn that asks for it is refused: node content can carry prompt injection, and a small model is the easiest to mislead.

## Planning by voice

Describe something to build. The copilot restates it and, once you confirm, files it as a goal node and starts a plan run on your runner: an agent reads the repository and drafts task nodes for the goal, changing no files. It takes your first online runner with a repository and the first agent installed there (Claude Code, Codex, OpenCode, Gemini CLI, Antigravity) with its default model, unless you name others. Meanwhile the goal's **Autopilot** pill reads **Breaking down…**. Steps that dispatch work ask you every time, bypass included, and never offer **Allow for this conversation**; plan mode refuses them. Your runner still decides whether to take each run.

The plan run writes nothing on the board: it is a draft run. The hub checks each of its `create_subgraph` calls as if it were made and records it on the run instead; any other board write the agent tries (create, link, edit, claim, set acceptance) is refused with `409 PLAN_RUN_DRAFTS`, telling it to send one `create_subgraph`. When the run ends, the plan comes back to the conversation as a draft card, like the copilot's own drafts, with the agent and model that planned it and the goal, and per step how many tags and acceptance criteria the planner wrote. Edit it, then press **Create**: the tasks are created as you (history: "created from a coding agent's plan (via Copilot)"), each tagged for the goal, with the planner's tags and priority, its criteria where the step's type takes them (a step changed to a type that takes none shows its criteria greyed, and Create leaves them out), and the goal waits for every step other than a note that no other step waits for. **Undo** takes them back as with any draft.

A plan draft differs from the copilot's own in four ways. It holds up to 100 steps. Typing does not change it: while it is open, **Make nodes** does not send, says "Create or discard the coding agent's plan first, or switch to Ask" and puts your text back in the box. Each step's description is kept to its first 600 characters, and the card warns on a step that was cut; the tags and criteria are kept whole and shown as counts. The planner's priority is not shown, but Create applies it.

Only `start_breakdown` starts such a run; plan runs started any other way, by the autopilot or by a revision, write directly as before. While a goal's plan is a draft (open, or still being drafted), the copilot refuses `start_autopilot`, `revise_goal` and another `start_breakdown` on that goal, in any of your conversations in the project; it does not stop the goal's own Autopilot panel (the goal is visibly empty there) or another person's draft of the same goal. So an open plan card keeps its **Create** and **Discard** even after a newer draft follows it (a copilot draft is replaced by its refinement, a plan never is). A plan run that recorded its plan and then failed or was cancelled still hands the plan over, marked as possibly incomplete. If `start_breakdown` cannot record the follow-up that brings the plan back, it cancels the run at once and says so. The autopilot takes at most 25 tasks, so remove steps from a longer plan before **Create**, or split the goal. If the goal is deleted before Create, the server refuses the link to it and the card says so.

You need not ask how it went. When the plan run ends, the copilot writes the outcome and the plan into the conversation by itself; once you have created the tasks, it can start the autopilot: by default 2 tasks at a time, up to 2 review rounds, and you approve every pull request before it merges. If that conversation is not open, the **Copilot** button shows a dot (a click opens it) and the browser notifies you when allowed. On a yes it starts the autopilot and tells you who implements: the agents you named, else the advisor your runner offers (below), else your routing rules, whose last resort is the first installed agent's default model, often the most expensive.

### Revising a goal mid-way

Tell the copilot how the requirement changes. Once you confirm, it pauses the goal's autopilot (running tasks carry on; only whoever started that autopilot can revise the goal), records the change on the goal under a dated **Revisions** heading, and starts a replan run; the pill reads **Revising…**. Done tasks stay, pending ones may be changed or skipped (a skipped task keeps its history), ones in progress are only flagged, and new ones are added. When it ends, the copilot tells you what was kept, changed, skipped and added and asks whether to resume; nothing resumes until you say yes or press **Resume with revised tasks** in the goal's Autopilot panel.

### When the target is busy

Before `start_breakdown` creates a goal node or starts its plan run, and before `revise_goal` pauses the autopilot, the copilot asks the hub whether another run is already at work on that repository or goal. If one is, nothing is created, paused or started. The tool answers that the target is busy and lists what is running as Station shows it: each run's title, agent and state, who started it and since when, and another member's run only as a count. The copilot tells you, and you start the work from the board, where you choose to wait in line, take over or run both (see [Busy choices](./docs/workstation.md#busy-choices)). The copilot's tools never make that choice for you, so a model that reads board text cannot stop anyone's run. The autopilot `start_autopilot` starts is an automatic start: it waits in line behind a busy node on its own.

## The autopilot's advisor model

With **Choose per task** in the goal's Autopilot panel, **Chosen by** picks each task's implementer: **Routing rules**, **A fast model on your runner** or **System One (Laya / Jev)**. An autopilot the copilot starts without named implementers uses what your runner offers, System One first. An advisor picks only among the agents and models your runners report; on an error, an offline runner or no answer within 5 seconds (10 for System One), the routing rules decide. The fast model, on the runner:

```sh
nodevas runner set advisorProvider openai        # or anthropic
nodevas runner set advisorModel <fast-model-id>
nodevas runner set advisorBaseURL https://…/v1   # optional: any OpenAI-compatible endpoint
nodevas runner advisor-key                       # stdin; or NODEVAS_ADVISOR_API_KEY
```

With the copilot's provider, the copilot key serves both. Restart the runner after a change.

### A System One advisor (Laya or Jev)

`advisorProvider systemone` asks a System One endpoint instead: a model that answers a typed choice in a fraction of a second and generates no text.

- **Laya**, open source, on your PC: a pinned version in a virtual environment of its own, bound to this machine (by default it listens on `0.0.0.0:8000`, open to your whole network). The first call downloads about 2.3 GB. No key unless you set `LAYA_API_KEY` (then `nodevas runner advisor-key`).

  ```sh
  python -m venv ~/.laya
  ~/.laya/bin/pip install "laya[serve]==0.4.14"   # Windows: ~/.laya/Scripts/pip
  LAYA_HOST=127.0.0.1 ~/.laya/bin/laya-serve
  nodevas runner set advisorProvider systemone     # advisorBaseURL defaults to http://127.0.0.1:8000
  ```

- **Jev**, TypeSafe's hosted model (early access; key from its console).

  ```sh
  nodevas runner set advisorProvider systemone
  nodevas runner set advisorBaseURL https://api.typesafe.ai
  nodevas runner set advisorModel jev-latest
  nodevas runner advisor-key                       # stdin; sent as a bearer token
  ```

## Configuration (`nodevas.yaml`)

```yaml
copilot:
  enabled: true
  provider: auto             # auto | anthropic | openai (any OpenAI-compatible endpoint)
  model: ""                  # anthropic or openai only; empty: the provider's default model
  base_url: ""               # anthropic or openai only, e.g. http://127.0.0.1:11434/v1 (Ollama, no key)
  effort: low                # low | medium | high; voice favours latency
  # allow_bypass: false      # unset: allowed on a loopback-only server, refused with accounts
  max_tool_calls_per_turn: 25
  agent_role: orchestrator   # orchestrator | worker, enforced against node write_access
```

The workspace's own `nodevas.yaml` may not set `allow_bypass: true` or a `base_url` off this machine (a local Ollama or LM Studio is fine): `serve` refuses to start. Put those in a file given with `--config` (or `NODEVAS_CONFIG`), or use `NODEVAS_COPILOT_BASE_URL`. `NODEVAS_COPILOT_PROVIDER`, `NODEVAS_COPILOT_MODEL`, `NODEVAS_COPILOT_BASE_URL` and `NODEVAS_COPILOT_ENABLED` override these keys, and `nodevas serve --copilot=false` turns the copilot off. What an administrator saves in Settings or with `PUT /api/copilot/settings` overrides the file without a restart.

## How drafts are built (for contributors)

The decisions behind drafts, kept here so the code can be read against them.

- **One draft, one Create.** Nodes made from a description go through a draft and the person's one **Create**, in every permission mode, bypass included: the model never writes a draft to the board, and Create writes as the person (their session, their project permissions), not as the `copilot:<name>` agent. The one direct path is the Ask chat's `create_subgraph`, for a person who asks for a direct write, behind the permission card. A draft creates new nodes and links them to existing ones; editing or deleting existing nodes stays in the chat. Not in drafts: assignees, deadlines and write access, nor acceptance criteria in the copilot's own drafts (a coding agent's plan keeps its planner's). A draft is private to its author until Create; ghosts are not broadcast.
- **A draft is a turn.** `POST /api/copilot/conversations/:id/turns` with `intent: "draft"` (`previous` and `refineOf` to refine), streamed as NDJSON like every copilot turn. It appends an `EntryDraft` whose `data` (`DraftData`) holds the draft, its issues, the provider and model, and its state. The web reports every state change with `PUT /api/copilot/conversations/:id/entries/:entry/draft-state`: `state`, `batchId`, `ids`, and an optional `draft`, the person's final draft (sent on Create, and when the draft opens again after an undo), held to the same bounds and checks as a turn's `previous` (40 nodes, 100 for a plan; invalid is `400`); it replaces the stored draft and sets `edited`.
- **`propose_nodes`.** A server tool offered in Ask turns, input `{"description": …}`. It is a read: it asks no permission and runs in plan mode. It runs the drafter with the turn's model, canvas selection and language, appends an `EntryDraft` to the same conversation (the usual `entry` event, so the web needs nothing new), and answers "Drafted N nodes as a draft card for the person to review and create; nothing is on the board yet." or the drafter's questions. The chat's authoring guidance points at it, and at `create_subgraph` only for a direct write.
- **Check and commit are project routes,** under `/api/graph`, because `/api/copilot/` is account-level and the project write gate never runs there: `POST /api/graph/subgraph/check` (the dry run: the commit's every check, no write, no history, no event; an abuse class of its own; read-only members may call it), `POST /api/graph/subgraph` as the commit (with `positions` and `batch`), and `POST /api/graph/batches/:id/undo` and `/redo`.
- **Caps are fixed constants** (`internal/copilot/draft_schema.go`): 40 nodes per copilot draft, 100 per plan draft, 12 000 output tokens, 150 board nodes in the prompt, titles of 60 and bodies of 600 characters. No per-user setting, no server ceiling, no expiry sweeper: a draft stays with its conversation.
- **Positions** are the canvas's integer grid cells (column 0–499, row 0–200), not pixels. A commit without them writes none, and the web places the nodes.
- **The drafter** (`internal/copilot/draft*.go`) makes one structured call: `response_format` with the JSON schema on an OpenAI-compatible server (asked again without it when the server refuses it), Anthropic's structured output, the browser model's `responseConstraint`, and over the runner wire `responseSchema` and `temperature` (0 to 2) forwarded with the call. Temperature 0.2 is sent only to an OpenAI-compatible model that is not a reasoning model. The answer is checked against the board it was given (`validateDraft`); on problems one repair call gets the exact list, and a draft still wrong after that carries per-node issues. The card then dry-runs the draft, and each edit after a short pause, with `/api/graph/subgraph/check` as the person. A truncated or unreadable answer is an error, never an empty draft. The board is read through the copilot's tool bridge (`get_graph_outline`) and goes into the prompt as a data block marked untrusted.
- **Batches** (`internal/store/batch.go`). A commit's `batch.via` names where it came from (`copilot-draft:<entry>`, or `copilot-plan:<entry>` for a plan, which may hold 100 nodes) and the optional `label` is the model label (at most 80 bytes after trimming, control characters stripped); both go into the batch record and the history note, "created from a Copilot draft (<via>, <label>)". There is no per-node revision, so the record keeps per node a fingerprint of its own fields and its status; undo refuses a node whose fingerprint or status changed, that is claimed, that a run is on, or that the caller may not delete (codes `changed`, `status`, `claimed`, `busy`, `denied`), and only the batch's actor or an administrator may undo. Redo restores from the trash and puts back the batch's links into nodes that were already there, which a plain restore loses. A record that cannot be written leaves the commit standing and answers `undoable: false`, and the toast has no Undo. A commit whose `via` names a batch that is recorded and not undone answers `409` `BATCH_VIA_TAKEN` with that `batchId` and creates nothing (checked under the commit's lock; an undone batch frees its via, and so does one with none of its nodes left on the board); the web then marks the card created with that batch, and the draft-state call it sends without ids keeps the ids the creating tab stored. A redo that restores nothing leaves the batch undone unless some of its nodes are already back on the board, which makes it live again.
- **A coding agent's plan as a draft** (the run's `draft` flag, migration `0041`):
  - Only the copilot's `start_breakdown` starts one. `POST /api/runs` takes `draft: true` only from a request a copilot server tool replays in-process (`auth.WithCopilotServerTool`; a person's own POST is `400`), and only for the person's plain plan run (no revision, planner or no role); `runs.Store.Create` and the `runs_draft_shape` constraint refuse any other shape again. A draft run's only reader is its follow-up, so the tool cancels the run when the follow-up cannot be recorded.
  - The hub, not the runner, holds the writes. `serveRunnerProxy` refuses the run's graph and node writes by path prefix (`planDraftRefuses`, so a write route added later is refused by default), and `postSubgraph` hands its `create_subgraph` to `capturePlanDraft`, which checks it as the run's agent and merges it into `run_plan_drafts`: at most 8 calls, 100 nodes and 2 MiB; a key sent again replaces that node; an edge to the goal is dropped and any other edge refused. Whether a run is a draft comes only from its row, through an unexported context key; nothing the agent sends can set it. A runner too old to know `Job.Draft` gives the old prompt; its other writes get the `409` telling it to send one `create_subgraph`, which is still captured.
  - A node body that opens with front matter is refused at capture and stripped again when the card's draft is built (`planDraftBody`, used for the copilot's own drafts too): front matter would set an assignee, requirements, effects, priority or tags the card never shows.
  - The follow-up reads the capture through `Options.PlanDraft`, not a route; retention and the missing read route are explained in `internal/runs/plandraft.go`. The draft entry stores no message; the model reads the plan's current state on each turn (`planDraftStateNote` in `internal/copilot/plan_draft.go` says why).
  - Create sends the planner's tags plus `goal:<id>` on every step, its criteria only where the step's type takes them (`takesAcceptance`, the web's mirror of `engine.Graph.AcceptanceAllowed`), its priority, and an edge to the goal from every step other than a note that no other step waits for. No `created_by_run` is recorded: the autopilot reads it only in its own plan phase, which a goal with tagged, linked tasks never enters.
- **Evals.** `internal/copilot/draft_eval_test.go` (build tag `copilot_eval`, not in CI) scores the drafter on `internal/copilot/evals/cases.json` with a real model; its header has the commands and the last baseline.
