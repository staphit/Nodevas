# Voice Copilot

A copilot inside the Nodevas UI that you talk to or type to. It reads and edits the board with the same tools agents get over MCP, asks before changing things as its permission mode says, and keeps each conversation's transcript, per account and project. With one of your runners online, it also turns a requirement you dictate into tasks and starts the autopilot on them.

## Using it

- **Voice Copilot** at the bottom right, or `Ctrl+Shift+Space`, opens and closes the panel. Hold `Alt+Space` to talk; the panel's microphone button works too (hold it, or click to start and stop).
- Speech recognition is the browser's own (Chrome and Edge) and follows the interface language; elsewhere, type (`Enter` sends, `Shift+Enter` adds a line). **Speak replies aloud** is off by default.
- The panel header names the model in use, such as "Browser (Gemini Nano)" or "Ollama · qwen3:8b on this PC".
- Besides the board's tools, it can open a node, show the graph or the timeline, and switch to another project, which ends the conversation.
- Its tokens count in **Settings → Agents → Advanced → Usage details** (shown once **Let agents work here** is on) as the agent `copilot`: your own turns, or everyone's for an administrator.

## Which model answers

A configured provider or key always wins: **Anthropic** or **OpenAI-compatible** chosen by an administrator as the **Model** in **Settings → General → Voice copilot** (or with `copilot.provider` or `NODEVAS_COPILOT_PROVIDER`); a key on the server, whose vendor answers while the provider is left automatic (Anthropic for a key that does not say); or `copilotProvider` set on your runner (unless **API key lives on**, below, is **This server**). With none of them, the default, it uses a free model and no key, in this order:

1. the browser's built-in model (Gemini Nano in Chrome 148 or later on a desktop), when it is ready;
2. Ollama (`127.0.0.1:11434`) or LM Studio (`127.0.0.1:1234`) on your runner's PC, else on the server, preferring a model that can call tools;
3. the browser's model again, once you click **Download the on-device model**.

When none is available, the button opens a help panel: use Chrome on a desktop, install Ollama and `ollama pull qwen3:8b`, or set a key on the server or your runner.

### A key on the server

<!-- sec-pass: copilot key storage on the server (machine key) -->
Set `NODEVAS_COPILOT_API_KEY` (or `NODEVAS_COPILOT_API_KEY_FILE`), `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in `serve`'s environment, or have an administrator store one with `PUT /api/copilot/settings` (`{"apiKey": "…"}`), which keeps it encrypted under the machine key, never in the database or `nodevas.yaml`. Anyone who breaks into the server can take a key kept there. An OpenAI-compatible server that needs no key (Ollama or vLLM on another machine) takes `provider: openai` and its `base_url` instead.

### Key on the runner (`keySource`)

**API key lives on**, in the same Settings section, decides where the model calls run: **Each person's runner (recommended)**, **Automatic** (the default: the server's key when it has one, otherwise each person's runner) or **This server**. Only an administrator changes it. With **Each person's runner**, only the runner's model answers: the browser's model and a local model on the server are not used. With the runner, the turn, the tools and the permission checks stay on the server; each model call goes to the speaker's own runner, which makes it with a key kept on that PC:

```sh
nodevas runner set copilotProvider anthropic   # or openai
nodevas runner set copilotModel <model>        # optional; the provider's default otherwise
nodevas runner copilot-key                     # the key on stdin, never a flag
```

Then restart the runner. `NODEVAS_COPILOT_API_KEY` in the runner's environment works instead of a stored key. `copilotBaseURL` points at an OpenAI-compatible server (Ollama and LM Studio need no key) or a proxy; it must be https, or http to this machine or the local network. With no `copilotProvider`, the runner offers a local Ollama or LM Studio it finds; `nodevas runner set copilotProvider off` stops that.

<!-- sec-pass: runner-held copilot key guarantees -->
The key never reaches the server: the runner names its provider and model, never the key. It refuses a call that names another provider, base URL or model, or that is for anyone but its owner. A server that is broken into can still spend the owner's quota, within limits: two calls at once, 40 a minute, 64 000 output tokens and ten minutes per call, each logged. While the runner is offline, the button stays and says how to start it.

## Permission modes

Reads always run; whatever changes something follows the conversation's mode. Pick it in the panel; `Shift+Tab` cycles.

| Mode | `read` | `write` | `risky` |
| --- | --- | --- | --- |
| **Plan** (`plan`) | run | refused; it proposes the change instead | refused |
| **Ask before edits** (`default`) | run | ask | ask |
| **Accept edits** (`acceptEdits`) | run | run | ask |
| **Bypass permissions** (`bypassPermissions`) | run | run | run |

Writes are ordinary edits: creating and linking nodes, changing a node's fields or status, claiming or releasing a task. Replacing a page wholesale, or any tool not classed as read or write, is risky. An ask is a card in the transcript: **Allow**, **Allow for this conversation** or **Deny** (keys `1`, `2`, `3`), or say "yes" or "no" ("確認" or "取消" in Chinese); after a spoken request it listens for the answer. No answer within 120 seconds is a deny.

<!-- sec-pass: copilot bypassPermissions default -->
`bypassPermissions` is offered only when the server allows it (`copilot.allow_bypass`; unset, it is allowed on a loopback-only server and refused with accounts). A bypass request the server does not allow runs as `acceptEdits`.

<!-- sec-pass: no bypass with the browser's model -->
With the browser's built-in model, bypass is never offered and a turn that asks for it is refused: node content can carry prompt injection, and a small model is the easiest to mislead.

## Planning by voice

Describe something to build. The copilot restates it and, once you confirm, files it as a goal node and starts a plan run on your runner: an agent reads the repository and adds task nodes under the goal, changing no files. It takes your first online runner with a repository and the first agent installed there (Claude Code, Codex, OpenCode, Gemini CLI, Antigravity) with its default model, unless you name others. Meanwhile the goal's **Autopilot** pill reads **Breaking down…**. Steps that dispatch work ask you every time, bypass included, and never offer **Allow for this conversation**; plan mode refuses them. Your runner still decides whether to take each run.

You need not ask how it went. When the plan run ends, the copilot writes the outcome and the tasks into the conversation by itself and asks whether to start the autopilot: by default 2 tasks at a time, up to 2 review rounds, and you approve every pull request before it merges. If that conversation is not open, the **Voice Copilot** button shows a dot (a click opens it) and the browser notifies you when allowed. On a yes it starts the autopilot and tells you who implements: the agents you named, else the advisor your runner offers (below), else your routing rules, whose last resort is the first installed agent's default model, often the most expensive.

### Revising a goal mid-way

Tell the copilot how the requirement changes. Once you confirm, it pauses the goal's autopilot (running tasks carry on; only whoever started that autopilot can revise the goal), records the change on the goal under a dated **Revisions** heading, and starts a replan run; the pill reads **Revising…**. Done tasks stay, pending ones may be changed or skipped (a skipped task keeps its history), ones in progress are only flagged, and new ones are added. When it ends, the copilot tells you what was kept, changed, skipped and added and asks whether to resume; nothing resumes until you say yes or press **Resume with revised tasks** in the goal's Autopilot panel.

## The autopilot's advisor model

With **Choose per task** in the goal's Autopilot panel, **Chosen by** picks each task's implementer: **Routing rules**, **A fast model on your runner** or **System One (Laya / Jev)**. An autopilot the copilot starts without named implementers uses what your runner offers, System One first. An advisor picks only among the agents and models your runners report; on an error, an offline runner or no answer within 6 seconds (10 for System One), the routing rules decide. The fast model, on the runner:

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

<!-- sec-pass: copilot.allow_bypass in the workspace nodevas.yaml -->
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

`NODEVAS_COPILOT_PROVIDER`, `NODEVAS_COPILOT_MODEL`, `NODEVAS_COPILOT_BASE_URL` and `NODEVAS_COPILOT_ENABLED` override these keys, and `nodevas serve --copilot=false` turns the copilot off. What an administrator saves in Settings or with `PUT /api/copilot/settings` overrides the file without a restart.
