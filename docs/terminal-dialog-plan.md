# Themed terminal dialog, interactive by default, picture-card pickers, multi-repo global agents

Work order, revised 2026-11-18. Nothing here is built yet.

- **Base.** Branch `feat/terminal-dialog` = master `e22700e` (Copilot drafts #13, agent mail #16 and Easy-start follow-ups #17 merged) plus this file. Every `file:line` below was checked on that tree.
- **First draft.** Written 2026-11-17 against `21169dd6`, merged from the reuse-first design with grafts from user-first (IME guard, the `user` event at seq 0, queued replies, the one-follow-up-per-run index, the resume fast-fail retry, the closing-question prompt, subscribe-in-view, the larger box with "Open large", the polite announcer, the text-to-picture table). §19 lists the 28 corrections and 13 stale items folded in since.
- **Decisions.** The owner's answers are final and recorded in §18. §C (coordination) is designed and included.
- **Delete this file** (and `docs/handoff-2026-11-17.md`) before the PR merges: shipped plans are not kept in the repo.
- **Superseded in the handoff.** `docs/handoff-2026-11-17.md:10` still says "Plan only" for Copilot drafts (merged as #13). Its §7 rule "the single-user path never needs a terminal" is retired by answer 1 (Simple is interactive too, inside a plain-word frame, §8.2).

## Owner's words (the requirements, verbatim)

- Story room dialogue box: 「我希望對話框是一個 terminal 的 dialog 只是經過外觀美化」; typing into it: 「能打字（像真的 terminal）」; content: 「agent 說的話＋工具一行摘要」.
- 「應該要是預設就是互動式的才對」, 「選取 agent 模型時應該要有圖卡才對」.
- 「這個主題包的目標就是把圖片代替繁雜文本」: with a theme pack active, art replaces text clutter (§11).
- Global agents: 「全域 agent 的目的就是處理統一的問題 比如微服務版本升級 cicd 調整 漏洞修復等等」 (§10).
- Coordination: 「三層處理沒問題」. Designed in §C.

## M. Agent mail finding: fixed by #16

Runs could not `list_team` or `save_attachment`. PR #16 (`7cafe81`) added both to `RunTools` (`internal/mcp/toolset.go:64-65`) and to the built-in roles that have `check_inbox` (`internal/engine/roles.go`). Nothing is left to do here. §C's agent mail (C2) and coordinated toolset (C3r) build on it.

---

## 0. Decisions

1. **Edition first.** Single-user (built-in runner, actor `local`) gets every feature. The team hub runs the same code with every existing gate unchanged (§1).
2. **One dialog component, `RunDialog`.** It is used in the story room's dialogue box, the Simple describe box and the Station global list.
   - Background run: a transcript of the run's stored events. The agent's text is shown in full. Each tool call and its result form one plain line (`› 執行 npm test`) that expands to the raw input and output.
   - Interactive run: the existing xterm `RunTerminal`, coloured by the theme, plus one input line and a key row.
   - In Simple mode the screen sits inside a plain-word frame (§8.2).
3. **Interactive (PTY) is the default, Simple included** (answer 1; 「應該要是預設就是互動式的」).
   - It is the only path today in which a person can answer a global agent's questions and permission prompts: global runs have no board, no `ask_user` and no relay, and their asks list is always empty (`httpapi/runs/global.go:219-226`).
   - Its known costs are handled, not avoided:
     - "完成，保留成果" (End) asks the CLI to quit, waits, then stops it, and publishes the work (§7.1).
     - A 20-minute quiet timeout ends an idle session the same way, for everyone (answer 2, §7.2).
     - The view-only state now reaches the browser (§7.3).
     - For Claude, the session's own transcript file is tailed into structured events (package R3), so Claude interactive runs also get step lines and history that lasts.
   - Background is used where interactive cannot work, or would hit a known blocker (§8).
4. **Interactive opens no window.** The agent's TUI runs in a hidden PTY on the runner (`internal/runner/interactive.go:119-262`, ConPTY on Windows), and its bytes are relayed to the in-app xterm. The i18n text `terminal.modeNoteLocal` / `terminal.modeNote` ("opens … on this computer") is wrong and is rewritten.
5. **Follow-ups continue the same work.** Typing after a run ends starts a new run, linked by `continue_of`:
   - same runner;
   - same worktree folder, reused in place (`<dataDir>/worktrees/<workRoot>`);
   - same branch;
   - when the work has an open PR, the turn pushes to that branch so the PR updates, under the rules of §6.5 (answer 5);
   - the CLI's own resume where it exists (flags checked on the installed CLIs, §6.6); otherwise a carry-over prompt.
   - The hub only names run ids. The runner resolves folder, branch, HEAD and session from its own `work.json` and git.
6. **Queued reply (graft).** A line typed while a *background* run is still running becomes a queued follow-up. It starts when that run ends, and it can be withdrawn.
7. **Picture cards replace the agent, model, folder and mode dropdowns.**
   - Art comes from `figureFor(manifest, {agent, model}, "idle")`, frame 0, drawn as an `<img onError>` cropped to `scene.box`, as `Face` and `DraftPresenter` already do (§9).
   - Folder cards use the pack's `rooms` art, picked by a stable hash of the repo id; a plain folder card otherwise (answer 4).
   - With no pack, no match or a broken image, a plain card shows `agentName` plus initials on `station-avatar agent-<kind>`. No logos.
   - Radio-group semantics (or native checkboxes for folders); works at phone width.
8. **Multi-repo default = B, fan-out.**
   - One ordinary global run per repo, tied together by a `group_id`. At most 30 repos; over budget, the whole group is refused (answer 3).
   - Groups run in the background. Talking to a member happens through follow-ups once it has ended.
   - The story room shows a group as one figure with member tabs (built now, §10.2).
   - Shape A (one session over N repos) is designed but deferred, because of its cost (§10.4).
9. **No theme-manifest change.** The terminal look comes only from the `--story-*` CSS variables, so the Go / TS / docs / `doc_test` chain is not touched. A runner is shown by a fixed icon, not a pack picture: manifest places are text only (`web/src/theme-pack/themeManifest.ts:86-87, 144`).
10. **Coordination (§C).** First come, first served; same files caught early; deliberate contests (比稿). The person always decides.

---

## 1. Editions

| | Single-user (built-in runner) | Team hub |
|---|---|---|
| Who starts a global run or group | the local person | the caller, **on their own runner only** (`runs/global.go:96-99`); all repos on that one runner; never across runners |
| Offer acceptance | `askBuiltIn` (`jobs.go:606-640`) starts it at once: `BuiltInDecision` lets an `Origin=person` run through (`builtin.go:79-100`) | unchanged: console y/N, auto-accept policy, or app ask. Each group member and each follow-up is a separate offer; there is no hub-side "accept all" |
| Typing into a PTY | always on (`jobs.go:635-637`) | hub `mayType` (the dispatcher or the runner owner; never an agent; an admin only when they are one of those two, `httpapi/runs/terminal.go:65-69`) **and** runner `remoteTyping` (`AllowRemoteTyping` or a console yes). New: the view-only state is shown in the browser; it is not widened |
| Permission prompts | `local` answers them all | the runner owner only (`MayAnswer`); runScope tool/command/once unchanged; truncated input means once |
| Follow-up, queued reply | the local person | global: `actor == prior.CreatedBy == runner.OwnerID`. Node follow-ups are not in v1 |
| End | the local person | the `mayType` set; on a **view-only** run, the runner owner only (§7.1) |
| Default mode | interactive, by the §8 rule | interactive only on the person's own runner when its policy allows typing; background on a teammate's runner |
| Simple describe box | shown | hidden, as today (`SimpleAgentBox.tsx:72-73`) |
| Busy, overlap, contests | every holder is yours | another member's run is a count, or one bit (§C.1) |

---

## 2. User flow

1. **Station > 全域 agent.**
   - Runner card (only when there are several), with a fixed runner icon.
   - Agent cards (character still).
   - Model cards.
   - Folder cards (pack room art or plain), multi-select with All / None.
   - Mode cards: 互動 shows the character's `speak` still; 背景 shows its `write` still. Pre-picked by §8.
   - Task box, then Start. A busy folder shows the §C choice first.
2. **One folder, interactive.** The run's dialog opens under its list row and in the story room.
   - Header: character plate, model, one flavour line (typewriter on this line only).
   - Body: the themed xterm.
   - Footer: input line plus key row (Enter, Esc, 1/2/3, Up, Down, from `keySequence`), with "完成，保留成果" and "停止".
   - Permission prompts and questions appear on screen. The person answers them on the screen or with the key row.
3. **It ends** (End, quiet timeout, the person quits the TUI, or the agent exits). The work is published as today: commit, keep the branch, open a PR if asked.
   - The screen stays for 10 minutes (`DefaultTermKeep`). After that the body shows the summary's last lines, or Claude's steps (R3). Simple shows #17's `SimpleRunSummary` (`web/src/runs/SimpleRunSummary.tsx`).
   - Typing "按鈕再大一點" starts a follow-up in the same folder and branch. If the work has an open PR, the PR gets the new commits and one comment for the turn (§6.5).
   - The dialog shows each run as a "turn", headed `› 你：…` from the `user` event.
4. **Background run.** The body is the live transcript. The input line says "它做完就會看到". Sending creates a queued follow-up, shown as a pending "你：" line with "收回" (withdraw).
5. **Several folders.** A group row with one folder card per repo, each with that member's pose still and status. Opening one shows that repo's dialog. Group buttons: 全部停止, 套用全部已完成的, 告訴全部 / 告訴失敗的, 立即繼續 (after a usage-limit pause).
6. **Simple describe box.** The same dialog renders inline in place of `StartedLine` (`SimpleAgentBox.tsx:217, 315`), inside the plain-word frame (§8.2). If the folder is not trusted, the run starts in the background instead and the box says why in plain words. A signed-out agent starts nothing in any mode (the runner declines it, `jobs.go:355-357`), so the box shows #17's sign-in help instead of a start (`SimpleAgentBox.tsx:164-179`, `result.signedOut`).

---

## 3. The dialog

### 3.1 Components (web)
- `web/src/runs/toolLine.ts`: pure. Turns a tool call (and its result, if any) into `{verb, target, state}` (§5).
- `web/src/runs/dialog/runLines.ts`: pure. Turns events + asks + the turn chain into lines.
  - Merges `tool_call` and `tool_result` by `toolId`.
  - Interleaves asks by time, but pins pending asks to the bottom (runner and hub clocks can disagree).
- `web/src/runs/dialog/RunTranscript.tsx`: `role="log"`, no animation.
  - Renders the last 200 lines, with "顯示較早內容" for the rest.
  - Follows the end while the reader is at the bottom (the `RunTimeline` logic) and shows "jump to latest" otherwise.
  - A visually-hidden `aria-live="polite"` region announces new agent `text` only: the first 200 characters, at most once every 5 s.
- `web/src/runs/dialog/RunComposer.tsx`: a real `<textarea>`, so the StoryRoom key handler skips it (`StoryRoom.tsx:291-304`).
  - Enter sends; Shift+Enter adds a newline.
  - **Enter is ignored while `isComposing` or `keyCode === 229`** (zh-TW IME).
  - 16 px font on phones (prevents iOS zoom). Sticky at the bottom. Its Send and End buttons keep clear of the copilot button: they follow the `--copilot-fab-clearance` contract (`styles/15-copilot.css:60-96`, `web/src/copilot/copilotFabClearance.test.ts`) and the agent-fab movement (`styles/16-agents.css:757-768`).
  - Its state comes from the §4 table.
  - **Sending into a TUI (interactive).** `conn.sendInput` returns false while read-only, closed or final (`api/terminal.ts:169-176`). Ink and ratatui TUIs read a fast burst as a paste, so a trailing CR in the same write can become a newline. So:
    - when the TUI has turned on bracketed paste (xterm's `modes.bracketedPasteMode`), the text goes as `ESC[200~ text ESC[201~`;
    - the CR is sent as its own write, 150 ms later;
    - if any send returns false, the composer keeps the text and shows the reason.
    - Letters, digits or Enter can pick an option in a permission menu. Bracketed paste keeps letters and digits from acting as keys; the composer also shows `runDialog.ph.screenAsks` ("The screen is asking something: answer it there") when the screen's cursor line matches a menu marker found by the smoke test (§16 Build gate). The live smoke test on each CLI decides the exact rule before W1 merges.
  - `prefill(text)` handle, used by §C's "Tell it".
- `web/src/runs/dialog/RunDialog.tsx`: `{runId, variant: "story"|"plain", simple?}`.
  - Header slot (plate and flavour line, supplied by the caller), body (transcript or screen, with Screen/Steps tabs when both exist), composer, and End/Stop.
  - `simple` wraps the screen in the plain-word frame (§8.2).
  - "放大" opens the same dialog in a `GameDialog`. Two fixes are needed for that:
    - `GameDialog` swallows Escape in a capture-phase window listener (`web/src/game/GameActions.tsx:328-334`), so Esc would never reach the TUI. It now skips Escape when the event target is inside `.xterm`.
    - It is portaled to `body` (`GameActions.tsx:341`), where there are no `--story-*` variables. The dialog passes them as an inline style on its own root.
- `web/src/runs/dialog/useRunDialog.ts`:
  - Loads the chain (`GET …/thread`).
  - Calls `watchRun` for the turn in view (ref-counted), and caps the history it fetches.
  - Derives the composer state.
- `web/src/runs/dialog/liveTerminals.ts`: **one live xterm per run per page.** Several mounts of one run (SimpleAgentBox plus the story room, or a list row plus "Open large") would each open a socket, and the hub applies the last resize from anyone, so the PTY size would thrash. The first mount owns the xterm; the others show "shown above" with a jump link.
- `web/src/runs/terminalKeys.ts` (new): `keySequence` and `terminalTheme` move here from `RunTerminal.tsx`, so `RunDialog` can use them without pulling xterm into the main bundle.
- `RunTerminal.tsx` gains:
  - a container-theme option: `terminalTheme(el)` reads `--story-night`, `--story-ink`, `--story-soft` and `--story-accent` when it is inside `.px-story-talk`;
  - an `onConnection(conn)` prop, so the composer sends through the **same** socket.
- `runStore.ts`: a chain helper, and `eventsByRun` is evicted on the last unwatch (today each `watchRun` keeps up to 10k events for good).

### 3.2 Mount points
- **StoryTalk `TalkBox`** (`StoryTalk.tsx:194-244`): for an actor with a run row, `<RunDialog variant="story">` replaces `StoryLine` + `px-story-meta` (:227-229).
  - Narrator, visitor and session actors keep today's box.
  - The plate stays. One flavour `StoryLine` goes into the header.
  - The facts become pictures (§11).
  - Choices (`ActorItems`/`RunItems`) stay unchanged.
- **SimpleAgentBox:** inline, in place of `StartedLine`, with `simple`.
- **GlobalAgentList:** inside the expanded row. Only one row is expanded at a time, so there is at most one xterm and one `watchRun`.

### 3.3 Subscriptions
- Story **list** layout: only boxes in view are subscribed (IntersectionObserver, at most 6). The rest show the flavour line, plus the last `text` event if it is already loaded.
- Story **stage** layout: only the focused member.
- GlobalAgentList: only the expanded row.

### 3.4 Sizing and motion
- The 166 px cap is on `.px-story-line` (`warroom.css:2178-2180`), not on `.px-story-said`. It stays for the flavour line.
- The story world is `clamp(540px, 100vh - 200px, 880px)` and `.run-term-host` is `clamp(260px, 56vh, 560px)` (`styles/19-run-terminal.css:155-157`). Full size inside the stage would push the figures out on a laptop. So:
  - inside `.px-story-talk.has-run`, the terminal host is compact: `clamp(180px, 34vh, 320px)`, and the transcript body `max-height: min(40vh, 360px)`;
  - "Open large" gives the full size.
- The `@container` rules at 760, 860 and 520 px stack the composer under the body.
- Typewriter: header flavour line only. `cast.still` (reduced motion, hidden tab) is honoured. Transcript and screen never animate.

### 3.5 Where the canned theme lines go
- Header flavour line: always.
- Body empty state, before the first event (queued or offered): the canned line plus "還沒有輸出".
- Failed: the flavour line stays in the header; the run error becomes an `error` row (`role=alert`) in the body.
- An ask: an inline `AskCard` row (node runs). It is never the box's main text.
- `actor.bubble` (agent mail) shows only when the actor has no run row.

### 3.6 Body by event kind
| Kind | Rendering |
|---|---|
| `user` (new) | `你：…`, accent colour, the start of a turn |
| `text` | full, pre-wrap, code fences in monospace. At `MaxEventText`, append `runDialog.cut` |
| `tool_call` + `tool_result` | one line `› {verb} {target}` with a status mark (running `…`, ok, failed `✗`). Expands to the pretty-printed input and the result text |
| `thinking` | one folded line `思考` |
| `error` | in full, `role=alert` |
| `log` | hidden behind "顯示 runner 紀錄", except `hub:` notes and a non-zero "exited with code N" (dimmed). An End's `-1` exit is not shown (§7.1) |
| `usage` | hidden (tokens appear as the energy bar, §11) |
| unknown | dimmed log line, as `RunTimeline` already does |

---

## 4. Typed input by run state

| Run state | Composer | Enter does | Guard |
|---|---|---|---|
| queued / offered | disabled: "等待 runner 開始" (team: "等待 {who} 在那台電腦上同意"); Stop shown. Waiting behind another run (§C): "等前一個工作結束" | — | — |
| running, interactive, viewer may type | enabled; raw keys also go straight to xterm | the §3.1 send on the same terminal socket (bracketed paste when on, then CR after 150 ms) | hub `mayType` + runner `remoteTyping`; socket audit, 64 KiB/s bucket, Origin/Sec-Fetch checks, `StillAuthorized`, 64 KiB cap |
| running, interactive, read-only (hub) or view-only (runner) | disabled, with the reason (`ph.notYours` / `ph.viewOnly`) | — | `view_only` (§7.3) |
| running, background, node run, pending question | becomes the answer field of the inline `AskCard` | `answerAsk {decision:"answer", answer}` | `canAnswerAsk`, project scope, `POST /api/agents/asks/:id/answer` |
| running, background, pending permission / approval | `AskCard` buttons (number keys, runScope tool/command/once); composer disabled | — | `MayAnswer`: runner owner only; truncated input means once |
| running, background, global | enabled: "它做完就會看到" | `POST …/follow-up` creates a **queued** follow-up that Pump holds until the prior run is final; shown as a pending "你：" line with "收回" | §6.3 checks; "收回" cancels the queued run |
| ended (succeeded / failed / cancelled / usage limit), latest turn, worktree kept or never needed | enabled: "還想改什麼？" | `POST …/follow-up` | §6.3 checks |
| same, and the work has an **open PR** | enabled: "還想改什麼？（會更新它的 pull request）" (`ph.followUpPr`; Simple never shows a PR) | `POST …/follow-up`; the turn pushes to the PR's branch when it succeeds (§6.5) | §6.3 checks, including the PR checks |
| ended, usage limit, before `reset_at` | enabled: "{time} 後可再繼續" | queued follow-up with `RetryAt = reset_at` | same |
| merge approval pending on this work | disabled: "正在合併，完成後再接續" (`ph.merging`) | — | `MERGE_PENDING` |
| merged_local / discarded / PR merged or closed | button "用這句開始新工作" | an ordinary new global run: same repo, agent and model, from base | normal start checks |
| a later turn exists | read-only: "接續在下方" | the dialog scrolls to the latest turn | — |
| viewer is not the dispatcher / owner | read-only: "只有 {name} 能回覆" | — | — |
| node run, ended | disabled, with a link to "再派一次" (Dispatch). No node follow-up in v1 | — | — |

**Cancelled prior.** The worktree may hold uncommitted changes (`jobs.go:514-518`). The follow-up may start on that dirty folder only if the runner's own record says the prior run was cancelled or failed.

Every disabled state that Simple can reach has a `runDialog.simple.*` variant in plain words (§15), including waiting behind another run (`ph.waitingBehind`) and a later turn existing (`continuedBelow`). The team-hub-only states (`ph.waitingOwner`, `ph.notYours`) and node runs (`ph.nodeRun`) never appear in Simple, which is single-user and global-only.

---

## 5. Plain one-line tool summary (`web/src/runs/toolLine.ts`)

**Normalise.** Lower-case the name. `mcp__<srv>__<tool>` gives a server and a tool. OpenCode's `nodevas_x` is already mapped to `mcp__nodevas__x`. Gemini stores board tools bare (`claim_task`, `gemini.go:292`), so the bare board names are aliases of `mcp__nodevas__*`.

**Rule order is fixed and tested:** `mcp__` names are matched first, so `mcp__terminal__x` reads as an mcp tool, not as "run".

| Verb | Names (first match wins) | Key | Target |
|---|---|---|---|
| mcp | `mcp__s__t`, and the bare board names `claim_task`, `get_node`, `set_node_status`, `release_task`, `check_inbox`, `send_message` | `.mcp` 使用 {server} 的 {tool}; `nodevas` → `.board` 看板; `claim_task` → 接下任務, `get_node` → 讀取任務, `set_node_status` → 回報狀態 | — |
| run | bash, shell, run_shell_command, run_command, command, terminal, exec_command, local_shell; any other name containing `command` or `terminal` | `runDialog.tool.run` 執行 {target} | `command`, `cmd`, `CommandLine`, `commandLine`. An array `["bash","-lc",X]` gives X; any other array is joined with spaces. A codex `shell` input is a plain string |
| write | write, write_file, create_file | `.write` 寫入 {target} | file key |
| edit | edit, multiedit, replace, replace_file_content, str_replace*, patch, notebookedit | `.edit` 修改 {target} | file key |
| patch (codex) | apply_patch: input is lines of `<kind> <path>` | 1 path: `.edit`; several: `.files` "{first} 等 {count} 個檔案" | paths |
| read | read, read_file, view_file, view, notebookread, cat | `.read` 讀取 {target} | file key |
| search | grep, glob, search*, find, ls, list*, codebase_search, find_by_name | `.search` 搜尋 {target} | `pattern`, `query`, `Query`, `glob`, `regex`, `path` |
| web | webfetch, web_fetch, fetch, websearch, web_search, google_web_search | `.web` 查詢網路 {target} | `url`, `Url`, `query`. A codex `web_search` input is a plain string |
| todo | todowrite, write_todos, update_plan, plan; any name containing `todo` | `.todo` 更新待辦清單 | — |
| agent | task, subagent, agent | `.agent` 派出助手：{target} | `description`, `prompt` (first line) |
| other | anything else | `.other` 使用 {tool} | — |

- **File keys, in order:** `file_path`, `filePath`, `path`, `Path`, `TargetFile`, `AbsolutePath`, `notebook_path`, `file`, `filename`.
- **Truncated JSON.** Input is capped at 16 KiB and may be cut mid-JSON. Fall back to the regex ``"(key)"\s*:\s*"((?:[^"\\]|\\.){1,300})``.
- **Display.**
  - A command: its first line, cut to 80 characters.
  - A path: relative to the worktree when the worktree prefix is present, otherwise its last two segments.
  - The full value is in the expanded view.
- **State.**
  - No result yet: running.
  - `isError` or codex `declined`: failed.
  - OpenCode sends call and result together, so its rows never show running.
  - Antigravity's early call stays running until DONE.
- **Rendering is web-only**, so every stored event (old ones included) renders without a wire change.
- **Tests use the names as stored**, not the fixture names. The `*_stream.jsonl` fixtures hold CLI-native names; the stored names come from the Go parsers (`codex.go:322-339` gives `shell`, `apply_patch`, `mcp__…`; `gemini.go:292` gives bare board names). `toolLine.test.ts` lists hand-normalised stored names per parser, each with a comment naming its parser line.

---

## 6. Continue (follow-up) mechanism

### 6.1 The work folder stays fixed
- `workRoot` is the id of the first run in the chain. The folder is `<dataDir>/worktrees/<workRoot>`, and its branch is the root's `agent/<slug>-<root8>`.
- In-place reuse keeps the cwd the same. Claude and Gemini key their sessions by cwd, and codex filters by cwd unless `--all` is given.

### 6.2 Wire (`internal/runnerwire`)

**`runs.go`**
- `EventUser = "user"`.
- `Job` gains `ContinueOf`, `WorkRoot`, `ContinueTask` (the prior run's task), `ContinueSummary` (the prior summary, already redacted, ≤ 4 KiB) and `ContinuePR` (the PR URL the hub knows for the work, or ""). It gains no group field: a group member's siblings reach it only through the prompt (§10.2).
- `Run` gains `continueOf`, `workRoot`, `continuedBy`, `viewOnly`, `groupId`, `resetAt`, `endReason` and `pushRefused`. Since #13 the `Run` already carries `draft`, and since #17 `failureCode`.
- `Accept` gains `ViewOnly bool`.
- `RunResult` gains `EndReason` (`""`, `ended`, `quiet`, `quiet_waiting`; §7) and `PushRefused bool`: a good turn whose fast-forward push to its open PR's branch was rejected (§6.5). It is stored as `runs.push_refused` and shown with `runDialog.err.branchMoved` on that turn.
- Decline codes `DeclinePREnded = "pr_ended"`, `DeclinePRMoved = "pr_moved"`, `DeclineBranchMoved = "branch_moved"`. F1 also adds them to the hub's `CleanDeclineCode` list (`internal/runs/offerask.go:44-53`), which otherwise stores any unknown code as `other`.

**`wire.go`**
- `Agent.Interactive bool` and `Agent.Untrusted bool` in the hello. `Agent.SignedOut` already exists (`wire.go:211-214`, #17).
- `TypeEnd = "end"`: an envelope with `RunID`.

**`global.go`**
- `CapContinue = "continue"`: covers follow-up, End, view-only, the work registry and the PR-update rules. The runner advertises it only once R1 and R2 have both merged: J1 adds it to `capabilities()` (`internal/runner/interactive.go:50`, §16.4).
- `FollowUpRequest {message, mode?}`.
- `MaxFollowUpBytes = MaxGlobalTaskBytes`.
- Group request and response types; `MaxGroupRepos = 30`.

**`workspace.go`**
- `WorkspaceGateContinued = "continued"`.
- `WorkspaceActionJob.WorkRoot`.

**`terminal.go`:** doc comment fix (no window is opened). The End message carries `NoPR` for §C's node take-over.

There is no new realtime name. Group and contest changes ride the existing `global-runs-changed` (dispatcher and owner only) and `runs-changed`, because every group or contest change comes with a member's run change. This keeps `web/src/App.tsx` (:379) and `web/src/api/ws.ts` (:89) unchanged.

### 6.3 Hub: `Dispatcher.FollowUp` (`internal/runs/followup.go`, new)

Checks, in order. Each refusal is a `*StartError` with a code and a field.

1. The actor is a person (`personOnly`, not an agent) and can see the prior run (`canSee`).
2. The prior run is global (v1 scope).
3. `actor == prior.CreatedBy == runner.OwnerID`.
4. Prior state:
   - final: start normally;
   - queued / offered / running and background: allowed as a *queued reply*, held by Pump;
   - running and interactive: refused with `RUN_LIVE` (the person should type into the screen instead). §C's `continue` passes `QueuedBehind: true`, which skips only this check. G1 owns the `QueuedBehind` field, its skip and its test; C1 only passes it.
5. The prior is the latest turn: no non-withdrawn run has `continue_of = prior`. Otherwise 409 `CONTINUED {latestId}`. **Withdrawn** means a run that never started: `status IN ('cancelled', 'declined') AND started_at = ''`. So a follow-up the person took back, and one the runner declined (`pr_moved`, `branch_moved`, a console No, a signed-out agent), both free the slot. A withdrawn run is never a prior itself: a follow-up that names one gets 409 `CONTINUED {latestId}` with the latest non-withdrawn turn, which is where the composer sits.
6. No other unfinished run in the same `work_root`, apart from the prior when this is a queued reply.
7. The work is still open:
   - the prior workspace is not `merged_local` or `discarded` (`WORK_GONE`);
   - `PRState` is not merged or closed (`PR_ENDED`; the UI offers "start new work");
   - no merge approval is pending on any turn of the work (`MERGE_PENDING`).
   - An **open** PR is allowed (answer 5). The runner re-checks it on disk before the turn (§6.5).
8. Message: trimmed, valid UTF-8, no NUL, ≤ `MaxFollowUpBytes`.
9. The runner has `CapContinue` (else `RUNNER_OUTDATED`). Interactive mode also needs `ModeSupported` and the §8 rule.
10. `BudgetStatus(owner)` has room.

**Create.** `New{Global, NodeTitle: title(message), BranchSlug, NoPR, RepoID, Base: prior's, Agent/Model: prior's, Mode: request or §8 default, ContinueOf: prior.ID, WorkRoot: prior.WorkRoot || prior.ID, GroupID: prior.GroupID, Origin: person}`. `NoPR` is the prior's. A prior that stopped at a usage limit with `reset_at` in the future sets `RetryAt = reset_at`. Global runs never wait for a limit on their own (`runs/global.go:21-22`, `dispatcher_routing.go:49-51`), so this is the only wait they get.

**Then:**
- Write the message as event **seq 0, type `user`** (`Store.UserFirst`, the `NoteFirst` pattern, `runs/routing.go:460-467`, `ON CONFLICT DO NOTHING`). This is also done for every person-started global run and group member, so each turn opens with "你：". A seq-0 `user` event and a stage-override note would collide, and one would be dropped silently. That cannot happen while follow-ups are global-only (global runs have no stage override); `TestUserFirstSeqZero` pins the global-only use.
- Add `d.overlapNote(ctx, prior)` (§C.3.3) to the prompt section.
- Audit `run.global.followup {prior, root, mode, messageBytes}`. The text itself is never audited.
- `Submit`.

**Store and gates:**
- `Store.Create` accepts `continue_of` and `work_root` only on global rows.
- `WorkspaceGate` returns `continued` for any run that has a non-withdrawn successor (withdrawn as in check 5, so a declined follow-up leaves the real latest turn mergeable), so merge, discard and apply act on the latest turn only. This is derived, so the `runs.workspace` CHECK (`postgres.go:1122-1126`) is not changed. A merge approval through an older row also fails its head check.
- `WorkspaceInUse` also matches `work_root`.
- **Merge approval** (`Dispatcher.Merge`, `runs/dispatcher_merge.go:16`) is refused while any turn of the work root is unfinished (`TURN_RUNNING`). This is "block a remote merge while a turn is unfinished".
- **Pump** (`dispatcher.go:306-321`, one added skip, done in F1): skip a queued run whose `continue_of` prior is not final.
- **Withdraw** = the ordinary cancel route on the queued follow-up. A follow-up cancelled or declined before it started (`started_at = ''`; it is stamped on entering running, `store.go:772`; a decline ends as `declined`, `dispatcher.go:439`) counts as withdrawn. One predicate is used everywhere: the unique index (§13), the latest-turn check, `Store.Successor` and `WorkspaceGate`.

### 6.4 Runner (`internal/runner/work.go`, new)

**Registry.**
- `<dataDir>/work.json`, mode 0600, the same pattern as `handoffs.json`.
- One entry per root: `{root, repoID, branch, head, pushedHead, base, agent, session, prURL, lastRunID, lastStatus, at}`.
- Written at every finish of a global run, including roots whose folder was dropped because nothing changed.
- Kept 30 days, at most 500 entries.

**Offer with `ContinueOf`.**
- `checkGlobal` allows `ContinueOf` and `WorkRoot` (both match the run-id regex). It still refuses `Branch` and `HandoffBranch`, so `global_test.go:51,225` keep their meaning.
- `prepareContinue(job)` runs before the accept ask, like `prepareHandoff`. Using only the runner's own record, it checks:
  - `entry.lastRunID == job.ContinueOf` and `entry.repoID == job.RepoID`;
  - the folder's git dir belongs to that root (the GitFile/GitDirs check) and its branch is `entry.branch` (`ownBranch`);
  - `HEAD == entry.head`;
  - the tree is clean, unless `entry.lastStatus` is cancelled or failed;
  - no active run on this runner uses that folder;
  - the PR checks of §6.5, when the entry has a PR.
- It tells two "folder gone" cases apart:
  - the folder was removed because the prior changed nothing (`publish.go:48-54`): it is re-created at the same path from base, with the root's branch name;
  - the folder was removed because the PR was merged or closed (`prwatch.go:239-246`): the offer is declined with `pr_ended`.
- `execute` uses that folder instead of `CreateWorktreeFrom` (`jobs.go:478`). `markNoBoard` and `guardGitDir` run unchanged. Publish follows §6.5, not the first-turn path.
- `describeOffer` (`runner/accept.go:188`) says "Follow-up of run X in the same folder" (and "updates PR …" when there is one), so a console y/N is informed.

**Run-id lookups re-keyed by root:**
- `findRunWorktree` (`workspace.go:54`)
- kept.json (`RecordKept`, `PruneKept`)
- the `watched` PR map (`publish.go:82-88`). A later turn never adds a second watch for the same worktree.
- `sharedWith`
- preview grant and `browseRun`
- workspace actions (`doWorkspaceAction`): the hub passes `WorkRoot`, and the runner checks `work.json lastRunID == job.RunID`. Earlier turns get gate `continued`.

**Session ids stay on the runner.** The hub never supplies a session id, ref or path.

### 6.5 Publishing a later turn, and updating an open PR (answer 5)

An unchanged `publish` would be wrong for a later turn:
- it pushes failed turns (`publish.go:70-75`), unlike revise (`review.go:572-576`);
- it reports cumulative changes and re-pushes a no-op turn (`changes.go:150-153, 187-190`);
- on a no-change turn with `BaseRef` = the prior head it deletes the root branch (`publish.go:50` → `changes.go:455-458`);
- it adds a second watch with the same worktree (`publish.go:82-88`).

`executeRevise` cannot be reused either: `ownBranchLocked` needs a NodeID (`review.go:144`). R1 writes `publishTurn` in `work.go`, keyed by the `work.json` entry, copying the revise rules (`review.go:184-223, 572-622`):

**Before the turn** (in `prepareContinue`), when the entry has a PR:
1. `gh pr view`: the PR is open, in the same repo, `headRefName == entry.branch`, and its base is `entry.base`. A merged or closed PR declines the offer (`pr_ended`): the person is offered a new run. A retargeted base declines it too (`pr_moved`), so no second PR is ever opened into an old base.
2. `git fetch origin <branch>` and compare `origin/<branch>` with `entry.pushedHead` (the last commit this runner pushed), not with `entry.head`. After a failed turn, HEAD is ahead of origin by the unpushed local commits, which is normal:
   - origin equals `entry.pushedHead`: go on (HEAD may be ahead with earlier failed turns' commits; the next good turn pushes them);
   - origin is ahead of `entry.pushedHead` (someone pushed to the PR), HEAD equals `entry.pushedHead` and the tree is clean: fast-forward the folder to origin and set `entry.head` and `entry.pushedHead` to it;
   - origin is ahead but HEAD has unpushed commits or the tree is dirty, or origin is anything else (rewritten, force-pushed, behind): decline (`branch_moved`). **Never force, never rebase.**

**After the turn:**
1. Commit as today. A failed, cancelled or usage-limited turn is committed locally and **never pushed**. The next good turn pushes it all.
2. A good turn with commits since `entry.pushedHead`:
   - `git push origin <branch>`, fast-forward only (no `--force`, no `--force-with-lease`). A rejected push (someone pushed during the turn) keeps the commits local and `entry.pushedHead` unchanged, and the turn still succeeds with `RunResult.PushRefused = true`; the hub stores `push_refused` and the dialog shows `runDialog.err.branchMoved` on that turn. The next follow-up then meets origin ahead with local commits and is declined `branch_moved` (step 2 above), so the person starts new work or settles the branch by hand. No PR comment is posted for an unpushed turn;
   - read the PR back (`attachPR`) instead of `gh pr create`;
   - **post one PR comment per pushed turn** (`gh pr comment`): "Turn {n}: {first line of the person's message}" plus the turn's summary (redacted, ≤ 2 KiB). The person's message is their own task text for their own PR, as on the first turn's PR body.
   - **never** `gh pr ready`: a draft PR stays draft (no auto-undraft).
3. A good turn with no new commits pushes nothing and comments nothing.
4. The turn's own changes are measured from the prior head, for the turn's report. The root's folder and branch are **never** dropped on a no-op turn.
5. `entry.head`, `entry.pushedHead` and `entry.prURL` are written back.

**PR watcher.** `prwatch.go:239-246` must not remove the folder on merged or closed while a turn of that root is unfinished; it removes it after the turn ends. **Runner-side merge** (`runner/merge.go`) refuses a merge job while a turn of the same root is active on this runner, as a second guard behind the hub's `TURN_RUNNING`.

**Costs, written down.** Every push re-runs CI and may dismiss approvals. The PR's title and body stay those of the first turn; the comments carry the later turns.

A first turn has no PR yet; its publish is unchanged. §C's `continue` choice calls `FollowUp`, so it inherits all of this.

### 6.6 Session resume (`adapter.Resumer`, optional)

| Agent | Session id source | Resume (flags checked on this Mac 2026-11-18; live smoke test still required) |
|---|---|---|
| claude 2.1.327 | headless: `system/init session_id`. Interactive: the runner picks a UUID and passes `--session-id <uuid>` (flag exists) | headless `-p --resume <id>` (prompt on stdin); TUI `claude --resume <id> <prompt>` |
| codex 0.149.1 | `thread_id` | `codex exec resume <id> -` (prompt on stdin). `exec resume` rejects `-s`/`--sandbox`, `-C` and `--color`, all of which `Codex.Command` passes today (`codex.go:63-70`). The resume argv drops all three: the sandbox goes as `-c sandbox_mode=…` and the folder comes from `cmd.Dir`. Interactive: no id is known, so the fallback is used |
| opencode run 1.19.4 | `sessionID` (`opencode.go:327`) | `opencode run --session <id>` (`-s` exists); TUI `--session` |
| opencode ACP | `s.session` (`acp.go:309`) | `session/load` when initialize advertises `loadSession`; otherwise a new session plus the fallback |
| antigravity | `conversation_id` (`antigravity.go:401`) | `--conversation <id>` (in `agy --help`) |
| gemini | `session_id` (`gemini.go:281-283`) | **no resume in v1** (answer 7; the CLI is not installed here). Carry-over prompt only, until a live smoke test passes |

- **Capture is new work.** Each parser today only writes the id into a log line's text (`claude.go:395`, `codex.go:206`, `gemini.go:201, 281-283`). A1 adds `SessionReporter{Session() string}` to every parser, Gemini included. The id is validated with `^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$` and stored only in `work.json`. It is passed in argv only, never in a shell string (the `.cmd` shim path keeps using `wrapBatch`).
- **Resume rules.** Resume only when the agent kind matches the entry's. A different model is allowed for claude, codex and opencode.
- **Fast-fail retry (graft).** If a resume exits non-zero with no `text` event within 30 s, the runner retries once with a fresh session and the fallback prompt, and logs "resume failed; continuing without the agent's memory".
- **Fallback prompt.** `adapter.ContinueSection`, built like `handoffSection`:
  > You are continuing your earlier work in this folder on branch X. Earlier task: … You reported: … Inspect `git log <base>..HEAD` and `git status`. The person now says: …
  When the work has a PR, it adds: "Your commits update pull request …; do not push, the runner does."
- **Prompt on resume.** Only the person's message plus a one-line rules reminder (global: "same worktree, do not push").
- **Global background prompt wording (graft)** (`adapter/global.go:20`). "Nobody can answer questions during this run" becomes: "Nobody can answer during this run: decide sensibly. If you need a decision, ask it as the last line of your final message; the person's reply continues this work."

### 6.7 Effect on usage limits
A global run that stops at a usage limit commits on its branch (`jobs.go:526-535`). Its reset time is now stored: `ResetAt` lived only on the in-memory `Record` (`runs/store.go:88-93, 835`), and F1 persists it as `runs.reset_at` (§13). A follow-up after `reset_at` continues the work. This fills in the handoff that global runs are missing.

---

## 7. End, quiet timeout, view-only

1. **End ("完成，保留成果").**
   - Routes: `POST /api/global-runs/:id/end` and `/api/runs/:id/end`.
   - Allowed for the `mayType` set (`httpapi/runs/terminal.go:67-69`: the dispatcher or the runner owner, never an agent). An admin has no extra right. **On a view-only run only the runner owner may End**, because the runner cannot tell who sent End, and otherwise a dispatcher could force a publish the owner blocked.
   - The hub sends `TypeEnd`. The runner:
     1. if the PTY is not open yet (`interactive.go:175-185`, before `terms.open`), sets a **pending-end** flag on the run and applies it as soon as the terminal opens (today such an End is lost, `interactive.go:270-272`);
     2. sends the CLI's own quit sequence (a per-agent table in `interactive.go`, fixed by the smoke test; until then Ctrl+C twice, 1 s apart);
     3. waits a 10 s grace period for the CLI to exit by itself;
     4. only then kills the process tree (`pty.go:86-91`, a hard kill).
   - It records `stopped="ended"`, `EndReason = ended`, and Status **succeeded**, and publishes normally. The `-1` exit code a kill leaves is not shown as a failure.
   - Cancel stays as it is (nothing is committed).
   - A runner without `CapContinue` gets 412.
2. **Quiet timeout: 20 minutes for everyone** (answer 2), Simple included.
   - A constant on the runner (`interactiveQuiet = 20 * time.Minute`). There is no setting and no "0 = off": the config convention reads `<= 0` as "the default" (`runner/config.go:319-326`), and the owner asked for one value for every mode.
   - The runner keeps a `lastInput` time beside the output time (`interactive.go:390-393`). No PTY output and no input for 20 minutes ends the session like End, with a log line.
   - **A quiet end while a prompt waits is reported differently.** If the screen's last lines match the CLI's known question or menu markers (the same smoke-test table as §3.1), `EndReason = quiet_waiting`, and the dialog says "It stopped while it was waiting for your answer". Otherwise `EndReason = quiet`, shown as "done, keep the work".
   - The 60-minute `runTimeoutMinutes` still applies (a failed timeout, as today).
   - If a TUI repaints while idle, the quiet timer never fires. The smoke test checks this per CLI; such a CLI's repaint is ignored by comparing screen content, not bytes.
3. **View-only.**
   - The runner sends `Accept.ViewOnly = !remoteTyping` on interactive runs.
   - The hub stores `runs.view_only`. `TermViewer.canType` is fixed at attach today, and the HTTP loop caches it (`runs/terminal.go:73-86`; `httpapi/runs/terminal.go:117, 179`). G1 makes the relay's type permission mutable, and on `view_only` sends `{"type":"readonly"}` to every viewer and stops relaying input.
   - The accept hook: `Dispatcher.Accept` (`dispatcher.go:408`) calls `d.acceptViewOnly(ctx, r, accept)`. F1 adds the call and a stub in `internal/runs/viewonly.go`; G1 fills the stub. So G1 never edits `dispatcher.go`, which G2 owns in the same wave.
   - The composer shows `ph.viewOnly`.
   - This replaces today's silent drop (`interactive.go:279-281`).

---

## 8. Interactive by default

### 8.1 The rule
`defaultDispatchMode(ctx)` in `RunModePicker.tsx` is pure and is the single source of the default. It gives **interactive** when all of these hold:
- the runner has `CapInteractive` (`runnerSupportsInteractive`);
- the chosen agent's hello entry has `interactive: true` (new per-agent flag, set from `adapter.Interactive`; all 5 kinds today);
- the agent is not `untrusted` (§8.2);
- the runner is the viewer's own, or the edition is single-user;
- auto-accept is off, or its policy `modes` include interactive **and** it has `allowRemoteTyping` (`runnerwire/remote.go:31-33`). Without typing, an auto-accepted interactive run starts view-only, nobody can answer its prompts, and it idles for 20 minutes. The value is already in the hello;
- for a node dispatch: not a staged Executor implement (`CodeStagedNeedsHeadless`, `start.go:289-294`, constant at `:467-469`), and no pipeline and no fallbacks (interactive skips handoff, so the fallbacks would never be used);
- one folder, not a group, not a contest.

Otherwise it gives **background**, with a one-line reason under the mode cards (`picker.mode.fallback.*`; Simple: `picker.simple.fallback.*`).

**Signed out is not a mode question.** The runner declines every job of a signed-out agent, in either mode (`runner/jobs.go:355-357`, `DeclineAgentSignedOut`; the hello's `signedOut`, since #17, `runner/detect.go:428-430`, Claude only today). So the rule never answers "background" for it: it cannot be started at all. A signed-out agent is not `installed` in the hello (`detect.go:428-433`), so `installedAgents` leaves it out and it never gets a card. Full mode's `AgentCards` adds one muted line under the cards for each signed-out agent (`picker.signedOut`), so the person sees why it is missing; Simple shows #17's sign-in help when no agent is left (`SimpleAgentBox.tsx:164-179`, `result.signedOut`).

**Always background:** autopilot, pipeline rounds, fallback and handoff runs, plan / judge / review / verify / compare (hub rules unchanged), a teammate's runner, group members and contest entries.

### 8.2 Simple: the agent's own screen inside a plain-word frame
Answer 1 makes Simple interactive too, so the CLI's own English (trust dialogs, permission menus, sign-in screens) appears in the xterm. The owner's answer (round 2): show the raw TUI inside a plain-word frame, and fall back to background for known blockers.

- **The frame** (`RunDialog simple`, W1):
  - above the screen, one plain line: "This is {agent}'s own screen. It may speak English. Type below, or use the buttons." (`runDialog.simple.frame`);
  - the key row and End use plain labels (`runDialog.simple.keys.*`, `runDialog.simple.done` "Done, keep what it made");
  - every composer state has a `runDialog.simple.ph.*` variant;
  - after the screen is gone, #17's `SimpleRunSummary` (`plainRunState`, `lastSaid`, `shownSaid`) is the summary.
- **Known blockers.** The owner's answer names two; they are handled differently, because only one of them can run in the background.
  - *Signed out:* not a fallback. The runner declines a signed-out agent's job in **any** mode (`jobs.go:355-357`, `DeclineAgentSignedOut`), so a background run would never happen. Simple starts nothing and shows #17's sign-in help (`result.signedOut`, `runs.signedOut.*`; `SimpleAgentBox.tsx:164-179`), which brings the box back once the runner sees the sign-in (#17's one-minute re-check). So Simple never shows a CLI sign-in screen, and never promises a run that cannot start.
  - *Folder not trusted:* **background fallback.** The §8.1 rule gives background when the agent is `untrusted`, and Simple says why in plain words (`runDialog.simple.background.untrusted`). Headless runs never show a trust screen.
  - How trust is detected: nothing detects this today. R2 adds a per-agent probe on the runner: would this CLI show its own "trust this folder" screen for a new worktree under `<dataDir>/worktrees`? It is reported as `agents[].untrusted` in the hello and re-checked with the same one-minute cadence as `signedOut` (`runner/detector.go:147-151`). The smoke test decides, per CLI, how trust is read (headless runs never show a trust screen). Until a CLI's probe exists, it reports `untrusted: false`.
- The handoff rule "the single-user path never needs a terminal" is retired.

### 8.3 Where
- `GlobalAgentForm.tsx:175`: `useState<DispatchMode>("headless")` becomes an unset choice resolved by the rule. A card the person picks wins.
- `DispatchDialog.tsx:212`: same.
- `SimpleAgentBox.tsx:131-147`: send `mode` when the rule gives interactive; `startFailure(…, {interactive: mode === "interactive"})` (:147).
- Follow-ups: the same rule. A queued reply is always background.

**Preference.** `runMode: "auto" | "interactive" | "background"` (`preferences/schema.ts`, beside #13's `copilotIntent` and `copilotVoiceAutoSend`), default `auto`. Picking a mode card stores it.

**i18n.** Rewrite `terminal.modeNoteLocal` / `terminal.modeNote` (§15). `globalAgents.noAsk` is **not** rewritten: the same key feeds `game/labels.ts:142` (actor aria text) and `RunWorkspaceDialog.tsx:372`. The list note gets a new key, `globalAgents.replyNote`.

---

## 9. Picture-card pickers

**`web/src/game/warroom/castArt.tsx` (new, foundation, F2).**
- Moves `still()`, `Face`, `skinVars` and the `FACE_*` constants out of `StoryRoom.tsx` (:69-73, :102-137, :188-200). This deletes those bodies from `StoryRoom.tsx`: a real edit to a W5 file, done in Wave 0 before W5 starts.
- Adds `useCastCard(agent, model, scene = "idle")`, which wraps `useActiveThemeCast(true)` and `figureFor` and works without the WarRoom provider.
- **Cards draw an `<img onError>`, not a CSS background.** A CSS background never fires an error event, and broken state is per hook instance (`themeCastContext.ts:62-66`). The image is cropped to `scene.box` the way `Face` is, because `still()` scales the whole scene rect and a whole-picture scene would show a tiny figure on a 96 px card. This matches `web/src/copilot/draft/DraftPresenter.tsx` (#13), which already renders pack stills outside the war room with `<img onError>`. `DraftPresenter` itself is not changed.
- `castArt` imports only `themeCast`, `themeCastContext` and `useThemePack`, so pickers never pull in the war-room chunk.
- **Folder art (answer 4).** `useFolderArt(repoId)`: when the active pack has rooms, it picks one by `hash(repoId)` (`themeCast.ts:33`) over a **sorted** list of room ids. `themeCastContext.ts` (:20-44) exposes that list; this is a context change, not a manifest change. With no rooms (`themeManifest.ts:141, 380`) or a broken image, it returns nothing and the card is the plain folder card with `IconFolder` (`web/src/icons.tsx:185`). Room images can be up to 16 MiB, so they load lazily.

**`web/src/station/pickers/` (new):**
- `PictureCards.tsx`: generic.
  - `mode:"one"`: `role=radiogroup`, `role=radio` with `aria-checked`, roving tabindex and arrow keys (copied from `web/src/components/settings/OfficeStylePicker.tsx:43-51`).
  - `mode:"many"`: `fieldset` / `legend` with native `input[type=checkbox]` inside label cards, All / None, and the count `已選 {n} 個`.
  - `aria-invalid` and `aria-describedby` sit on the group, keyed by `failure.field` (`agent`, `model`, `repoId`, `repoIds`).
  - Grid: `repeat(auto-fill, minmax(96px, 1fr))`. Rows scroll beyond 3; one column of wide cards at ≤ 400 px.
- `AgentCards.tsx`: one card per installed agent. Picture, `agentName(kind)`, version in small text. Below the cards, one muted `picker.signedOut` line per signed-out agent (it has no card, §8.1).
- `ModelCards.tsx`:
  - Always shows a visible model name (`runnerModelLabel`); "CLI 預設" comes first and shows the agent's character (Simple: `picker.simple.defaultModel`, "Its usual model").
  - Model ids are matched against the pack, so a deepseek model shows the DeepSeek card.
  - With more than 8 models: grouped by character, with a filter box and "還有 {n} 個模型" (OpenCode can list 64).
- `RepoCards.tsx`: folder cards, room art or plain (inline neutral SVG). The repo name and path tail are always visible, because two repos can share a room when a pack has few. Multi-select only in `GlobalAgentForm`.
- `ModeCards.tsx`: 互動 shows the `speak` still; 背景 shows the `write` still.
- `RunnerIcon` (in `PictureCards.tsx`): a fixed runner icon with the runner's name. No pack art (§0.9).

**Plain fallback.** Used when there is no pack, no match and no fallback character, or the image is broken. It shows `agentName` + `initials()` on `station-avatar agent-<kind>`. No logos (legal rule; `docs/theme-packs.md:325`). The unnamed `?` is not used in pickers.

**Keep the derived-on-every-render fallbacks** (`agents[0]`, `defaultModel`, `repos[0]`). In DispatchDialog, a pick sets `touched.current.agent` (`DispatchDialog.tsx:689, 707`; the keys are declared at :223). There is no `.model` key, and none is added: a model card also sets `.agent`.

**Images** are `alt=""`. The card label is name + model.

---

## 10. Multi-repo global agents

### 10.1 Shapes
| | A: one session over N repos | **B: fan-out (default)** |
|---|---|---|
| Fits | changes that must stay consistent in one session (bump a shared library, then link consumers locally) | the same change in independent repos: dependency upgrade + fix breakage per service, CI workflow edits, a CVE fix with one PR each |
| Run model | one run + new `run_repos`; `run_changes`, merges and workspace actions re-keyed per repo | `runs.group_id` + `run_groups`; each member is an ordinary global run |
| Runner | `worktrees/<run>/<repo>/`; every worktree, kept, preview, prwatch and `findRunWorktree` path changes; needs a run-folder NoBoard marker that `cmd/nodevas/mcp.go:122-152` understands (**security gap otherwise**); codex in a non-git folder needs `--skip-git-repo-check` | **unchanged** |
| Dialog | one dialog, interactive | group view; each member's dialog |
| Interactive | yes | background. N PTYs would each hold a slot until someone ends them (`maxConcurrent` defaults to 1) |
| Results | new per-repo state inside one run | existing `ResultActions` per member; partial success comes for free |
| Cost | very high | moderate |

**Why B.** All three typical tasks are per repo. B reuses the runner, git, PR and results paths unchanged, and partial success is natural.

**A lockstep library + consumers task under B** takes two steps: run the library alone, publish it, then run B over the consumers.

### 10.2 B in detail
- **Request.** `POST /api/global-runs/groups {runnerId, repoIds[2..30], agent, model, base?, task, slug?, openPR, simple, onBusy?}` returns `201 {group, runs[]}`. One folder keeps today's `POST /api/global-runs`.
- **Hub** (`internal/runs/groups.go`, G2):
  - `StartGlobal` is split into `validateGlobal` (task, slug, base, runner ownership, agent/model, caps), run once, plus a per-repo `CheckAgainstHello`.
  - Duplicate `repoIds` are refused (400, field `repoIds`).
  - Mode must be background; interactive gives 400 `GROUP_NEEDS_BACKGROUND`.
  - **Members' prompt** (answer 6): each member's prompt lists its siblings ("This is one of N repositories changing together: a, b, c …", names cut to 1 KiB). The hub builds `JobPrompt` itself through `BuildPrompt`'s extra argument (`runs/global.go:154`; `start.go:616-628`), so there is **no `Job` field and no capability bump**.
  - **One transaction** (§C.2.3 shape for a multi-run start): take the advisory lock `budget:<owner>` (F1's `lockBusy`), read `BudgetStatus` through the transaction, refuse the **whole group** when `Used.Runs + N > DailyRuns` (429 `{room}`; the message says how many fit, and the UI keeps the first that many folders picked), apply §C's busy decision, then insert the `run_groups` row and N runs through `createWith(tx)`. `store.Create` uses `s.db` and then `s.Get` and takes no transaction (`store.go:281-388`), and the hub has one writer (`postgres.go:82`), so F1 provides `createWith(q)`, which reads the row back through the transaction. The lock keeps two concurrent groups from each overshooting by N.
  - After commit: each member's seq-0 `user` event, one audit entry `run.global.group {group, count}`, then one `Pump`: FIFO under `maxConcurrent`; the rest stay queued.
- **Pump** (`dispatcher.go`):
  - skips members while `run_groups.held_until` is in the future (F1);
  - **re-checks cost and tokens only** before offering a queued member (G2), and cancels it with `failure_code = 'budget'` if over. Runs are not re-checked: `Used.Runs` counts queued members from the moment they exist (`budget.go:94-108`), so a group that exactly fills `DailyRuns` makes `Exceeded == "runs"`, and a re-check written as `Exceeded != ""` would cancel every sibling. (`release()` has the same flaw today, `handoff.go:201-212`; it is not changed here.) The code is stored in #17's `failure_code` column, so no new column is needed.
- **Usage limit.** A member that finishes with `FailureKind` `quota` or `rate_limit` (`runnerwire/routing.go:70-71`; there is no `usage_limit` kind) sets `held_until = reset_at` (or the default backoff from `resetTime`, `handoff.go:73-81`). This happens in `Finish` (G2), **before** `afterEnd`'s global early return (`dispatcher_routing.go:49-51`).
  - Pump runs only on submit, finish, attach, cancel and release, so nothing would pump when `held_until` passes. F1 adds one call on the `RunRetries` 15 s tick (`handoff.go:151-181`): `d.releaseGroupHolds(ctx)` (G2), which pumps runners whose group hold has expired.
  - UI: "已暫停到 {time}：用量上限" and "立即繼續" (clears the hold).
  - The member that hit the limit continues through a follow-up.
- **Routes:**
  - `GET /api/global-runs/groups/:id`
  - `POST …/groups/:id/cancel`: `CancelAllGlobal` filtered by group
  - `POST …/groups/:id/resume`
  - `POST …/groups/:id/follow-up {message, only: "failed" | "all"}`: runs `FollowUp` for each eligible latest turn and returns `{started[], skipped[{runId, code}]}`. It needs G1's `FollowUp`, so it is built by J1 (Wave 1b), not G2
  - `Run.groupId` on every member
- **Results.**
  - Per member: Take a look / Apply to my folder / PR, via the existing `ResultActions`.
  - Group button "套用全部已完成的": sequential merge_local over finished members, stopping per repo on a conflict and reporting each.
  - PRs per repo: chosen at start (`openPR`). There is no later "open PR" action for groups.
- **Unaffected repos** (e.g. the CVE is not present): the agent changes nothing, the worktree is dropped, and the member shows "沒有變更".
- **Team hub.** Every repo must be in that one runner's hello. Each member is accepted by that runner's rules: N console asks unless the auto-accept repo list covers them. `describeOffer` shows each member as an ordinary global run (title, repo, task preview); the runner is not told that it belongs to a group, because the `Job` carries no group field (answer 6). No hub "accept all".
- **Queue.** With no per-runner lane (answer 8) and `maxConcurrent` 1, a 30-member group queues ahead of every later run on that runner. The group row shows "{done} of {count} done" and the waiting count, so the queue is visible.
- **Retention.** `run_groups` rows are kept like `runs` rows: `analytics.Prune` never deletes runs (`retention.go:73-101`).
- **Story room group view (built now, round 2).** Today the story group key is the figure (`character/sceneName`, `storyPlaces.ts:72-73`; `themeCast.ts:102`), so members split as soon as their states differ, and unrelated runs in the same scene merge. Actors have no repo field (`web/src/game/world.ts:101-151`; `globalActor` in `web/src/game/building.ts:270`). W5 changes:
  - `world.ts`: `Actor` gains `repoName?` and `groupId?`;
  - `building.ts`: `globalActor` fills them from the run;
  - `storyPlaces.ts`: actors with a `groupId` group by `group:<id>` first (one figure per group, the members' shared agent and model), then by figure as today; members are ordered by repo name;
  - `StoryTalk.tsx`: member tabs show repo name + a pose face, and a pip strip shows per-repo status.

### 10.3 Checked against typical tasks
| Task | Shape | How |
|---|---|---|
| Upgrade a dependency to vX in every service and fix the breakage | B | per-repo upgrade and tests; failures are continued per member through follow-ups |
| Change every repo's CI workflow | B | `openPR` gives one PR each; follow-ups update those PRs (§6.5) |
| Fix a CVE in all affected repos with a PR each | B + `openPR` | unaffected repos end with no change |
| Bump a shared library, then make every consumer use it before it is published | A | deferred; two-step B for now |

### 10.4 A, later (behind `CapGlobalMultiRepo`)
`run_repos` table; per-repo changes, merges and workspace actions; a run-folder NoBoard marker plus the `mcp.go` lookup (required for safety); codex `--skip-git-repo-check`; per-repo web keys; one interactive dialog.

---

## 11. Text that becomes pictures (✓ = in this work)

**Station global tab**
1. ✓ agent select → character cards
2. ✓ model select → character cards with model name
3. ✓ repo select → folder cards (multi): pack room art by repo-id hash, or a plain folder card
4. ✓ mode radios → `speak` / `write` scene cards
5. ✓ runner select → a card with a fixed runner icon and the name (no pack art: manifest places are text only, `themeManifest.ts:86-87, 144`; the runners place has `groups: []`, `storyPlaces.ts:127`)
6. ✓ GlobalAgentList status chip → face + pose still (status text kept visually hidden)
7. ✓ repo name column → folder mini-card (room art or plain)
8. ✓ group per-repo status → folder card + pose still + pip strip
9. ✓ `noAsk` chip → removed; a new `globalAgents.replyNote` takes its place in the list
10. later: RunnerLoad busy / queued numbers → a row of faces
11. later: AutoAccept and full-access notes → icon + tooltip
12. stays text: open PR, base, slug, budget

**Describe box**

13. ✓ agent select → agent cards
14. ✓ folder "Change" → folder card (room art or plain)
15. ✓ StartedLine → face + pose still + the dialog

**Story room**

16. ✓ facts list, when a pack is active:
    - status → already shown by the figure (kept visually hidden)
    - runner → the fixed runner icon with the name as a tooltip
    - tokens → EnergyHud-style bar
    - held mail → mail icon with count
    - tests → test lamp
    - overlap (§C) → a small "same files" mark with the text in a tooltip
    - role and node stay text
17. ✓ group member tabs `1. working` → repo name + face (§10.2)
18. ✓ canned line → header flavour only
19. ✓ ask modal → inline `AskCard` row (node runs)
20. later: choice buttons → icon + short label (text kept for a11y)
21. stays text: the agent's output and the person's input; the plate's model name

---

## 12. API contracts

```
POST /api/global-runs/:id/follow-up   {message, mode?:"interactive"}  -> 201 Run (continueOf, workRoot)
  409 CONTINUED{latestId} | IN_USE | WORK_GONE | PR_ENDED | MERGE_PENDING | RUN_LIVE
  412 RUNNER_OUTDATED   429 budget (as today)   400 field "message"
GET  /api/global-runs/:id/thread      -> {runs: Run[]}   oldest first, <= 50
POST /api/global-runs/:id/end  |  POST /api/runs/:id/end
  -> 202 Run   403 outside mayType, or view-only and not the runner owner
  409 not a running interactive run   412 old runner
POST /api/runs/:id/merge (existing)   + 409 TURN_RUNNING while a turn of the work root is unfinished
POST /api/global-runs/groups          {runnerId, repoIds[2..30], agent, model, base?, task, slug?, openPR, simple, onBusy?}
  -> 201 {group, runs}   400 GROUP_NEEDS_BACKGROUND | field repoIds (duplicate)   429 {room}
GET  /api/global-runs/groups/:id      -> {group, runs}
POST /api/global-runs/groups/:id/cancel     -> {cancelled, runs}
POST /api/global-runs/groups/:id/resume     -> {group}
POST /api/global-runs/groups/:id/follow-up  {message, only:"failed"|"all"} -> {started: Run[], skipped:[{runId, code}]}
Run += continueOf?, workRoot?, continuedBy?, viewOnly?, groupId?, resetAt?, endReason?, pushRefused?
RunEvent.type += "user"
Runner: hub->runner  end {runId, noPR?}
        runner->hub  accept {runId, base, branch, viewOnly}
                     result += endReason, pushRefused
                     decline codes += pr_ended, pr_moved, branch_moved (kept by the hub's CleanDeclineCode)
        hello        agents[].interactive, agents[].untrusted, capabilities += "continue" (J1, after R1 and R2)
Realtime: no new names (global-runs-changed, runs-changed)
```

All routes are `personOnly`, use `canSee`, and send global notifications only to the dispatcher and the runner owner. Hub refusals use `locale.Errorf` en/zh pairs. §C.6 adds the busy, overlap and contest routes.

---

## 13. DB

Migrations 0040 (`0040_copilot_entry_data`, `postgres.go:1221`), 0041 (`0041_plan_run_drafts`, :1231) and 0042 (`0042_run_failure_code`, :1247, #17) are taken. This work adds three, in this order: `0043_run_continue` and `0044_run_groups` (F1), and `0045_run_coordination` (F1, §C.5). The next free numbers were checked in `internal/db/postgres.go` on `e22700e`; F1 checks again with `grep -n 'name: "00' internal/db/postgres.go` on the day it writes them. Tests assert the names, and the NAS records applied steps by name, so a name can never change after release.

**`0043_run_continue`**
```sql
ALTER TABLE runs ADD COLUMN IF NOT EXISTS continue_of TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS work_root   TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS view_only   BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS reset_at    TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS end_reason  TEXT NOT NULL DEFAULT ''
    CHECK (end_reason IN ('', 'ended', 'quiet', 'quiet_waiting'));
ALTER TABLE runs ADD COLUMN IF NOT EXISTS push_refused BOOLEAN NOT NULL DEFAULT FALSE;
-- one live follow-up per run; a follow-up that never started (taken back, or
-- declined by the runner) is withdrawn and frees the slot
CREATE UNIQUE INDEX IF NOT EXISTS runs_one_continuation ON runs (continue_of)
  WHERE continue_of <> '' AND NOT (status IN ('cancelled', 'declined') AND started_at = '');
CREATE INDEX IF NOT EXISTS runs_work_root ON runs (work_root) WHERE work_root <> '';
```
`started_at` exists since `0010_runs` (`postgres.go:515`) and is stamped on entering running (`store.go:772`).

**`0044_run_groups`**
```sql
CREATE TABLE IF NOT EXISTS run_groups (id TEXT COLLATE "C" PRIMARY KEY, owner_id TEXT NOT NULL,
  runner_id TEXT NOT NULL, created_by TEXT NOT NULL, created_by_name TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL, agent TEXT NOT NULL, model TEXT NOT NULL, no_pr BOOLEAN NOT NULL,
  repo_count INTEGER NOT NULL, held_until TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
ALTER TABLE runs ADD COLUMN IF NOT EXISTS group_id TEXT COLLATE "C" NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS runs_group ON runs (group_id, created_at) WHERE group_id <> '';
```
- `runs_global_shape` (`postgres.go:1178-1181`) does not mention these columns. §C's 0045 rewrites it for `compare` (§C.5).
- The `runs.workspace` CHECK is unchanged (`continued` is derived).
- **Store.** The new columns go after `draft, failure_code` in `runColumns` / `scanRun` (`store.go:389-398, 400-414`), and `Job()` keeps carrying `Draft` (`store.go:150`). `Finish` stores `reset_at` (today only the in-memory `r.ResetAt`, `store.go:835`), `end_reason` and `push_refused`.
- `postgres_only_test.go`: add the three names to the want list (`:126-129`). `TestMigrationNamesUniqueAndOrdered` allows only 0036 to repeat.
- `internal/legacysqlite/legacysqlite_test.go`: `TestImportTableListCoversPGSchema` (:183-213) fails for any new table. Add `run_groups` (and §C's three tables) to the "came after SQLite was removed" case (:194).
- Retention: `run_groups` rows are kept, like runs (§10.2).

---

## 14. Security guards

- **The hub names run ids only.** The runner resolves folder, branch, HEAD and session from its own `work.json` and git. It never checks out a hub-named ref and never resumes a hub-supplied session. `work.json` is 0600; ids are checked against the run-id regex; a folder is reused only after the git-dir ownership check.
- **Pushing to an open PR** (§6.5). The runner pushes only the root's own branch, fast-forward only, never with force, never a failed turn, and only after it re-read the PR (open, same repo, same head branch, same base). It never opens a second PR for a root. It never marks a PR ready. The hub refuses a merge approval while a turn is unfinished, and the runner refuses a merge job while a turn of the same root is active.
- **Global runs and groups:** the runner must be the caller's own, routes are `personOnly`, agents are refused, and every repo must be in that runner's hello. Never across runners.
- **No new input path into a running agent.** The composer uses the existing terminal socket, so `mayType`, `remoteTyping`, audit, the 64 KiB/s bucket, the Origin / Sec-Fetch checks, `StillAuthorized` and the 64 KiB cap all still apply. Queued replies are new *offers*, not input. Bracketed paste only wraps what the person typed.
- **Asks** go only through `answerAsk` with `MayAnswer` (permission prompts are the runner owner's alone), runScope tool/command/once, and truncated input means once.
  - No relay is added for global or interactive runs, and `noRelay` is unchanged.
  - Interactive permission prompts stay in the TUI, answered by whoever may type: the dispatcher or the owner. For global runs those two are the same person.
- **End** is limited to the `mayType` set, and to the runner owner on a view-only run. It quits gracefully before any kill. Publish keeps `guardGitDir`, the NoBoard mark and the autoPR rules.
- **Offers** on a team hub still wait for the runner's console, its auto-accept policy, or the remote-typing consent. Follow-ups and group members are accepted one by one. `Origin=person` and `Simple` lift no gate.
- **Redaction.**
  - The follow-up text is the person's own task, the same as a global task today. It is stored as the seq-0 `user` event and is never put in the audit log.
  - `ContinueSummary` and the per-turn PR comment's summary come from an already-redacted summary.
  - R3 transcript events go through `emit`, so `redact.Output` applies.
  - Terminal bytes stay unredacted, as today.
- **Session ids** are validated by a regex, stay on the runner, and go in argv only.
- **Budget** is checked per follow-up; for a multi-run start (all N group members, or a contest's entries and reviewer) it is read inside the start's transaction under the per-owner `budget:<owner>` lock (§C.2.3); cost and tokens are re-checked at Pump.
- **Transcript tail (R3).** The runner reads only `~/.claude/projects/*/<session-id>.jsonl` for the UUID it chose. It requires a regular file (no symlink) owned by the runner user, and it only tails (read-only).
- §C.10 adds the coordination guards.

---

## 15. i18n keys (all in `web/src/i18n.ts`, en + zh-TW)

**Checks.** Parity holds today (4458 keys each), but there is no global parity test. F2 extends the `FAMILIES` list in `web/src/i18nCopilotParity.test.ts` with `runDialog.`, `picker.`, `globalAgents.group.`, `busy.`, `overlap.` and `contest.`. Simple strings live in their own families, `runDialog.simple.`, `picker.simple.`, `busy.simple.` and `overlap.simple.`, which F2 adds to `web/src/i18nPlainWords.test.ts`. So the non-Simple strings may say "CLI", "runner" and "pull request", and the Simple ones may not.

**`runDialog.*`**
| key | en | zh-TW |
|---|---|---|
| label | {who}'s session | {who} 的工作階段 |
| screen / steps | Screen / Steps | 畫面 / 步驟 |
| you | You | 你 |
| turn | Turn {n} | 第 {n} 輪 |
| noOutput | Nothing said yet | 還沒有輸出 |
| notes | Show runner notes | 顯示 runner 紀錄 |
| earlier | Show earlier | 顯示較早內容 |
| expand | Open large | 放大 |
| shownAbove | Shown above | 已在上方顯示 |
| cut | (longer text was cut) | （較長的內容已截斷） |
| screenGone | The screen is kept 10 minutes after the end. Its last lines: | 畫面只保留到結束後 10 分鐘。最後幾行： |
| send / done / stop / withdraw | Send / Done, keep the work / Stop / Take back | 送出 / 完成，保留成果 / 停止 / 收回 |
| newWork | Start new work with this | 用這句開始新工作 |
| continuedBelow | Continued below | 接續在下方 |
| end.quiet | Ended after 20 quiet minutes; the work is kept | 安靜 20 分鐘後結束，成果已保留 |
| end.quietWaiting | It stopped while it was waiting for your answer | 它在等你回答時停止了 |
| ph.type | Type to the agent, Enter to send | 輸入要對 agent 說的話，按 Enter 送出 |
| ph.followUp | What should change next? (e.g. make the button bigger) | 還想改什麼？（例如：按鈕再大一點） |
| ph.followUpPr | What should change next? (updates its pull request) | 還想改什麼？（會更新它的 pull request） |
| ph.queued | Waiting for the runner to start it | 等待 runner 開始 |
| ph.waitingOwner | Waiting for {who} to say yes on that computer | 等待 {who} 在那台電腦上同意 |
| ph.background | It will read this when it finishes | 它做完就會看到 |
| ph.resetAt | Ready again at {time} | {time} 後可再繼續 |
| ph.viewOnly | The runner's owner did not allow typing from here | runner 的擁有者不允許從這裡輸入 |
| ph.notYours | Only {name} can reply | 只有 {name} 能回覆 |
| ph.merging | Being merged; continue after it is done | 正在合併，完成後再接續 |
| ph.screenAsks | The screen is asking something: answer it there | 畫面正在問你問題，請直接在畫面上回答 |
| ph.sendFailed | Not sent: {reason}. Your text is kept | 沒有送出：{reason}。你的文字還在 |
| ph.nodeRun | Dispatch the node again to continue | 請重新派送這個節點以繼續 |
| err.continued / inUse / gone / outdated / live | Already continued; showing the latest turn / Another turn of this work is still running / This work's folder is gone / Update nodevas on the runner to continue work / Type into its screen instead | 已有後續，改顯示最新一輪 / 這份工作還有一輪在執行 / 這份工作的資料夾已不在 / 請更新 runner 的 nodevas 才能接續工作 / 請直接在它的畫面輸入 |
| err.prEnded / branchMoved / prMoved / mergePending / turnRunning | Its pull request was merged or closed; start new work instead / Its branch changed on GitHub; this turn was not pushed / Its pull request now targets another branch / A merge is pending; continue after it / A turn of this work is still running; merge after it ends | 它的 pull request 已合併或關閉，請開始新工作 / 它的分支在 GitHub 上有變動，這一輪沒有推送 / 它的 pull request 已改合併到別的分支 / 正在等待合併，完成後再接續 / 這份工作還有一輪在執行，結束後再合併 |
| resumeLost | Continuing without the agent's memory of the last turn | 這一輪 agent 不記得上一輪，改用摘要接續 |
| tool.run/write/edit/read/search/web | Run / Write / Edit / Read / Search / Look up {target} | 執行 / 寫入 / 修改 / 讀取 / 搜尋 / 查詢網路 {target} |
| tool.files | {first} and {count} more | {first} 等 {count} 個檔案 |
| tool.todo | Update the to-do list | 更新待辦清單 |
| tool.agent | Send a helper: {target} | 派出助手：{target} |
| tool.mcp / tool.board | Use {tool} from {server} / the board | 使用 {server} 的 {tool} / 看板 |
| tool.claim / getNode / setStatus | Take the task / Read the task / Report status | 接下任務 / 讀取任務 / 回報狀態 |
| tool.other / thinking / failed / running | {tool} / Thinking / failed / working… | {tool} / 思考 / 失敗 / 處理中… |

**`runDialog.simple.*`** (plain words; no "runner", "CLI", "branch", "pull request", "worktree"; never names the actor)
| key | en | zh-TW |
|---|---|---|
| frame | This is {agent}'s own screen. It may speak English. Type below, or use the buttons. | 這是 {agent} 自己的畫面，可能會顯示英文。可以在下面輸入，或按按鈕。 |
| done | Done, keep what it made | 完成，保留它做的東西 |
| keys.enter / esc / up / down / pick | Enter / Back / Up / Down / Choose {n} | 確定 / 返回 / 上 / 下 / 選 {n} |
| ph.queued | Getting ready… | 準備中… |
| ph.background | It will read this when it is done | 它做完就會看到 |
| ph.followUp | Anything else to change? | 還想改什麼？ |
| ph.resetAt | It can go on at {time} | {time} 後可以繼續 |
| ph.viewOnly | You can watch, but not type here | 這裡只能看，不能輸入 |
| ph.screenAsks | It is asking you something on the screen | 它在畫面上問你問題 |
| ph.sendFailed | Not sent. Your words are kept | 沒有送出，你的文字還在 |
| ph.merging | Being saved into your folder… | 正在存進你的資料夾… |
| ph.waitingBehind | It starts when the earlier job is done | 前一個工作做完就會開始 |
| continuedBelow | It went on below | 後面還有接著做的 |
| background.untrusted | {agent} has not been allowed to work in this folder yet, so it works quietly for now | {agent} 還沒被允許在這個資料夾工作，所以這次先在背景做 |
| end.quiet | It stopped after 20 quiet minutes; what it made is kept | 安靜 20 分鐘後停止了，做好的東西已保留 |
| end.quietWaiting | It stopped while it was waiting for your answer | 它在等你回答時停止了 |
| err.gone | This work is finished; start new work below | 這份工作已經結束，請在下面開始新的工作 |

**`picker.*`**
| key | en | zh-TW |
|---|---|---|
| agent / model / repos / runner | Who works / Which model / Which folders / Which computer | 誰來做 / 用哪個模型 / 哪些資料夾 / 哪台電腦 |
| all / none / picked | All / None / {count} picked | 全選 / 全不選 / 已選 {count} 個 |
| filter / more / defaultModel | Find a model / {count} more models / CLI default | 搜尋模型 / 還有 {count} 個模型 / CLI 預設 |
| mode.interactive / mode.background | Talk with it / Background | 互動 / 背景 |
| mode.group | Several folders run in the background; talk to any one after it finishes | 多個資料夾會在背景執行；任一個完成後都能接著對話 |
| mode.fallback.agent / teammate / autoAccept / noTyping / staged / pipeline / untrusted | {agent} cannot run interactively here / Someone else's runner runs it in the background / Its auto-accept covers background runs only / Its auto-accept does not allow typing from here / A staged node runs in the background / Fallbacks and pipelines run in the background / {agent} has not trusted the runner's work folders yet | {agent} 在這裡不能互動執行 / 別人的 runner 會在背景執行 / 它的自動接受只涵蓋背景執行 / 它的自動接受不允許從這裡輸入 / 分階段的節點會在背景執行 / 備援與管線會在背景執行 / {agent} 還沒信任 runner 的工作資料夾 |
| signedOut | {agent} is not signed in on that computer; sign in there to use it | {agent} 在那台電腦上還沒登入，請先在那裡登入 |

**`picker.simple.*`**: `defaultModel` "Its usual model" / 「它平常用的模型」; `repos` "Which folder" / 「哪個資料夾」; `fallback.untrusted` reuses the `runDialog.simple.background.untrusted` sentence. There is no Simple signed-out key: Simple reuses #17's `result.signedOut`.

**`globalAgents.group.*`:** title `{count} folders` / `{count} 個資料夾`; progress `{done} of {count} done` / `已完成 {done}/{count}`; waiting `{count} waiting their turn` / `{count} 個排隊中`; stopAll 全部停止; applyAll 套用全部已完成的; tellAll 告訴全部; tellFailed 告訴失敗的; paused `Paused until {time}: usage limit` / `已暫停到 {time}：用量上限`; resume 立即繼續; room `Today's limit leaves room for {count} runs` / `今天的上限只剩 {count} 次`; noChange `Nothing to change here` / `這裡沒有需要改的`; budget `Stopped: today's cost or token limit was reached` / `已停止：今天的費用或 token 上限已到`.

**New:** `globalAgents.replyNote`: "Background runs cannot ask you anything; reply here when it finishes to continue." / 「背景執行時無法問你問題；完成後在這裡回覆即可接續。」

**Rewritten:**
- `terminal.modeNoteLocal`: "Runs the agent's own terminal on this computer and shows it here; you can type to it." / 「在這台電腦的終端機中執行，畫面顯示在這裡，可以直接輸入。」
- `terminal.modeNote`: the same, with "on the runner".

**Unchanged:** `globalAgents.noAsk` (it also feeds `game/labels.ts:142` and `RunWorkspaceDialog.tsx:372`).

§C.8 adds the `busy.*`, `overlap.*` and `contest.*` keys.

---

## 16. Work packages

### 16.1 Rules
- **One owner per file per wave.** Shared files live only in the foundation packages F1 and F2. A few files pass from one package to a later one after the first has merged; §16.7 lists each such hand-over, and no two packages in one wave ever hold the same file.
- Every package starts with failing tests.
- Every package ends with the build gate (§16.8) green for what it touched; the full gate runs at the end of every wave.
- File lists were checked with `git ls-files` on `e22700e`. "(new)" marks files that do not exist yet. Test files follow their source file unless named.

### 16.2 Before Wave 0
1. **CI red.** The Web job fails only in its `npm audit` step (picomatch GHSA-fc5q-39xc-vjpf via vite/vitest), on master and on every PR. Fix it in its own small PR first, so a red Web check means something again.
2. **#16 and #17 are merged** (master `e22700e`), and master is merged into `feat/terminal-dialog` (`cacdb2e`). Done.
3. **Live CLI smoke tests** (in parallel with Wave 0; they must pass before R1, R2, A1 and W1 merge):
   - composer submit: text, then a CR 150 ms later, or bracketed paste; it must submit, and must not pick a menu option;
   - whether any TUI repaints while idle (the quiet timer, §7.2);
   - the quit sequence for End, per CLI (§7.1);
   - the question and menu markers (§3.1, §7.2);
   - trust and sign-in screens for a new worktree, and how each CLI's trust can be read (§8.2);
   - `codex exec resume` argv on 0.149.1; claude 2.1.327, opencode and agy resume;
   - gemini, once installed. Without it, Gemini ships without resume (answer 7).

   **Status: not run yet.** Waves 0 to 3 and the review fixes merged without these live tests. The interactive quit sequence for End, whether a TUI repaints while idle, the question and menu markers and the resume flags are still unconfirmed against the real CLIs; the code's comments say so too. Run them before calling the terminal dialog done, and record the results here.

### 16.3 Wave 0: foundations (F1 ∥ F2)

**F1: Go contracts, DB, store, hooks.** No dependencies. It is the largest package; it lands after #17, which touched the same files.
- **Files:**
  - `internal/runnerwire/{runs.go, wire.go, global.go, workspace.go, terminal.go, coordination.go (new)}` (`workspace.go`: also `WorkspaceOutcome.PRURL` for `open_pr`) + `global_test.go`, `workspace_test.go`, `runs_test.go (new)`, `wire_test.go (new)`, `coordination_test.go (new)`
  - `internal/db/postgres.go` (0043, 0044, 0045), `internal/db/postgres_only_test.go`, `internal/legacysqlite/legacysqlite_test.go`
  - `internal/runs/store.go`: columns (after `draft, failure_code`; with §C's `overlapCount` subquery), `New` fields, Create guards, `createWith(q)`, `lockBusy(ctx, tx, keys...)`, `Successor`, `ActiveInWork`, `PriorFinal`, `GroupHeld`, `UserFirst`, `SetViewOnly`, `SetGroupHold`, `reset_at` and `end_reason` on finish
  - `internal/runs/pipelines.go`: `createPipelineWith(q)`
  - `internal/runs/start.go`: **only** the `StartBusy = "busy"` code (beside `start.go:77-84`) and a `Busy *runnerwire.BusyResponse` field on `StartError` (`start.go:88-108`), so G2 and C3h can return BUSY in Wave 1 and the HTTP layer can map it; C1 takes the file after
  - `internal/runs/offerask.go`: `CleanDeclineCode` (`:44-53`) keeps `pr_ended`, `pr_moved` and `branch_moved` + `offerask_test.go`
  - `internal/runs/{workspace.go, dispatcher_workspace.go}`: hub-side `open_pr` (§C.4.4). `CheckWorkspaceAction` (`workspace.go:189-210`) accepts it only on a succeeded contest entry with a commit, no `pr_url`, a live workspace and a runner with `CapCoord`; `CreateWorkspaceAction` (`:382`) accepts the action; the outcome handling (`:469-493`) stores the reported PR URL on the run and leaves `workspace` live; `workspaceVerb` (`dispatcher_workspace.go:12-17`) gives `open_pr`. G1 takes `workspace.go` after; `dispatcher_workspace.go` is F1's alone
  - `internal/runs/routing.go`: `jobKinds += compare` (one line, :28)
  - `internal/runs/dispatcher.go`: **only** the Pump skips (prior not final; group held; `waitsBehind`; `heldByOther`), the `d.coordJob` call in `offer()` (:330), the `d.acceptViewOnly` call in `Accept` (:408) and the `d.changesTouched` call in `Changes` (:518), each calling a stub
  - `internal/runs/dispatcher_routing.go`: `defer d.ended(ctx, r)` right after the `RunFinal` check in `afterEnd` (:43-46), before the global early return; the contest guard in `fallback`
  - `internal/runs/handoff.go`: the contest guard in `canWaitForReset` (:39); in `RunRetries` (:151) one `d.releaseWaiting(ctx)` and one `d.releaseGroupHolds(ctx)` call per tick
  - `internal/runs/coordhooks.go` (new, F1's alone): `ended` = `busyEnded` + `contestEnded`; the `releaseRunClaim` field and setter
  - stubs in new files that later packages take over: `internal/runs/{viewonly.go, groups.go, busy.go, overlap.go, contest.go}` (new), `internal/agents/runclaims.go` (new)
  - `internal/agenthook/changes.go`: export `SecretPath` (a rename of `secretFile`, nothing else)
  - `internal/server/server.go`: one line, `d.SetRunClaimRelease(hub.ReleaseRunClaim)`
  - `internal/runner/adapter/adapter.go`: `RunEnv.SessionID` / `RunEnv.Resume`, `SessionReporter`, `Resumer`, `ContinueInput`
  - `internal/runner/jobs.go`: **only** the `run` struct fields `session`, `workRoot`, `pendingEnd`
  - `internal/httpapi/runs/{global.go, api.go}`: route registrations and the `StartBusy` → 409 mapping in `startFailed`, plus new stub files `internal/httpapi/runs/{followup.go, groups.go, busy.go, overlaps.go, contests.go}` (new) whose handlers answer 501
- **Tests first:**
  - `TestContinueJobShape`, `TestGroupRequestBounds` (1, 2, 30, 31; duplicates), `TestValidSessionID`, `TestHelloAgentInteractiveAndUntrusted`
  - `TestMigrationsAddContinueAndGroups` (incl. `reset_at`, `end_reason`)
  - `TestStoreContinueColumnsGlobalOnly`, `TestOneLiveContinuationPerRun` (a withdrawn one frees the slot), `TestDeclinedFollowUpFreesTheSlot` (a follow-up declined before it started frees the index slot, and `Successor` ignores it)
  - `TestUserFirstSeqZero`, `TestFinishStoresResetAtEndReasonAndPushRefused`, `TestRunResultPushRefusedWire`
  - `TestCleanDeclineCodeKeepsContinueCodes` (`pr_ended`, `pr_moved`, `branch_moved` are stored as sent)
  - `TestStartErrorCarriesBusy` (the `StartBusy` → 409 mapping in `startFailed` with the busy payload)
  - `TestOpenPRWorkspaceActionOnlyForContestWinner`, `TestOpenPROutcomeStoresPRURL`
  - `TestPumpHoldsAFollowUpUntilPriorFinal`, `TestPumpSkipsAHeldGroup`
  - `TestAcceptCallsViewOnlyHook`
  - §C.9's F1 list

**F2: Web foundation.** No dependencies.
- **Files:**
  - `web/src/i18n.ts` (every §15 and §C.8 key), `web/src/i18nCopilotParity.test.ts`, `web/src/i18nPlainWords.test.ts`
  - `web/src/api/{runs.ts, globalRuns.ts, runners.ts, terminal.ts, contests.ts (new), index.ts}` (types, `followUp`, `endRun`, `thread`, group calls, `onBusy`, `BusyError`, busy and overlap calls, `RunnerAgent.interactive` / `.untrusted`, `viewOnly`, `endReason`) + `globalRuns.test.ts`, `terminal.test.ts`, `runs.test.ts (new)`, `contests.test.ts (new)`
  - `web/src/preferences/schema.ts` (`runMode`)
  - `web/src/game/warroom/castArt.tsx` (new) + `castArt.test.tsx` (new); `web/src/game/warroom/StoryRoom.tsx` (deletes the moved `still`, `Face`, `skinVars` and `FACE_*`; imports them from `castArt`); `web/src/game/warroom/themeCastContext.ts` (sorted room ids)
  - `web/src/app.css` + new `web/src/styles/{32-run-dialog.css, 33-pickers.css, 34-coordination.css}` (empty, with header comments)
- **Tests first:** `globalRuns.test.ts` (paths and bodies), `terminal.test.ts` (readonly from `viewOnly`), `runs.test.ts` (`BusyError` parsing), `contests.test.ts`, `castArt.test.tsx` (deepseek → DeepSeek still; `default` → the agent's character; broken → `onError` falls back to initials; crop to `scene.box`; `useFolderArt` stable per repo id, none without rooms), the extended parity families and the plain-word families.

### 16.4 Wave 1: parallel after F1 / F2 (Wave 1b: C1 and J1, after G1 and G2; J1 also after R1 and R2)

**G1: Hub follow-up, End, view-only, merge guard.** After F1.
- **Files:** `internal/runs/{followup.go (new), followup_test.go (new), workspace.go, terminal.go, viewonly.go, dispatcher_merge.go}`, `internal/httpapi/runs/{followup.go, terminal.go}`, `internal/httpapi/runners/api.go`, `internal/server/followup_test.go` (new).
- **Tests first:**
  - `TestFollowUpContinuesTheSameWork`, `TestFollowUpChainsRoot`, `TestFollowUpQueuedWhilePriorRunsInBackground`
  - `TestFollowUpRefuses{AnotherPerson, Agent, LiveInteractive, NotLatest, Discarded, MergedLocal, PRMerged, PRClosed, MergePending, OverBudget, OutdatedRunner, TooLong, NodeRun}`
  - `TestFollowUpAllowedOnOpenPR`, `TestFollowUpQueuedBehindSkipsOnlyRunLive` (G1 owns `QueuedBehind`)
  - `TestDeclinedFollowUpFreesTheSlot` (a follow-up declined `branch_moved` leaves the prior the latest turn: a new follow-up of it is accepted, `WorkspaceGate` does not say `continued`, merge and discard work; a follow-up naming the declined run gets `CONTINUED {latestId}`)
  - `TestFollowUpAfterUsageLimitWaitsForReset` (from the stored `reset_at`), `TestFollowUpNeverAuditsText`
  - `TestWorkspaceGateContinued`, `TestWorkspaceInUseByWorkRoot`, `TestMergeRefusedWhileTurnRunning`, `TestMergeThroughOlderTurnFailsHeadCheck`
  - `TestEndOnlyByTypists`, `TestEndOnViewOnlyRunOwnerOnly`, `TestEndOldRunner412`, `TestAcceptViewOnlyMakesViewersReadOnly` (an already-attached viewer turns read-only)
  - server: person-only, `canSee`, notify only the dispatcher and the owner

**G2: Hub groups.** After F1.
- **Files:** `internal/runs/{global.go, groups.go, budget.go, dispatcher.go, groups_test.go (new)}` (dispatcher: Pump cost/token re-check, usage-limit hold in `Finish`), `internal/httpapi/runs/groups.go`, `internal/server/global_groups_test.go` (new).
- `StartGlobal` and the group start take the §C.2.3 shape: checks, then one transaction calling `d.applyBusy` (F1's stub) and `createWith`, then `d.afterBusy`; `onBusy == "continue"` goes to `d.continueBusy`. G2 adds no busy rule and no busy test of its own: C1's tests pin the behaviour.
- **Tests first:**
  - `TestStartGlobalGroup{CreatesNRunsInOneTx, OwnRunnerOnly, RepoNotOnRunner, RefusesDuplicateRepos, BudgetForN, RefusesInteractive, WritesUserEvents, PromptListsSiblings}` (truncated sibling list)
  - `TestTwoConcurrentGroupsCannotOvershootBudget`
  - `TestPumpRechecksOnlyCostAndTokens` (a group that exactly fills `DailyRuns` is not cancelled), `TestPumpBudgetCancelStoresCode`
  - `TestQuotaOrRateLimitHoldsTheGroup` (global run; the hold is set before the early return), `TestExpiredHoldIsPumpedOnTick`, `TestResumeGroupClearsHold`
  - `TestCancelGroup`
  - (the group follow-up route and `TestGroupFollowUpFailedOnly` are J1's: they need G1's `FollowUp`)
  - The existing `TestStartGlobal*` stay green.

**R1: Runner work registry, continue, PR updates.** After F1.
- **Files:** `internal/runner/{work.go (new), jobs.go, global.go, worktree.go, workspace.go, workspace_actions.go, preview.go, kept.go, publish.go, prwatch.go, pr.go, changes.go, merge.go, accept.go}` + `followup_test.go (new)`, `work_test.go (new)`.
- Path tests need a real temp dir on macOS: `TMPDIR=/private/tmp/nodevas-realtmp`.
- **Tests first:**
  - `TestFollowUpWorksInTheSameFolder`
  - `TestFollowUpRefuses{StaleLastRun, HeadMoved, BranchMismatch, InUse, ForeignWorktree, RepoMismatch, DirtyAfterSuccess}`
  - `TestFollowUpAcceptsDirtyAfterCancel`, `TestFollowUpRecreatesANoChangeFolder`, `TestFollowUpDeclinesWhenPRFolderWasRemoved`
  - `TestCheckGlobalAllowsContinue`, plus `global_test.go:51,225` still refuse a handoff
  - `TestWorkspaceActionOnALaterTurn`, `TestContinueRekeysKeptAndWatched` (no second watch), `TestContinueStillNoBoard`
  - PR updates: `TestTurnPushesFastForwardToOpenPR`, `TestFailedTurnIsNeverPushed`, `TestNextGoodTurnPushesEarlierFailedCommits`, `TestTurnRefusesMergedOrClosedPR`, `TestTurnRefusesRetargetedPR`, `TestTurnFastForwardsWhenRemoteAhead`, `TestTurnDeclinesDivergedBranch` (never force), `TestTurnAttachesPRInsteadOfCreate`, `TestTurnPostsOneCommentPerPush`, `TestTurnNeverMarksReady`, `TestNoOpTurnKeepsRootBranch`, `TestTurnReportsOnlyItsOwnChanges`
  - `TestPRWatchKeepsFolderWhileTurnRuns`, `TestMergeJobRefusedWhileTurnActive`
  - `TestDescribeOfferNamesFollowUp` (and the PR it updates, when there is one)
  - `TestAcceptReportsViewOnly`, `TestResumeOnlyItsOwnSession`, `TestFallbackWhenAgentChanged`, `TestResumeFastFailRetriesFresh`

**R2: Runner interactive End, quiet, detect.** After F1.
- **Files:** `internal/runner/{interactive.go, client.go, detect.go, detector.go}` + `interactive_test.go`, `detect_test.go` additions. (No `config.go`, `settings.go` or `cmd/nodevas/runner.go`: the quiet timeout is a constant.)
- **Tests first:** `TestEndQuitsGracefullyThenKills`, `TestEndBeforeTerminalOpensIsKept` (pending end), `TestEndPublishesAnInteractiveRunAsSucceeded` (no `-1` failure), `TestQuietTimeoutEndsAfter20Minutes`, `TestQuietCountsInputToo`, `TestQuietWhileMenuShownIsQuietWaiting`, `TestIdleRepaintDoesNotResetQuiet`, `TestInteractiveClaudeGetsSessionID` (records `r.session`), `TestHelloReportsAgentInteractive`, `TestHelloReportsUntrusted`, `TestEndUnknownRunIgnored`.

**A1: Adapters.** After F1.
- **Files:** `internal/runner/adapter/{claude.go, codex.go, opencode.go, acp.go, opencode_acp.go, gemini.go, antigravity.go, interactive.go, global.go, continue.go (new)}` + tests.
- **Tests first:**
  - `TestParsersReportSessionID` (each fixture, Gemini included)
  - `TestClaudeResumeArgs` (headless and TUI), `TestClaudeInteractiveSessionIDArg`
  - `TestCodexResumeArgs` (`exec resume <id> -`; no `-s`, `-C` or `--color`; sandbox via `-c`; folder from `cmd.Dir`)
  - `TestOpenCodeSessionArg`, `TestAgyConversationArg`, `TestACPLoadSession`, `TestGeminiNoResume` (provisional until the smoke test)
  - `TestContinueSection` (with and without a PR), `TestGlobalPromptInvitesClosingQuestion` (update `TestGlobalPrompts`)

**W1: Dialog core.** After F2.
- **Files:** `web/src/runs/toolLine.ts` (new), `web/src/runs/terminalKeys.ts` (new), `web/src/runs/dialog/{RunDialog.tsx, RunTranscript.tsx, RunComposer.tsx, runLines.ts, useRunDialog.ts, liveTerminals.ts}` (new), `web/src/runs/RunTerminal.tsx`, `web/src/runs/runStore.ts` (chain helper, eviction), `web/src/runs/RunTimeline.tsx` (`user` kind), `web/src/game/GameActions.tsx` (Escape inside xterm), `web/src/styles/32-run-dialog.css`.
- **Tests first:**
  - `toolLine.test.ts`: stored names per parser (hand-normalised), codex raw string for `shell` and `web_search`, apply_patch with several files, `["bash","-lc",X]`, truncated JSON, mcp and bare board names, `mcp__terminal__x` is not "run"
  - `runLines.test.ts`: merge, ask pinning, turns
  - `RunTranscript.test.tsx`: full text, merged row expands, error mark, logs hidden but hub notes shown, follows the end, polite announcer throttle
  - `RunComposer.test.tsx`: every §4 row; IME Enter ignored; bracketed paste when on; CR as a separate, delayed write; text kept when the send fails; `prefill`; the Simple variants
  - `RunDialog.test.tsx`: interactive lazily mounts the terminal; read-only on `viewOnly`; expired screen shows the summary (`SimpleRunSummary` in Simple); chain shows `你` lines; End visible only to those who may End; Simple frame present; quiet and quiet-waiting endings worded apart
  - `liveTerminals.test.ts`: a second mount of one run shows "shown above"
  - `RunTerminal.test.tsx`: story palette; `terminalKeys` has no xterm import
  - `GameActions.test.tsx`: Escape inside `.xterm` is not swallowed
  - `runStore.test.ts`: events evicted on the last unwatch

**W2: Picture-card pickers.** After F2.
- **Files:** `web/src/station/pickers/{PictureCards, AgentCards, ModelCards, RepoCards, ModeCards}.tsx` (new) + tests, `web/src/styles/33-pickers.css`.
- **Tests first:**
  - radiogroup + arrow keys + roving tabindex; native checkboxes + All / None + count
  - `aria-invalid` on the group
  - deepseek → DeepSeek card; `default` → the agent's character
  - plain initials and no `img` without a pack; `onError` on a broken image falls back to initials; no logo assets
  - folder cards: room art by repo-id hash, plain without rooms, repo name and path tail always visible, images lazy
  - runner card: fixed icon, no pack art
  - a signed-out agent has no card and gets the `picker.signedOut` line
  - more than 8 models → filter + "還有 N 個"
  - phone grid (class check)

**C3h: Contests, hub.** After F1. See §C.4 and §C.9.
- **Files:** `internal/runs/{contest.go, contest_store.go (new), compare.go (new), contest_test.go (new), compare_test.go (new)}`, `internal/httpapi/runs/contests.go`, `internal/server/contests_test.go` (new). The hub side of `open_pr` (the workspace action's acceptance) is F1's; C3h only calls it from pick.

**CW: Coordination components, web.** After F2. See §C.8.
- **Files:** `web/src/runs/coord/{BusyChoice.tsx, OverlapNotice.tsx, ContestForm.tsx, ContestView.tsx, ContestCompare.tsx, coordStore.ts}` (new) + tests, `web/src/styles/34-coordination.css`.
- Standalone components with props, mounted in Wave 2.

**C1: Busy, hub (Wave 1b).** After F1 and **after G1 and G2 merge**: it calls G1's real `FollowUp` (with `QueuedBehind`, which G1 owns) and `End`, and its server tests drive G2's real `StartGlobal`, whose busy calls G2 wires. It runs beside the rest of Wave 1 that is still open, with disjoint files.
- **Files:** `internal/runs/{busy.go, busy_store.go (new), busy_test.go (new), start.go}` (start.go: the §C.2.3 shape and `RepoKey`, after F1's `StartBusy` lines), `internal/httpapi/runs/busy.go`, `internal/agents/{runclaims.go, runclaims_test.go (new)}`, `internal/autopilot/{orchestrator.go, goalflow.go}` (`OnBusy: queue`), `internal/copilot/{servertools.go, revise.go}` + `servertools_test.go`, `revise_test.go`, `internal/server/busy_test.go` (new; includes the global start's busy tests against G2's merged `StartGlobal`).

**J1: Wave 1 joins (Wave 1b).** After G1, G2, R1 and R2. Two small pieces that each need two Wave 1 packages merged.
- **Files:** `internal/runs/groupfollowup.go (new)` + `groupfollowup_test.go (new)`, `internal/httpapi/runs/groups.go` (the follow-up handler only), `internal/server/group_followup_test.go (new)`; `internal/runner/interactive.go` (one line in `capabilities()`, `:50`) + `internal/runner/caps_test.go (new)`.
- What it does:
  - the group follow-up route (§10.2): `FollowUp` (G1) for each eligible latest member turn of a group (G2), `{started[], skipped[{runId, code}]}`;
  - `CapContinue` in the runner's hello. It covers R1's registry and PR updates and R2's End and view-only, so it goes in only when both have merged. Until then the hub answers follow-ups `RUNNER_OUTDATED` and End 412, which is the intended state between waves.
- **Tests first:** `TestGroupFollowUpFailedOnly`, `TestGroupFollowUpSkipsNotLatestAndGone`, `TestCapabilitiesAdvertiseContinue`; server: the group follow-up route is person-only and own-runner-only.

### 16.5 Wave 2: parallel after W1 / W2 and the Wave 1 Go packages

**W3: Forms + interactive default + busy and contest mounts.** After W1, W2, CW.
- **Files:** `web/src/station/{GlobalAgentForm.tsx, SimpleAgentBox.tsx}`, `web/src/runs/{RunModePicker.tsx, DispatchDialog.tsx}` + `GlobalAgentForm.test.tsx`, `SimpleAgentBox.test.tsx`, `RunModePicker.test.tsx`, `DispatchDialog.test.tsx`, `DispatchDialog.roles.test.tsx`, `DispatchDialog.simple.test.tsx`, `DispatchDialog.single.test.tsx`, `DispatchRouting.test.tsx`, and `web/e2e/runs.spec.ts`.
- **Tests first:**
  - Rewrite the combobox queries to radio / checkbox (`GlobalAgentForm.test.tsx:139,239,266`; `SimpleAgentBox.test.tsx:217,231,320-321`; the DispatchDialog files; `web/e2e/runs.spec.ts:152-153, 546-547`).
  - `defaultDispatchMode` table: capability, agent flag, untrusted, teammate runner, auto-accept modes, auto-accept without typing, staged, pipeline, fallbacks, group, contest.
  - A picked card is remembered (`runMode`).
  - A group sends `repoIds` and is background only.
  - The Simple box sends `mode`, shows the dialog in its frame, and falls back to background with the plain reason for an untrusted agent. A signed-out agent gets no card and starts nothing: full mode shows the `picker.signedOut` line, and Simple shows #17's sign-in help.
  - `touched.current.agent` is still set on an agent or model card pick.
  - Busy: node preflight in DispatchDialog, global preflight in GlobalAgentForm and SimpleAgentBox; 409 → `BusyChoice` → re-send with `holderIds`; the 比稿 toggle renders `ContestForm` (not in Simple).
  - e2e: `runs.spec.ts` gets a busy case, and specs that dispatch twice on one node send `onBusy: "both"`.

**W4: Global list + group view + results.** After W1, CW.
- **Files:** `web/src/station/{GlobalAgentList.tsx, GlobalAgentGroup.tsx (new), globalAgentsStore.ts, waiting.ts}`, `web/src/runs/{ResultActions.tsx, resultState.ts}`, `web/src/styles/30-global-agents.css` + `GlobalAgentList.test.tsx`, `GlobalAgentGroup.test.tsx (new)`, `globalAgentsStore.test.ts`, `waiting.test.ts`, `ResultActions.test.tsx`, `resultState.test.ts`.
- **Tests first:**
  - a chain row shows the latest turn; one row expanded at a time
  - the `noAsk` chip is gone and `replyNote` is shown
  - group counts, pips, waiting count, "Paused until", the budget stop
  - group and contest rows refetch on `global-runs-changed` (no new event)
  - apply-all with partial results and a conflict
  - tell-failed calls group follow-up
  - `ResultActions` acts on the latest turn only; earlier turns say "接續在下方"; merge shows `TURN_RUNNING`; a PR-ended work offers new work
  - contest rows with entry pips; the overlap badge; the "waiting behind" line; `ResultActions` locked on an undecided contest entry

**W5: Story room + group view.** After W1, CW.
- **Files:** `web/src/game/warroom/{StoryTalk.tsx, StoryRoom.tsx, storyPlaces.ts, warroom.css}`, `web/src/game/{world.ts, building.ts}`, `web/src/station/StationWarRoom.tsx` + `web/src/game/warroom/WarRoomTheme.test.tsx` additions, `web/src/station/StationWarRoom.test.tsx`.
- **Tests first:**
  - a run actor shows real output, not the canned line; the canned line sits in the header; with no events the canned line is the body
  - no typewriter on the transcript; `still` honoured
  - the textarea is not hijacked by arrow and digit keys
  - compact terminal height inside the stage; "Open large" gives full size
  - list layout subscribes only boxes in view (≤ 6)
  - facts become pictures when a pack is active (visually-hidden text kept); the runner fact is the fixed icon
  - group view: members of one group stand as one figure even when their states differ; unrelated runs in the same scene do not merge into it; member tabs show repo name + face; pips
  - `OverlapNotice` in the header; contest entries stand as a pair
  - **screenshot bug check:** a global row resolves through `useRowOf`, so the choices never show 開啟執行 or 開啟「…」 for a global run

**C2: Touched files and overlap.** After R1, R2, G1 and J1 (the Wave 1 owners of the runner files and the runner socket file; J1 is `interactive.go`'s owner before C2). See §C.3.
- **Files:** `internal/runner/{touched.go (new), touched_test.go (new), jobs.go, interactive.go}`, `internal/runs/{overlap.go, overlap_store.go (new), overlap_test.go (new)}`, `internal/httpapi/runners/api.go` (case `TypeTouched`), `internal/httpapi/runs/overlaps.go`, `internal/agents/{coordination.go, coordination_test.go}` (new), `internal/analytics/retention.go` + `retention_test.go`, `internal/server/server.go` (`d.SetCoordinationMail(hub.CoordinationNotice)`).

**C3r: Contests and coordinated runs, runner (Wave 2b).** After R1, A1 and **after C2** (both touch `jobs.go` and `interactive.go`). See §C.2.5, §C.4.
- **Files:** `internal/runner/{contest.go (new), contest_test.go (new), workspace_actions.go, global.go, jobs.go, interactive.go, accept.go}`, `internal/runner/adapter/{adapter.go, interactive.go, roles.go, repoconfig.go, coordinated.go (new)}`, `internal/mcp/toolset.go` (`CoordinatedTools`).

### 16.6 Wave 3

**R3: Claude interactive transcript tail.** After R2, A1, C2 and C3r.
- **Files:** `internal/runner/transcript.go` (new) + `transcript_test.go` (new). The single call site goes in `internal/runner/interactive.go`; R3 is its last owner.
- What it does: with `--session-id` known, tail `~/.claude/projects/*/<id>.jsonl` through `claudeParser` into `emit` (redacted). The summary becomes the last assistant text.
- **Tests first:** `TestTailEmitsTextAndToolLines` (fixture jsonl recorded from the installed claude), `TestTailRefusesSymlinkOrForeignFile`, `TestTailStopsAtEnd`, `TestSummaryUsesLastText`.
- After R3, an interactive Claude dialog shows **Screen while running** (prompts live there) and **Steps after the end**.

**D1: User docs.** After Wave 2. **Files:** `docs/workstation.md` (Global agents: follow-ups and PR updates, End and the quiet timeout, groups, busy choices, overlap notices, contests).

**Order:** F1 ∥ F2 → {G1, G2, R1, R2, A1, W1, W2, C3h, CW} → Wave 1b: C1 once G1 and G2 merge, J1 once G1, G2, R1 and R2 merge → {W3, W4, W5, C2} → C3r → {R3, D1}. End-to-end check on the built-in runner (Simple and full mode) after Wave 2, then a team-hub pass with two members (busy, overlap and view-only privacy can only be seen with two).

### 16.7 Files that pass from one package to the next

Every other file in §16 has exactly one owner.

| File | Owners, in order |
|---|---|
| `internal/runner/jobs.go` | F1 (struct fields) → R1 → C2 (watcher start/stop) → C3r (toolset, kind compare) |
| `internal/runner/interactive.go` | R2 → J1 (`CapContinue` in `capabilities()`, after R1 and R2) → C2 (watcher, `CapTouched`) → C3r (`CapCoord`, coordinated prompt, End `NoPR`) → R3 (tail call) |
| `internal/runner/{global.go, workspace_actions.go, accept.go}` | R1 → C3r |
| `internal/runner/adapter/adapter.go` | F1 → C3r (one call to the coordinated section) |
| `internal/runner/adapter/interactive.go` | A1 → C3r (one call to the coordinated section) |
| `internal/runs/dispatcher.go` | F1 (hooks) → G2 |
| `internal/runs/start.go` | F1 (`StartBusy`, `StartError.Busy`) → C1 |
| `internal/runs/workspace.go` | F1 (hub-side `open_pr`) → G1 |
| `internal/runs/{viewonly.go, groups.go, busy.go, overlap.go, contest.go}` | F1 (stubs) → G1 / G2 / C1 / C2 / C3h |
| `internal/agents/runclaims.go` | F1 (stub) → C1 |
| `internal/httpapi/runs/{followup.go, groups.go, busy.go, overlaps.go, contests.go}` | F1 (501 stubs) → G1 / G2 / C1 / C2 / C3h |
| `internal/httpapi/runs/groups.go` | F1 (stub) → G2 → J1 (the group follow-up handler) |
| `internal/httpapi/runners/api.go` | G1 → C2 |
| `internal/server/server.go` | F1 → C2 |
| `web/src/game/warroom/StoryRoom.tsx` | F2 (castArt move) → W5 |
| `web/src/styles/{32-run-dialog.css, 33-pickers.css, 34-coordination.css}` | F2 (empty) → W1 / W2 / CW |

`web/src/App.tsx`, `web/src/api/ws.ts`, `web/src/game/labels.ts`, `web/src/runs/RunWorkspaceDialog.tsx` and `web/src/copilot/draft/DraftPresenter.tsx` are not changed by this work.

### 16.8 Build gate

Per package, for what it touched; in full at the end of every wave:
- `gofmt -l .` (empty), `go vet ./...`, `go build ./...`;
- `go test ./...`, with `TMPDIR=/private/tmp/nodevas-realtmp` on macOS (Go lives in `~/.local/bin`; the default `/var` temp dir breaks path tests);
- `cd web && npx tsc -b`, `npm run check:api-imports`, `npx vitest run`;
- desktop tests (`npm --prefix desktop test`);
- Playwright e2e at the end of every wave (W3 and C1 change what `runs.spec.ts` sees);
- at the very end, CI's full list as well: the `nosqliteimport` build, `govulncheck`, `vite build`, `npm audit`, the embedded-PG matrix.

**Live CLI smoke tests** (§16.2 item 3) gate the merge of R1, R2, A1 and W1: the composer submit (text, then a delayed CR, or bracketed paste), the quit sequence, the idle repaint, the menu markers, trust and sign-in screens, and the resume flags of each installed CLI.

---

## 17. Out of scope (v1)

- Shape A (one session over N repos).
- Follow-ups on node runs.
- A general "open PR later" workspace action. `open_pr` exists only for a contest winner (§C.4.4).
- Undrafting a PR (`gh pr ready`) after a follow-up.
- Live mid-turn input into a background run (claude `--input-format stream-json`, agy stream-json, repeated ACP `session/prompt`). These flags exist and are the next step, giving "talk while it works" with step lines.
- Structured transcripts for interactive agents other than Claude.
- Theme-manifest fields for dialog art, terminal colours or runner pictures.
- Picture cards for the autopilot, roles and routing pickers.
- Groups that span runners; a hub-side group accept; a per-runner lane (answer 8).
- An automatic worktree sweep.
- Asks or a permission relay for background global runs.
- Gemini resume, until a live smoke test passes.
- Adopting a running holder into a contest (§C.0.9).

---

## 18. Decisions

The owner answered on 2026-11-18, in two rounds. These are final for this build.

### 18.1 Round 1: the eight questions

| # | Question | Answer | Where it lands |
|---|---|---|---|
| 1 | Simple mode default | **Interactive**, the same as full mode | §0.3, §8 |
| 2 | Quiet timeout | **20 minutes for everyone**, Simple included; it ends as "done, keep the work". No setting, no "0 = off" | §7.2 (R2 drops `TestQuietZeroIsOff`) |
| 3 | Group cap and over budget | Cap 30 repos; over budget, **refuse the whole group** (no partial start) | §10.2 |
| 4 | Folder art | Default taken: the pack's `rooms` still by a stable hash of the repo id when a pack is active; a plain folder card otherwise | §9, §11 items 3, 7, 14 |
| 5 | Follow-up on a run with an open PR | **Push to the same branch so the PR updates** (v2, not the v1 refusal); handle a PR under review being changed | §4, §6.3, §6.5 (the `HAS_PR` refusal, `ph.hasPr` and `TestFollowUpRefusesOpenPR` are gone) |
| 6 | Group members' prompt | Default taken: list the sibling repos ("one of N services changing together"). Done through `BuildPrompt`'s extra argument, so no `Job` field is needed | §10.2 |
| 7 | Gemini resume | Not a decision: a live smoke test per CLI before R1/A1 merge. Gemini ships without resume until then | §6.6, §16.2 |
| 8 | Per-runner lane | Default taken: none in v1; rely on `maxConcurrent`. The UI shows the queue | §10.2, §17 |

### 18.2 Round 2: after the verified read and the §C design

| Topic | Answer | Where it lands |
|---|---|---|
| Simple + interactive | The raw TUI inside a plain-word frame; runs fall back to background for known blockers: agent signed out (detectable since #17) or folder not trusted | §8.2, §15 `runDialog.simple.*`. Applied: an untrusted folder falls back to background. A signed-out agent cannot run in either mode (the runner declines it, `jobs.go:355-357`), so it starts nothing and Simple shows #17's sign-in help (§8.1, §8.2) |
| Follow-up on an open PR | Never push a failed turn; fast-forward only; refuse when the PR is merged or closed (offer a new run); block a remote merge while a turn is unfinished; **post a PR comment per pushed turn**; do not auto-undraft | §6.3, §6.5, §12, §14 |
| Contests (§C layer 3) | Add `open_pr`, so the winner can become a pull request | §C.4.4, §C.5 |
| Story-room group view (one figure per group, member tabs with repo name) | **Build it now** (world.ts / building.ts carry a repo; not deferred) | §10.2, §11 item 17, W5 |
| Pack has no rooms | Plain folder card | §9 |
| Runner picture | A fixed icon; no manifest change | §0.9, §9, §11 items 5, 16 |
| Cross-member file overlap | Says only "some of the same files"; no agent mail | §C.3.2 |
| Queue vs continue | Queue = a fresh run; "continue its work" only for your own global run | §C.0.6, §C.0.7 |
| Gemini | Without resume until a live smoke test | §6.6 |
| End | Graceful quit, a grace period, then kill | §7.1 |
| End on a view-only run | Only the runner owner may End | §7.1, §1 |
| Quiet end while a prompt waits | Reported differently | §7.2 |
| Build order | Merge #16 and #17 first; fix this plan (28 stale claims, migrations renumbered after 0042, §C appended); live CLI smoke tests; then Wave 0 → 3 | §16 |

Coordination: 「三層處理沒問題」 (the three-layer approach), designed in §C.

---

## 19. Corrections folded in since the first draft

The first draft was written against `21169dd6`. A verified read against the code found these; each is applied in the section named.

| # | The first draft said | The code says | Applied in |
|---|---|---|---|
| 1 | Migrations 0040 / 0041; edit `postgres_only_test.go:127-128` | 0040 and 0041 are taken (`postgres.go:1221, 1231`), #17 took 0042 (:1247); the want list is at `postgres_only_test.go:126-129` | §13 (0043, 0044, 0045) |
| 2 | Follow-up refuses an open PR (`HAS_PR`) | Answer 5 replaces it | §4, §6.3, §6.5, §12, §15, §17 |
| 3 | A follow-up after a usage limit sets `RetryAt = ResetAt` | `ResetAt` is not stored (`store.go:88-93, 835`); global runs never wait (`global.go:21-22`, `dispatcher_routing.go:49-51`) | §6.3, §6.7, §13 (`reset_at`) |
| 4 | A member ending with `usage_limit` sets `held_until` | The kinds are `quota` and `rate_limit` (`runnerwire/routing.go:70-71`); `afterEnd` returns early for global runs; nothing pumps when a hold expires | §10.2 (hold in `Finish`; `releaseGroupHolds` on the `RunRetries` tick) |
| 5 | Pump re-checks the budget and cancels with `BUDGET` | `Used.Runs` counts queued members (`budget.go:94-108`), so `Exceeded != ""` would cancel the whole group; no cancel-code column | §10.2 (cost and tokens only; `failure_code = 'budget'`) |
| 6 | One transaction inserts the group and N runs | `store.Create` takes no tx and reads back through `s.Get` (`store.go:281-388`); one writer (`postgres.go:82`) | §10.2, F1 (`createWith`, `createPipelineWith`) |
| 7 | `run_groups` rows are pruned with their last member | `analytics.Prune` never deletes runs (`retention.go:73-101`) | §10.2, §13 |
| 8 | Sibling repos need a small `Job` field | The hub builds `JobPrompt` through `BuildPrompt`'s extra (`global.go:154`, `start.go:616-628`) | §10.2, §18 |
| 9 | Runner shown as the pack's `runners` place still | Places are text only (`themeManifest.ts:86-87, 144`); the runners place has `groups: []` (`storyPlaces.ts:127`) | §0.9, §9, §11 (fixed icon) |
| 10 | Group members share a figure and already group as one | The group key is `character/sceneName` (`storyPlaces.ts:72-73`, `themeCast.ts:102`); actors have no repo (`world.ts:101-151`, `building.ts:270`) | §10.2, W5 (owns `world.ts`, `building.ts`) |
| 11 | Cards draw frame 0 as a CSS background | No error event on a CSS background; broken state is per hook (`themeCastContext.ts:62-66`); `still()` scales the whole scene; `DraftPresenter` (#13) already uses `<img onError>` | §9 |
| 12 | `.px-story-said` gets `max-height: min(52vh, 520px)` | The cap is on `.px-story-line` (`warroom.css:2178-2180`); the world and `.run-term-host` sizes crowd the figures (`19-run-terminal.css:155-157`) | §3.4 (compact terminal, Open large) |
| 13 | "Open large" opens the dialog in a `GameDialog` | `GameDialog` swallows Escape in capture phase (`GameActions.tsx:328-334`) and is portaled without `--story` vars (:341) | §3.1, W1 owns `GameActions.tsx` |
| 14 | End by kill gives succeeded; End refused for admins | Kill is a hard tree kill (`pty.go:86-91`, exit -1); an early End is lost (`interactive.go:175-185, 270-272`); `mayType` excludes only agents (`httpapi/runs/terminal.go:67-69`) | §7.1 |
| 15 | The hub stores `view_only` and sends readonly | `canType` is fixed at attach and cached (`runs/terminal.go:73-86`; `httpapi/runs/terminal.go:117, 179`); `Accept` is in `dispatcher.go:408`, which G2 owns | §7.3 (mutable relay; hook in F1, body in G1's `viewonly.go`) |
| 16 | Default rule inputs | The rule left out `AllowRemoteTyping` (`runnerwire/remote.go:31-33`) | §8.1 |
| 17 | The composer sends `text + "\r"` | `sendInput` can return false (`api/terminal.ts:169-176`); TUIs read a burst as a paste; keys can pick menu options | §3.1, §16.2 (smoke test) |
| 18 | toolLine tests take names from the fixtures | Fixtures hold CLI-native names; stored names come from the parsers (`codex.go:322-339`; `gemini.go:292` bare); codex `web_search` is a plain string | §5 |
| 19 | Execute reuses the folder; publish runs unchanged | Publish would push failed turns, re-push no-ops, delete the root branch and double-watch (`publish.go:50, 70-75, 82-88`; `changes.go:150-153, 187-190, 455-458`); `ownBranchLocked` needs a NodeID (`review.go:144`) | §6.5, R1 |
| 20 | codex resume: drop `-s`, sandbox via `-c` (codex 0.156.0) | The installed CLI is 0.149.1; `exec resume` also rejects `-C` and `--color` (`codex.go:63-70`) | §6.6 |
| 21 | Parsers already give the session id | Each parser only writes it into a log line (`claude.go:395`, `codex.go:206`, `gemini.go:201, 281-283`) | §6.6 (`SessionReporter` is new work) |
| 22 | Copy `NoteFirst` for `UserFirst` at seq 0 | `NoteFirst` (`runs/routing.go:460-467`) uses `ON CONFLICT DO NOTHING`; a stage-override note would collide | §6.3 (global-only, pinned by a test) |
| 23 | Remove the `noAsk` chip and rewrite `globalAgents.noAsk` | The key also feeds `game/labels.ts:142` and `RunWorkspaceDialog.tsx:372` | §8.3, §15 (new `replyNote`) |
| 24 | F2 changes `StoryRoom.tsx` imports only | The move deletes `still`, `Face`, `skinVars` and `FACE_*` (`StoryRoom.tsx:69-73, 102-137, 188-200`) | §9, F2 |
| 25 | File sets are disjoint | `api/index.ts`, `App.tsx`, `retention.go`, `runner/accept.go`, the i18n tests, `web/e2e/runs.spec.ts`, `docs/workstation.md` had no owner; `warroom/office.ts` does not exist; `OfficeStylePicker` is in `components/settings/` | §16 (owners given; `App.tsx` needs no change) |
| 26 | DispatchDialog sets `touched.current.agent` / `.model` | There is no `.model` key (`DispatchDialog.tsx:223`) | §9 |
| 27 | F2 adds an i18n parity check | No global test; Simple strings in the draft used banned words (`picker.defaultModel` "CLI", `ph.queued` "runner", `ph.hasPr` "pull request") | §15 (families; `*.simple.*` keys) |
| 28 | Per-package gate: `go test` or vitest + `tsc --noEmit` | CI runs much more; macOS needs a real `TMPDIR` | §16.8 |

**Second review (2026-11-18), also folded in:** `runs_contest_shape` used `mode = 'headless'`, but `runs.mode` takes only `''` and `'interactive'` (`postgres.go:650`), so it is `mode = ''` (§C.5); a declined follow-up kept the one-continuation slot for good, so "withdrawn" now covers `cancelled` and `declined` with `started_at = ''` (§6.3, §13); the three new decline codes would have been stored as `other` by `CleanDeclineCode` (F1 now adds them); the group follow-up route and `CapContinue` moved to J1 (Wave 1b), and C1 now waits for G2 as well; `QueuedBehind` is G1's alone; the hub side of `open_pr` and `StartBusy` went to F1; the group-member line in `describeOffer` was dropped (no `Job` field); a signed-out agent cannot run in either mode, so it blocks the start instead of falling back to background (§8.1, §8.2); the PR check compares origin with `pushedHead`, and a refused push travels as `RunResult.PushRefused` (§6.5); groups offer only `both` and `skip` (§C.2.2); multi-run starts read the budget inside the transaction (§C.2.3); citations re-checked on `e22700e`.

**Stale since the handoff, also folded in:** Copilot drafts merged (#13); the migration numbers; `runs.draft` and `runs_draft_shape` (`postgres.go:1231-1242`) put F1's columns after `draft`; `CodeStagedNeedsHeadless` moved to `start.go:289-294` (constant `:467-469`); `DraftPresenter` exists; the `--copilot-fab-clearance` contract; `copilotIntent` / `copilotVoiceAutoSend` in `preferences/schema.ts`; #16 and #17 are merged (so §M is fixed and #17's `SimpleRunSummary` is reused); the Web job's `npm audit` red; codex 0.149.1 and claude 2.1.327 installed, gemini still not; the owner's answers (§18); the retired "never needs a terminal" rule (§8.2).

---

## C. Coordination: who does it (three layers)

Approved by the owner (「三層處理沒問題」) and designed here. It was written against `3a942a0` (master `c0aeebe` plus this plan), revised after a skeptic review (§C.12), and re-checked on `e22700e`. Its packages (C1, C2, C3h, C3r, CW) are folded into §16. The person always decides; the team hub shows another member's run only as "someone is on it", only its owner or an admin can stop it, and nothing shows more of their task than Station already does.

**What the code says, beyond the first §C text:**
- **`internal/agenthook/changes.go` does not feed dispatched runs.** It feeds only *external* sessions: a person's own CLI, through `nodevas hook`, recorded by `agents.RecordSessionChange` against a claimed node (`internal/agents/sessionchanges.go:15-19`). Today a dispatched run gives the hub its tool calls only as `tool_call` events (`Tool`, `Input`, cut to 16 KiB), and its files only at the end (`RunChanges`). An interactive run gives none, since the PTY sends no events. Layer 2 therefore needs a new runner signal (§C.3.1).
- **Station already shows every online agent session in a project, to every member** (`internal/httpapi/station/assemble.go:186-196`: ID, name, agent, model, node, `Doing` text and run id, no viewer filter; `station/types.go:62-73`). Only the *run list* is trimmed: another member's runs appear as a count (`BuildingProject.OthersActive`, `runs.Store.CountOthersActive`; 0 for an admin). This design is **deliberately stricter than Station**: busy and overlap answers never name another member, never show their task text, node title or file paths, and never list a run the viewer cannot `canSee`, even when its session is on Station. The security tests compare against both `OthersActive` and `Sessions` (§C.10).
- **"Its owner" is the existing `canSee` set** (`httpapi/runs/api.go:178-182`): the dispatcher (`created_by`), the runner owner, or an admin. Today that set may already cancel; this design keeps it, so nothing is widened.
- **The hub never releases a run's board claim.** `ReleaseClaim` is called only from the HTTP route (`httpapi/node/claims.go:97`); a cancelled run keeps its claim for `DefaultLease` (30 min, `store/claims.go:43`), and every node implement prompt starts with `claim_task` (`adapter.go:216`, `interactive.go`, `roles.go:110`), whose conflict advice is "pick a different task" (`mcp/tools_write.go:176-177`). Anything that lets two runs work one node (queue, take over, both, contests) must handle claims (§C.2.5).
- **The hub has one write connection** (`internal/db/postgres.go:82`, `db.go:46-50`). A transaction holds it until commit, so no other store write may run while a busy transaction is open (§C.2.3).
- **Migration.** §C adds `0045_run_coordination`, after F1's `0043_run_continue` and `0044_run_groups` and #17's `0042_run_failure_code` (§13). Tests assert the name.

---

### C.0 Decisions

1. **Same code for both editions; only visibility differs.** On the single-user hub (actor `local`, built-in runner) every holder is your own, so every choice is offered. On a team hub, another member's run is only a counted "someone is on it". Only `canSee` people may stop it, and an admin may stop anything, as today.
2. **A run "holds" what it writes.** The rule, stated once (§C.2.1 is its only definition):
   - Writing kinds are `kind IN ('', 'test', 'revise', 'plan')` (`JobImplement` is `""`, `internal/runnerwire/routing.go:170`). Plan counts only on a node.
   - **Node start:** held by unfinished writing runs on the same node, from any member, and by your own unfinished **global** runs in the same repository.
   - **Global start:** held by your own unfinished writing runs in the same repository, global or node.
   - Read-only kinds (review, judge, verify, compare) never hold anything and never wait.
   - Runs of the same lineage (pipeline, fallback chain, follow-up chain `work_root`, group, contest) never block each other.
   - Two node runs on *different* nodes of one repository never block each other, whoever started them. Autopilot's `MaxParallel` and a person dispatching several nodes keep working.
3. **Layer 1 is decided in one short transaction.** All validation and the budget check happen first, with no writes. Then one transaction on the writer takes the advisory lock, reads the holders (and, for a multi-run start, re-reads the budget under the `budget:<owner>` lock), and either rolls back (BUSY) or inserts the pipeline row (if any) and the run, reading the run back through the same transaction. Every other write (stopping holders, follow-up delegation, route-pick links, notes, criteria locks, audit, `Submit`) runs after commit or rollback, never inside (§C.2.3).
4. **The default for a person is "ask".** A busy start returns `409 BUSY` with what the viewer may see. The client re-sends it with `onBusy`, one of:
   - `queue`
   - `takeOver`
   - `both`
   - `continue` (only for a global start whose holder is your own global run on the same runner and repo)
   - `skip` (group starts only: drop the busy folders)
   A group start offers only `both` and `skip`.
5. **Automatic starts queue without asking.**
   - Autopilot sends `onBusy: "queue"`.
   - Pipeline steps, fallbacks and usage-limit handoffs are created by the store directly, not through `Start`. Pump holds a *writing* one while a *running or offered* run of another lineage holds its node (`heldByOther`). Read-only steps never wait.
   - §6 follow-ups already wait for their prior.
6. **Queue is first come, first served.**
   - Queue always means a **fresh run**: the person's own agent, model, base and PR choice, its own branch, with `after_run` = the newest unfinished holder (the tail of the queue, waiting runs included).
   - Pump offers a queued busy run only when no unfinished holder of its keys, of another lineage, is offered or running, or is queued with an older lineage (§C.2.3, "Order"). This waits for *every* holder and for the holders' own successors (fallback, usage-limit handoff, next pipeline step, follow-up), and releases queued runs one at a time in the order they were queued.
   - At offer time the hub adds a short carry-over section about the run it queued behind (§C.2.3).
7. **Continue** is a separate, explicit choice, not "queue". It is offered only when the start is global and the holder is your own global run on the same `runner_id` and `repo_id`. It becomes a §6 follow-up of the holder's latest turn (same folder and branch, the holder's agent, model and PR choice, which the BUSY answer shows). It runs after the busy transaction, through G1's `FollowUp` with `QueuedBehind: true`. It inherits every §6.5 rule for a work with an open PR.
8. **Take over** stops the holders the person saw, then starts the new run as a queued busy run (`after_run` = the stopped holder), so it begins once the stops have landed and the claims are released.
   - "Stop" means End (keep the work; G1's `TypeEnd`) for a running interactive run the actor may End. On a **node** take-over the End carries `noPR: true`: the holder's branch is kept and no pull request is opened for it, so the person never gets two competing PRs.
   - Otherwise it means Cancel, plus `CancelPipeline` when the holder is in a pipeline, exactly as the cancel route does.
   - The request must name the holder ids the person saw (`holderIds`). If the set has changed, the answer is `409 BUSY` again, so nobody stops a run they were never shown.
9. **Both** starts anyway, and Layer 2 watches the files. There is no adoption of a running holder into a contest in v1; a contest is started as one (§C.4).
10. **Two runs on one node never fight over the board claim.** A busy-started node run (queue, take over, both) is offered with `Job.Coordinated` when, at offer time, the node is done or claimed by another session; contest entries always are. A coordinated run gets a prompt and a toolset without `claim_task`, `release_task`, `set_node_status` and `create_subgraph`. When a holder is stopped by take-over, or ends while a busy run waits on its node, the hub releases that holder's claim (§C.2.5).
11. **Layer 2's signal comes from the runner.**
    - The runner reports the repo-relative paths each active run has touched (`touched` envelope), using `git status` in the worktree plus, for headless runs, the edit tools' paths. Lockfiles and generated files are never sent.
    - The hub intersects them per repository and records one overlap per run pair.
    - **Runner-reported paths are untrusted whenever they concern another member.** A member can run their own runner and claim any paths. So an overlap with another member's run carries no paths and no count, and never mails that member's agent (§C.3.2, §C.10).
12. **Who gets told about an overlap.**
    - **People:** always, in the UI.
    - **Node runs (they have a board), own pairs only:** agent mail from the hub, once per pair, on the first shared path that matters (§C.3.2).
    - **Global runs (NoBoard, no mail):** never told mid-run by a new channel. §14 says "no new input path into a running agent", and this design keeps that. Instead:
      - *Interactive:* the dialog offers "Tell it". This pre-fills the person's own composer, and the person sends it through the existing terminal socket and gates.
      - *Background:* it pre-fills a queued §6 follow-up.
      - *Every later follow-up* of that work gets the overlap in its prompt.
13. **Layer 3 (比稿) is a contest.**
    - Two or three headless entries run on the actor's own runner(s), each in its own worktree with `NoPR` forced, no fallbacks, no usage-limit wait.
    - Entries are coordinated runs (C.0.10), so two entries never fight over `ALREADY_CLAIMED`.
    - An optional reviewer runs a new read-only job kind `compare`. It reads the entries' stored diffs, **redacted by the hub**, under anonymous labels (A/B/C) and returns JSON scores.
    - The person picks one. The winner is applied (merge to the local folder, open a PR, or keep). The others are discarded through the existing `discard` workspace action.

---

### C.1 Editions

| | Single-user (built-in runner, actor `local`) | Team hub |
|---|---|---|
| Busy holders shown | all, full details (they are all yours) | your own (and, for an admin, everyone's) with details; anyone else's only as a count, "someone else is working on this" |
| Queue | yes (a fresh run after it) | yes, behind anyone's run (the queued run waits; it learns nothing of theirs at offer time unless it is yours, §C.2.3) |
| Continue its work | yes, for a global start behind your own global run on the same runner and repo | the same |
| Take over | yes | only if every holder is one you `canSee` and there is no hidden holder. An admin may take over anything. A non-admin facing a hidden holder gets no button and the text "ask its owner or an admin" |
| Run both | yes (with `maxConcurrent` 1 the runs still go one after another; the UI says so) | yes |
| Same-file notice, people | yes, with paths | yes. With your own runs: paths. With another member's run: only "another person's work also changes some of the same files", with no path, count, name, node, agent or task |
| Same-file notice, agents | node runs by mail (sender `nodevas`, session `coordination-<run8>`); global runs as in C.0.12 | own pairs only. Another member's agent is never mailed about your run |
| Overlap scope | your runs in the same repo | same project (node runs, any member, active only), or your own runs in the same repo (global and node). Two members' global runs are never compared: they share no project, and comparing them would tell one member about the other's private work |
| Contest | yes, on the built-in runner (with `maxConcurrent` 1 the entries run one after another) | your own runner(s) only, like any run. Accepting the offers stays on the runner (console, auto-accept or app ask), entry by entry; there is no hub "accept all" |
| Pick, apply, discard | `local` | the contest creator, who is every entry's runner owner. Discard and merge go through the existing owner-only workspace actions (`ownerOnly`, runner-side owner check) |

---

### C.2 Layer 1: first come, first served

#### C.2.1 Holders

`internal/runs/busy.go` (C1) holds the rules; `busy_store.go` reads the rows through the transaction it is given:

```go
type BusyQuery struct {
    ActorID   string
    IsAdmin   bool
    Project, NodeID string // node start; "" for global
    RepoKey   string       // always set
    Global    bool
    Exclude   Lineage      // pipelineID, workRoot, groupID, contestID to ignore
}
type Holder struct {           // as stored; trimmed for the reader by Visible()
    Run     Record
    Hidden  bool               // !canSee(actor, run)
    Waiting bool               // queued behind another run (after_run <> '' and not yet offered)
}
func (d *Dispatcher) holders(ctx context.Context, q querier, bq BusyQuery) ([]Holder, error)
```

The rule (this is the only place it is defined):
- **Holder:** an unfinished run (`queued`, `offered`, `running`) of a writing kind (`kind IN ('', 'test', 'revise', 'plan')`; `plan` only in the node check), not in the request's lineage. Runs that are themselves waiting in a queue **are holders**: they are part of the queue, and the busy card shows them as "waiting its turn". A running `run_pipelines` row on the node, with no unfinished run at the moment (between steps), counts as a holder too, shown as its pipeline's last run.
- **Node start, node check:** holders with the same `project` and `node_id`, any member.
- **Node start, repo check:** holders with the same `repo_key`, `is_global`, and `created_by = actor OR owner_id = actor`.
- **Global start, repo check:** holders with the same `repo_key` and `created_by = actor OR owner_id = actor`, global or node.
- Two node runs on different nodes never meet: the node check is per node, and the repo check of a node start looks at global runs only.

`repo_key` is a stable 32-hex value for the repository, computed by `runnerwire.RepoKey(runnerID string, repo Repo) string` (F1):
- with a remote: sha256 of the normalised remote (scheme, userinfo and `.git` stripped, host lower-cased, `git@h:o/r` becomes `h/o/r`);
- without one: sha256 of `local:` + runnerID + `/` + repoID.

The hub stores only the hash, so a remote URL with credentials never reaches the database. Two checkouts of the same remote get the same key, across runners and across members. That is what we want for the repo check (your own global job in that repo, on whichever runner) and for team overlap (§C.3.2). It also means two repo entries on one runner with the same origin share a key (§C.3.3, groups).

#### C.2.2 Request and response

`runnerwire.CreateRunRequest`, `CreateGlobalRunRequest` and the group and contest requests gain:

```go
OnBusy    string   `json:"onBusy,omitempty"`    // "" ask | "queue" | "takeOver" | "both" | "continue" | "skip" (groups only)
HolderIDs []string `json:"holderIds,omitempty"` // takeOver, continue: the visible holders the person saw (≤ 8)
```

A busy start with `onBusy: ""` gets:

```
409 {"code":"BUSY","message":"…en/zh…",
     "holders":[{"runId":"…","title":"Fix login","agent":"claude","model":"opus","mode":"interactive",
                 "status":"running","waiting":false,"since":"2026-11-18T03:12:00Z","by":"Ann","nodeId":"node-0003",
                 "repoName":"api","mayStop":true,"sameNode":true}],
     "others":1,
     "choices":["queue","both"],
     "continue":{"runId":"…","agent":"claude","model":"opus","openPR":false}}
```

- `holders` lists only runs the viewer can `canSee`, at most 8, oldest first.
- `others` counts the rest. It never exceeds what Station's `othersActive` would say, and it carries nothing else.
- `choices` is computed by the server:
  - **a group start offers only `both` and `skip`**, never `queue`, `takeOver` or `continue` (§C.2.4); the rest of this list is for single-run starts (node, global, contest);
  - `queue` always (on a node start: only when the chosen runner advertises `CapCoord`, §C.2.5);
  - `both` (on a node start only with `CapCoord`);
  - `takeOver` only when `others == 0` and `mayStop` holds for every listed holder;
  - `continue` only for a global start whose holders are all the actor's own global runs of one `work_root`, on the same `runner_id` and `repo_id` as the start. The `continue` object then shows the holder chain's latest turn and the agent, model and `openPR` the follow-up will use (the holder's, not the form's).
- Fields per holder:
  - `title` is the node title, or the global run's title. These are your own runs, and admins already see all runs.
  - `since` is `started_at`, falling back to `created_at`.
  - `mayStop = canSee && (actor is admin || running interactive and actor ∈ mayType || ordinary cancel rights)`, which today is `canSee`.

The other answers:
- `onBusy: "queue"`: 201 Run, with `afterRun` set.
- `onBusy: "continue"`: 201 Run, the follow-up, with `continueOf` set; 409 BUSY when `holderIds` no longer matches or the `continue` condition no longer holds.
- `onBusy: "takeOver"`:
  - 403 `NOT_YOURS_TO_STOP` when a hidden holder exists or `mayStop` is false;
  - 409 `BUSY` when `holderIds` differs from the visible holder set;
  - otherwise 201 `{run, stopped:[runId]}`, the run with `afterRun` = the newest stopped holder.
- `onBusy: "both"`: 201 Run.
- `onBusy: "skip"` (group start): 201 `{group, runs}` for the folders that were not busy; the busy ones are left out, and the budget counts only those that remain.
- A bad `onBusy` value, or one the start does not offer (`skip` outside a group; `queue`, `takeOver` or `continue` on a group): 400 `INVALID_ARGUMENT` with field `onBusy`.

Preflight routes let the forms show the note *before* the click. They only read (reader pool), and they call the same `holders` function:

```
GET /api/runs/busy?runnerId=&repoId=[&nodeId=]   (?project=) -> {holders, others, choices, continue?}
GET /api/global-runs/busy?runnerId=&repoIds=a,b,c              -> {repos:[{repoId, holders, others, choices, continue?}]}
```

Both are `personOnly`; the global one is for your own runner only. Without `nodeId`, the node route answers the repo check alone, so a caller about to create a node (the copilot's `start_breakdown`) can ask first.

#### C.2.3 Hub flow

**One decision function, called by every start.** C1 owns it in `busy.go`; F1 stubs it to "proceed, nothing to do":

```go
type BusyRequest struct { Query BusyQuery; OnBusy string; HolderIDs []string; RunnerCaps []string }
type BusyOutcome struct {
    Proceed  bool       // insert the run
    AfterRun string     // set on New
    Choice   string     // stored as runs.busy_choice
    Stop     []Holder   // take over: stop these after commit
    Audit    *AuditEntry
}
func (d *Dispatcher) applyBusy(ctx context.Context, tx *sql.Tx, br BusyRequest) (BusyOutcome, error) // in the tx
func (d *Dispatcher) afterBusy(ctx context.Context, actor auth.Actor, o BusyOutcome)                  // after commit
func (d *Dispatcher) continueBusy(ctx context.Context, req StartGlobalRequest) (Record, error)       // before any tx
```

`Start` (start.go, C1), `StartGlobal` and the group start (global.go and groups.go, G2) and the contest start (contest.go, C3h) all follow the same shape. Only `busy.go` decides; the callers only call.

1. **Before any transaction.** Every existing check runs first, unchanged: runner, hello, node, workflow, budget, prompt. Nothing is written. The `New` (and `NewPipeline`, if any) is fully built in memory. A single-run start's budget check stays here, as today (two concurrent single starts can each pass it, which is today's behaviour too).
   - `onBusy == "continue"` is handled here: `continueBusy` re-reads the holders on the reader, checks `holderIds` and the `continue` condition, and calls G1's `FollowUp(prior = latest turn, QueuedBehind: true)`. `QueuedBehind` skips only the §6 `RUN_LIVE` check. No busy transaction is opened.
2. **The busy transaction** (`d.store.db.Tx`, the writer):
   1. `pg_advisory_xact_lock(hashtextextended('busy:'||key, 0))` for the node key `project/node` and the repo key, in sorted order. With one writer connection this adds nothing today; it keeps the check correct if the write pool ever grows, and it is harmless.
      **A multi-run start** (a group, or a contest's entries plus reviewer) also takes `budget:<owner>` through the same `lockBusy` and reads `BudgetStatus` through `tx`; when `Used.Runs + n > DailyRuns` it rolls back with 429 `{room}` (answer 3's "refuse the whole"). So two concurrent groups, or two concurrent contests, can never each overshoot by n.
   2. `o := d.applyBusy(ctx, tx, br)`: holders are read through `tx`. If there are none, or `onBusy == "both"`, `o.Proceed`. If `onBusy == ""`, it returns `&StartError{Code: StartBusy, …}` and the transaction rolls back with nothing written. Queue and take-over set `o.AfterRun` to the newest holder (waiting holders included, so a new queued run goes to the tail).
   3. If `o.Proceed`: `createPipelineWith(ctx, tx, NewPipeline)` when the start has a pipeline, then `createWith(ctx, tx, New{…, PipelineID, AfterRun: o.AfterRun, BusyChoice: o.Choice})`. `createWith` reads the row back with `scanRun(tx.QueryRowContext(…))`, never through `Get` (the reader cannot see an uncommitted row). The merge and workspace-action attachments are empty on a new run.
   4. Commit.
   Nothing else happens in the transaction: no `FollowUp`, no `EndPipeline`, no `Transition`, no `NoteFirst`, no audit, no runner message. Each of those uses the writer and would wait forever for the connection the transaction holds.
3. **After commit:** `d.afterBusy(ctx, actor, o)` stops each `o.Stop` holder (End with `noPR` on a node, or Cancel plus `CancelPipeline`; C.0.8) and writes the audit; then the existing tail of `Start` runs unchanged (criteria lock, route-pick link, override note, `Submit`). A failed criteria lock cancels the run and ends its pipeline, as today.

Audit: `run.busy {choice, holders:[ids visible], others:n}`, plus `run.takeover {stopped:[ids]}`. Task text is never audited.

**Order (Pump).** F1 adds two skips to `Pump` (dispatcher.go), each calling a C1 function that F1 stubs to `false`:
- `d.waitsBehind(ctx, r)` for a queued run with `busy_choice IN ('queue', 'takeOver')`: true while any holder of r's keys (the same node and repo checks as at its start), of another lineage, is
  - `offered` or `running`; or
  - `queued` with an older lineage: its lineage start (the pipeline's `created_at`, the root of its `retry_of` chain, its `work_root`'s `created_at`, or its own `created_at`) is before r's `created_at`; or
  - a running `run_pipelines` row on r's node created before r.
  This is a strict order (a run waits only on running runs or on strictly older lineages), so it cannot deadlock. It waits for every holder, not just the one named in `after_run`. And because a holder's fallback, usage-limit successor, next pipeline step and follow-up belong to the holder's lineage, which is older, a queued run never cuts into the middle of someone else's staged work.
- `d.heldByOther(ctx, r)` for a queued *writing* run with `origin` in `pipeline` or `handoff`: true while an offered or running writing run of another lineage holds its node. Read-only steps (review, judge, verify, compare) are never held.

`after_run` itself is the queue's display link ("waiting behind …") and the source of the carry-over text. It does not decide when the run starts; `waitsBehind` does.

Also:
- **Release.** F1 changes `afterEnd` to `defer d.ended(ctx, r)` right after its `RunFinal` check, before the global early return, so the hook runs on every path and *after* `fallback`, `waitForReset` and `advance` have created any successor. `ended` (F1, `coordhooks.go`) is a fixed list: `d.busyEnded(ctx, r)` (C1, busy.go) and `d.contestEnded(ctx, r)` (C3h, contest.go). `busyEnded` Pumps every runner that has a queued busy run on r's node key or repo key, and releases r's board claim when §C.2.5 says so.
- **Restart gap.** If the hub restarts between an end and the pump, F1 adds one call in `RunRetries` (handoff.go:151) on each tick: `d.releaseWaiting(ctx)` (C1, stubbed), which Pumps runners that have a queued busy run whose `waitsBehind` is now false.
- **Carry-over.** F1 adds one call in `offer()`: `d.coordJob(ctx, r, &job)` (C1, stubbed). For a run with `after_run`, it appends `afterSection(prior)` when the prior is the actor's own: summary (redacted, ≤ 2 KiB), branch, PR URL. For a hidden prior (another member's run) it gets only "Another run on this task finished before yours; check the base branch and open pull requests for its work." It also sets `Job.Coordinated` (§C.2.5).
- **Withdraw** = the ordinary cancel route on the waiting run.

#### C.2.4 What each kind of start does

| Start | Default | Notes |
|---|---|---|
| Person: DispatchDialog (node) | ask | node preflight shows the note; the 409 is the backstop |
| Person: GlobalAgentForm, one folder | ask | global preflight |
| Person: SimpleAgentBox (global, single-user only) | ask | global preflight. In Simple every holder is your own global run in that folder; choices are worded in plain words (§C.8): "Wait, then do mine" is a fresh run after it, "Finish it (its work is kept), then do mine" is take-over (End keeps an interactive run's work), "Do both", and "Add this to its work" (`continue`) |
| Person: group (G2) | ask, but only `both` or `skip` | `skip` drops the busy repos from `repoIds`. Group members are independent tasks, so queueing them behind unrelated work is not offered. The budget check counts only what remains |
| Person: contest (C3) | ask | the contest's own entries never block each other |
| Person: §6 follow-up | no busy check | it is the same work, and it waits for its own prior already. Layer 2 still applies |
| Autopilot (`orchestrator.go:921`, `goalflow.go:369`) | `queue` | one field each. Two autopilot tasks on different nodes of one repo never block each other (C.0.2) |
| Pipeline step, fallback, handoff (store) | implicit Pump wait | writing steps only (C.2.3) |
| Copilot server tools (`start_breakdown`, `revise_goal`) | never choose | they preflight first and, when busy, return the holders to the model as a plain answer for the person, without creating the goal node. Their schema never takes `onBusy` or `holderIds`; the person starts it from the board, where the choice card is (risk R4) |

#### C.2.5 Board claims when two runs share a node

- **Release.** `busyEnded(r)` releases r's live claim on its node when r was stopped by take-over, or when a queued busy run waits on r's node. It calls an injected `d.releaseRunClaim(ctx, project, nodeID, runID)`, which F1 wires in `server.go` to `agents.Hub.ReleaseRunClaim` (C1, `internal/agents/runclaims.go`, stubbed by F1). That finds the run's MCP session (`RunID == runID`) and calls `store.ReleaseClaim(node, owner)` only when the live claim's owner is that session. It never releases a claim held by anyone else. Today's behaviour for runs nobody waits on is unchanged.
- **Coordinated job.** `coordJob` sets `Job.Coordinated = true` for a node run when:
  - it is a contest entry; or
  - `busy_choice = 'both'` (the holder still holds the claim); or
  - `busy_choice IN ('queue', 'takeOver')` and, at offer time, the node is done (the holder finished it) or has a live claim by another session.
  Otherwise the run is offered as usual and claims the node normally.
- **Runner side (C3r).** A coordinated job gets `nodevas mcp --toolset coordinated` (`RunTools` minus `claim_task`, `release_task`, `set_node_status`, `create_subgraph`) and the prompt section: "Another run worked on, or is working on, this task. Do not claim it or change its status; read it with get_node. Work only in your worktree. Finish with a short summary; the person updates the board." It replaces rule 1 and the `set_node_status` part of rule 4 in `adapter.go:216-224`, and the same steps in the interactive prompt (`adapter/interactive.go:52`) and the role prompt (`adapter/roles.go:110`). The node's status is left to the person (risk R7).
- **Old runners.** `Job.Coordinated` needs `CapCoord`. On a node start whose runner lacks it, `choices` offers only `takeOver`. A stopped holder's claim is released and the node is not done, so the new run claims it normally. The note says "Update nodevas on this computer to run both or wait in line".

---

### C.3 Layer 2: same files caught early

#### C.3.1 Runner signal (wire)

`runnerwire/coordination.go` (F1):

```go
const CapTouched = "touched"          // the runner sends touched for its active runs
const TypeTouched = "touched"         // runner -> hub
const MaxTouchedFiles = 500           // per run, total
const MaxTouchedPerMessage = 200
type Touched struct {
    Files   []string `json:"files"`             // repo-relative, slash form, validPath (no .., no abs), ≤ 1 KiB each
    AddOnly []string `json:"addOnly,omitempty"` // subset of Files whose diff so far only adds lines
    More    bool     `json:"more,omitempty"`    // the runner stopped listing at MaxTouchedFiles
}
// Envelope gains Touched *Touched `json:"touched,omitempty"` (with RunID).
```

Runner (`internal/runner/touched.go`, new, C2):
- While a run is running, a watcher calls `git status --porcelain=v1 -z --untracked-files=all` in its worktree (4 s timeout, `GIT_OPTIONAL_LOCKS=0`):
  - every 30 s;
  - for headless runs, also 3 s after a `tool_result` event (debounced);
  - and once at the end.
- For headless runs, edit-tool paths (`agenthook.EditedFiles` + `RepoRelative(wt.Path)`, a new import of `internal/agenthook` from `internal/runner`, which creates no cycle) are added at once, so a file shows up before the next status poll.
- **Interactive runs** (the default now) send no events, so they rely on the poll alone: news arrives within 30 s, or later once the poll has backed off. R3's transcript tail (Claude only) may add edit-tool paths later; v1 does not depend on it.
- **Dropped on the runner, before sending**, so one stray path never fails a whole message:
  - any path that is not repo-relative (`RepoRelative` returns a cleaned *absolute* path for a file outside the worktree, `agenthook/changes.go:150-155`), contains `..`, or is over 1 KiB;
  - secret paths (`agenthook.SecretPath`, exported by F1);
  - the built-in ignore set: `go.sum`, `package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`, `bun.lockb`, `Cargo.lock`, `poetry.lock`, `uv.lock`, `Pipfile.lock`, `composer.lock`, `Gemfile.lock` (any directory), and paths that `git check-attr -z --stdin linguist-generated` marks set.
- `AddOnly` comes from `git diff --numstat -z HEAD -- <paths>` on the same tick (deletions 0, and untracked files count as add-only). A path is re-sent once if it later stops being add-only.
- Only *new* paths (or a path's add-only change) are sent, and each only once. The hub's `validPath` still checks everything; the hub drops a bad path rather than refusing the message.
- If one status call takes over 2 s, the interval doubles, up to 5 min.
- Interactive runs get the same watcher, started from `interactive.go`.
- Global runs are included: NoBoard does not matter here, because the runner sends this, not the agent.

#### C.3.2 Hub storage and overlap

`internal/runs/overlap.go` and `overlap_store.go` (C2):
- `Dispatcher.Touched(ctx, runnerID, runID, t)`:
  - the run must be this runner's and running;
  - each path is validated (`validPath`) and capped, and invalid ones are dropped;
  - rows are upserted into `run_touched_files`: `ON CONFLICT (run_id, path) DO UPDATE SET add_only = run_touched_files.add_only AND EXCLUDED.add_only`;
  - then `d.findOverlaps(ctx, r, newPaths)`, which also retries any of r's pairs whose mail is still owed (C.3.3).
- At `Changes` (dispatcher.go:518), F1's one-line hook `d.changesTouched(ctx, r, changes.Files)` (C2, overlap.go) adds the committed files, in case the watcher missed some.
- **Scope of `findOverlaps`.** Other runs B whose `repo_key` equals r's, `B.id ≠ r.id`, not in r's pipeline, fallback chain, `work_root` or contest (group siblings *are* compared: two repo entries with one origin share a key), and either:
  - **own:** `B.created_by = r.created_by` or `B.owner_id = r.owner_id`, and B is active (`queued`, `offered`, `running`) or a kept result not applied: succeeded within 24 h, `workspace = ''`, `pr_url = ''` (a result with a PR is left out: the PR is its signal); or
  - **another member's:** same project, both node runs, B active only.
- **What a shared path is worth.**
  - *Strong:* a shared path that is not add-only on both sides.
  - *Weak:* add-only on both sides (two runs appending keys to `web/src/i18n.ts`). It is shown in the list but triggers no notice and no mail.
- **Overlap row.** `run_overlaps (run_a < run_b)`:
  - `paths` is a JSON array of the first 20 shared paths, strong ones first, plus `path_count` and `strong_count`;
  - `cross_member` is set when the two runs belong to different members;
  - it is upserted, with `first_at` kept and `updated_at` bumped.
- **Notify** when `strong_count` first becomes ≥ 1, and again when it crosses 5 and 20 (own pairs only; a cross-member pair is announced once):
  - node rows get the existing `changed(project, nodeID)`, which emits `runs-changed` as today; the client refetches overlaps (`changed` has no kind parameter, and none is added);
  - global rows get `tellGlobal(GlobalRunsChangedEvent, r)`, which reaches the dispatcher and the owner only;
  - then mail (C.3.3) for own pairs.
- `Run` (wire) gains `overlapCount int` (number of pairs with `strong_count ≥ 1`), read as one extra `COUNT` subquery in `runColumns` (F1, store.go).
- **Cross-member pairs and untrusted input.** The other runner's paths are taken as reported, and a member can run their own runner. So for a cross-member pair:
  - the answer for either side is only `{hidden: true, since}` (no paths, no count, no strong/weak), and the other side's run is never named;
  - the pair is announced to people once, and never moves (no thresholds);
  - no agent is mailed about it.
  At most one bit per pair ("some file in common") reaches the other member. To guess paths, a member would need one run per guess, each charged to their budget. This is written down as accepted in §C.10 and R12.

#### C.3.3 Telling agents

**Node runs (they have a board), own pairs only.**
- `internal/agents/coordination.go` (new, C2) adds `Hub.CoordinationNotice(ctx, project, runID, body string) error`:
  - it finds the run's own session with `SessionForRun(project, runID)` (a session with `RunID == runID`, from C1's `runclaims.go`);
  - with no session yet, it returns `ErrNoSession`. **There is no fallback to the node's claim owner**, which could be another member's session. The pair keeps `mailed_x = false`, and `findOverlaps` retries on that run's next `touched` message;
  - on `ErrRateLimited`, `mailed_x` also stays false and the next `touched` retries.
- It sends with the unexported `system: true` (no hold), sender actor `nodevas`, agent `nodevas`, and a **session per recipient run**, `coordination-<run8>`. The loop guard's rate key is project + sender (`agents/messages.go:342-357`), so one session per recipient keeps one busy repository from starving the rest. It is not `HubNotice`-stamped: this is the hub's information, not a person's decision.
- At most 5 coordination notices per run.
- Body (English, for agents; titles go through `EscapeTrustMarkers`):
  - **Own pair:** "Coordination: run for node `node-0003` "Fix login" is also changing: `src/auth.ts`, `src/api.ts` (+3 more). If your changes may clash, send_message node:node-0003 to agree who changes what, or keep your edits to these files minimal." Only strong paths are listed.
  - There is no cross-member body.
- The run reads it through `check_inbox`. Existing nudges help: `[You have N unread message(s)]` on network tools, and the hook's inject on its next event where the CLI runs hooks. §M's `RunTools` fix landed in #16, so runs can `list_team`.

**Global runs (NoBoard).** Global runs get no MCP board and no mail (`markNoBoard`, `RunEnv.NoBoard`), and this design does not change that.
- **Person:** the overlap shows in the run's dialog header and list row (C.5).
- **Interactive global run:** the notice has a "Tell it" button. It puts a plain sentence into the dialog's own composer (`RunComposer`, W1): "Another task of yours is also changing src/auth.ts and src/api.ts. Keep changes to them minimal." The *person* presses Enter. It travels the existing terminal socket with `mayType`, `remoteTyping`, the bucket and audit, so no new input path is added.
- **Background global run:** the same button pre-fills the queued-reply composer, giving a §6 queued follow-up that the person sends.
- **Every global follow-up** (G1 `FollowUp`) calls `d.overlapNote(ctx, prior)`, which F1 stubs to `""`. The note goes into the follow-up's prompt section ("Note from Nodevas: …", ≤ 1 KiB, own pairs, strong paths only).
- **Group siblings** usually work in different repositories and never pair. Two repo entries on one runner with the same origin share a `repo_key`, pass G2's `repoIds`-unique check, and *are* compared like any other two runs of yours, because group lineage is not an overlap exclusion.

---

### C.4 Layer 3: deliberate competition (比稿)

#### C.4.1 Shape

- 2 or 3 entries, each a separate run in its own worktree, branch `agent/<node|slug>-<run8>`, `NoPR: true`, headless (`mode = ''`, `runnerwire.ModeHeadless`), `contest_id`, and a contest label A, B or C.
- Entries are on the actor's own runners (one runner or several). Each entry is checked with `CheckAgainstHello` on its runner, which must advertise `CapCoord`.
- **Node contest:**
  - same node, same prompt;
  - refused for staged workflows, pipelines and fallbacks (400 `CONTEST_SHAPE`), because v1 keeps contests to plain implement runs;
  - `Kind: ""` (`JobImplement`).
- **Global contest:** one runner, one repo, the same task; each entry is an ordinary global run.
- **Entries never leave the contest's shape.** F1 adds guards:
  - `canWaitForReset` (handoff.go:39) returns false when `contest_id <> ''`, so a usage limit fails the entry instead of creating a plain successor that would open a PR and claim the node;
  - `fallback` (dispatcher_routing.go) returns false when `contest_id <> ''`;
  - `Create` refuses `Fallbacks` with a `ContestID`.
- **Budget:** `entries + (reviewer ? 1 : 0)` must fit in the room the budget has left. It is read inside the start's transaction under the `budget:<owner>` lock, as for groups (§C.2.3). Otherwise 429 `{room}` and nothing starts (answer 3's "refuse the whole").
- **Start:** one busy transaction (C.2.3) that inserts every entry through `createWith`, and the reviewer later.
- **Entry job:** `Job.Contest = {id, label, of}` and `Job.Coordinated = true`.
  - The adapter prompt adds: "You are entry B of 2 attempts at this task; another agent works on the same task in a separate folder. Work only in your folder. Do not claim the task or change its status. Finish with a short summary of your approach."
  - Node entries get `nodevas mcp --toolset coordinated` (§C.2.5): `RunTools` minus `claim_task`, `release_task`, `set_node_status`, `create_subgraph`. This is `internal/mcp/toolset.go` (C3r).

#### C.4.2 Reviewer (`compare` job kind)

- **When it runs.** Optional. It is chosen at start (`reviewer: {runnerId, agent, model}`) or later through `/judge`. It starts once every entry is final and at least two entries succeeded with a commit; otherwise the contest goes straight to `ready`. `Origin: pipeline`, the same as a pipeline's next round. The runner's acceptance rules apply.
- **Job.** `Kind: "compare"`, headless, read-only:
  - `adapter.ReadOnly` adds `JobCompare`;
  - repo config is stripped as for review runs;
  - the worktree is at base;
  - `NoPR`;
  - for a global contest, global with `contest_id`.
- **Prompt (hub, `internal/runs/compare.go`):**
  - the task;
  - for each entry, under its label only (no agent or model names): summary (≤ 2 KiB), files, +/-, and the stored patch, each cut to `(MaxPromptBytes − 8 KiB) / n`, with the `runDialog.cut` marker text in English;
  - **every patch is cleaned by the hub before it goes in.** Stored patches are raw `git diff` output: the runner redacts the summary, events and PR body, not the patch (`runner/changes.go:313-320`), and the hub's `SetChanges` only truncates. `compare.go` drops the hunks of any file `agenthook.SecretPath` matches, and runs `redact.Output` on the rest;
  - the branch name of each entry, so the reviewer may `git diff` read-only;
  - the order is A, B, C, rotated by contest id, to reduce position bias.
- **Instruction:** "Score each entry 0-10 for correctness, completeness, scope discipline, tests; then pick one. End with a fenced json block: `{"scores":[{"entry":"A","score":7,"why":"…"}],"pick":"A"}`."
- **Parsing.** At Finish, `ParseScores(summary)` reads the *last* fenced json block:
  - labels must be the contest's;
  - scores are ints 0-10;
  - `why` is ≤ 300 runes, run through `redact.Output`;
  - anything else gives `scores: null` with the note "the reviewer gave no scores".
- **The scores are advice only.** Nothing is applied automatically.

#### C.4.3 Contest state

`running → judging → ready → decided`, or `cancelled` from any state before `decided`. It is stored in `run_contests.state`. Transitions happen in `d.contestEnded` (C3h, contest.go; called from F1's fixed `ended` list when an entry or the reviewer finishes) and in the routes.

#### C.4.4 Pick

`POST …/contests/:id/pick {runId, apply: "local"|"pr"|"keep", discardOthers: bool}`:

1. The actor is the contest creator, the contest is `ready` (or `judging`), and the pick is a succeeded entry with a commit.
2. The contest goes to `decided` with `winner_run_id`, `decided_by` and `decided_at`. Audit `run.contest.pick {contest, winner, apply, discarded:[ids]}`.
3. Apply, through existing machinery:
   - `local`: the `merge_local` workspace action on the winner (the existing gates: owner, head SHA, clean base and so on);
   - `pr`: the new workspace action **`open_pr`** (CapCoord). The hub accepts it through F1's additions to `CheckWorkspaceAction`, `CreateWorkspaceAction`, the outcome handling and `workspaceVerb` (only for a succeeded contest entry with a commit and no PR). The runner (C3r) pushes the winner's own branch without force and opens the PR with the existing `openPR`; it refuses when a PR exists or the branch is not its own, and reports the PR URL in `WorkspaceOutcome.PRURL`;
   - `keep`: no action; the existing `ResultActions` stay available on the winner.
4. With `discardOthers`, every other entry gets the existing `discard` action, which refuses `unsaved_commits`. Each refusal is reported in `actions[]`, and that entry stays. Entries never have a PR (they are `NoPR`, with no fallback or usage-limit successor).
5. For a node contest, the node's status is left alone: entries could not change it. The view then offers the board's normal status control (risk R7).

`open_pr` goes beyond the "later open PR action" that §17 left out of scope. It is needed only so that a contest can end with a PR. The owner approved it (round 2), for contests only.

---

### C.5 Data model, migration `0045_run_coordination` (F1)

```sql
-- Layer 1
ALTER TABLE runs ADD COLUMN IF NOT EXISTS after_run TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS busy_choice TEXT NOT NULL DEFAULT ''
    CHECK (busy_choice IN ('', 'queue', 'takeOver', 'both'));
ALTER TABLE runs ADD COLUMN IF NOT EXISTS repo_key  TEXT COLLATE "C" NOT NULL DEFAULT ''
    CHECK (repo_key = '' OR repo_key ~ '^[0-9a-f]{32}$');
CREATE INDEX IF NOT EXISTS runs_after ON runs (after_run) WHERE after_run <> '';
CREATE INDEX IF NOT EXISTS runs_busy_waiting ON runs (runner_id)
    WHERE status = 'queued' AND busy_choice IN ('queue', 'takeOver');
CREATE INDEX IF NOT EXISTS runs_active_node ON runs (project, node_id)
    WHERE status IN ('queued', 'offered', 'running') AND NOT is_global;
CREATE INDEX IF NOT EXISTS runs_active_repo ON runs (repo_key)
    WHERE repo_key <> '' AND status IN ('queued', 'offered', 'running');

-- Layer 2
CREATE TABLE IF NOT EXISTS run_touched_files (
    run_id   TEXT COLLATE "C" NOT NULL REFERENCES runs(id),
    path     TEXT COLLATE "C" NOT NULL CHECK (length(path) BETWEEN 1 AND 1024),
    add_only BOOLEAN NOT NULL DEFAULT FALSE,
    at       TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (run_id, path)
);
CREATE INDEX IF NOT EXISTS run_touched_files_path ON run_touched_files (path);
CREATE TABLE IF NOT EXISTS run_overlaps (
    run_a        TEXT COLLATE "C" NOT NULL REFERENCES runs(id),
    run_b        TEXT COLLATE "C" NOT NULL REFERENCES runs(id),
    paths        TEXT NOT NULL DEFAULT '[]',      -- JSON, first 20 shared paths, strong first
    path_count   INTEGER NOT NULL DEFAULT 0,
    strong_count INTEGER NOT NULL DEFAULT 0,
    cross_member BOOLEAN NOT NULL DEFAULT FALSE,
    mailed_a     BOOLEAN NOT NULL DEFAULT FALSE,
    mailed_b     BOOLEAN NOT NULL DEFAULT FALSE,
    first_at     TEXT COLLATE "C" NOT NULL,
    updated_at   TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (run_a, run_b),
    CHECK (run_a < run_b)
);
CREATE INDEX IF NOT EXISTS run_overlaps_b ON run_overlaps (run_b);

-- Layer 3
CREATE TABLE IF NOT EXISTS run_contests (
    id              TEXT COLLATE "C" PRIMARY KEY,
    owner_id        TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_by_name TEXT NOT NULL DEFAULT '',
    project         TEXT COLLATE "C" NOT NULL DEFAULT '',
    node_id         TEXT COLLATE "C" NOT NULL DEFAULT '',
    is_global       BOOLEAN NOT NULL DEFAULT FALSE,
    title           TEXT NOT NULL,
    entry_count     INTEGER NOT NULL CHECK (entry_count BETWEEN 2 AND 3),
    reviewer        TEXT NOT NULL DEFAULT '',   -- JSON {runnerId, agent, model} or ''
    state           TEXT NOT NULL DEFAULT 'running'
        CHECK (state IN ('running', 'judging', 'ready', 'decided', 'cancelled')),
    judge_run_id    TEXT COLLATE "C" NOT NULL DEFAULT '',
    scores          TEXT NOT NULL DEFAULT '',   -- JSON {scores:[…], pick} or ''
    winner_run_id   TEXT COLLATE "C" NOT NULL DEFAULT '',
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_at      TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at      TEXT COLLATE "C" NOT NULL,
    CHECK (is_global = (project = '' AND node_id = ''))
);
ALTER TABLE runs ADD COLUMN IF NOT EXISTS contest_id    TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS contest_label TEXT NOT NULL DEFAULT ''
    CHECK (contest_label IN ('', 'A', 'B', 'C', 'J'));       -- J: the reviewer
CREATE INDEX IF NOT EXISTS runs_contest ON runs (contest_id) WHERE contest_id <> '';
ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_contest_shape;
ALTER TABLE runs ADD CONSTRAINT runs_contest_shape CHECK (contest_id = '' OR
    (mode = '' AND pipeline_id = '' AND retry_of = ''   -- '' is headless (runnerwire.ModeHeadless; runs.mode allows '' and 'interactive', 0015)
     AND (kind = '' OR (kind = 'compare' AND contest_label = 'J'))));
-- a global contest's reviewer is a global row of kind compare
ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_global_shape;
ALTER TABLE runs ADD CONSTRAINT runs_global_shape CHECK (NOT is_global OR
    (project = '' AND node_id = '' AND pipeline_id = '' AND retry_of = '' AND role = '' AND branch_slug <> ''
     AND (kind = '' OR (kind = 'compare' AND contest_id <> ''))));
-- workspace actions gain open_pr
ALTER TABLE run_workspace_actions DROP CONSTRAINT IF EXISTS run_workspace_actions_action_check;
ALTER TABLE run_workspace_actions ADD CONSTRAINT run_workspace_actions_action_check
    CHECK (action IN ('merge_local', 'discard', 'open_pr'));
```

- F1 checks the real name of the inline CHECK on `run_workspace_actions.action`, which 0036 created unnamed (Postgres names it `run_workspace_actions_action_check`), in `TestMigrationsAddCoordination`.
- `postgres_only_test.go`: add the migration name to the want list (`:126-129` today), after F1's `0043_run_continue` and `0044_run_groups`.
- `internal/legacysqlite/legacysqlite_test.go` (F1): `TestImportTableListCoversPGSchema` (:183-213) fails for any new table that is neither imported nor excluded. Add `run_touched_files`, `run_overlaps` and `run_contests` (and F1's own `run_groups`) to the "came after SQLite was removed" case at :194.
- **Retention (C2, `internal/analytics/retention.go`):**
  - `Prune` returns at once when `days <= 0` and all its steps share one cutoff (`retention.go:79-83`). So the touched-files prune is a separate step with its own fixed 7-day cutoff, run *before* the `days <= 0` return: delete `run_touched_files` and `run_overlaps` rows whose runs finished more than 7 days ago, in batches of `PruneBatch`.
  - `run_contests` rows are kept, like `runs`; `analytics.Prune` deletes no runs.
- **Store (F1, store.go and pipelines.go only):**
  - `New` gains `AfterRun`, `BusyChoice`, `RepoKey`, `ContestID`, `ContestLabel`.
  - `Create` becomes `createWith(ctx, q querier, in New)` plus a thin `Create` that calls it on the writer. `createWith` reads the row back through `q` (`scanRun(q.QueryRowContext(…))`). The same for `createPipelineWith(ctx, q, NewPipeline)` in pipelines.go.
  - `Create` accepts `NoPR` on a node row only when `ContestID != ""`, refuses `Fallbacks` with a `ContestID`, and accepts `Kind: compare` only with `ContestLabel == "J"`.
  - `runColumns`/`scanRun` append `after_run, busy_choice, repo_key, contest_id, contest_label` and the `overlapCount` subquery (`(SELECT COUNT(*) FROM run_overlaps o WHERE (o.run_a = runs.id OR o.run_b = runs.id) AND o.strong_count > 0)`) after F1's own new columns.
  - `Job()` fills `Contest` and keeps `NoPR`.
  - `jobKinds += compare` is in routing.go, which F1 takes as one line.

---

### C.6 API (all `personOnly`; refusals as `locale.Errorf` en/zh pairs)

```
# Layer 1
POST /api/runs                    +{onBusy?, holderIds?}
POST /api/global-runs             +{onBusy?, holderIds?}            (onBusy may be "continue")
POST /api/global-runs/groups      +{onBusy?: "" | "both" | "skip"}
  409 BUSY {holders[], others, choices[], continue?}  (groups: {repos:[{repoId, holders, others}]})
  403 NOT_YOURS_TO_STOP                              (takeOver with a hidden or unstoppable holder)
  400 INVALID_ARGUMENT field onBusy | holderIds
  201 Run (+afterRun | continueOf) | {run, stopped[]}
GET  /api/runs/busy?runnerId&repoId[&nodeId] (&project) -> {holders, others, choices, continue?}
GET  /api/global-runs/busy?runnerId&repoIds            -> {repos:[{repoId, holders, others, choices, continue?}]}

# Layer 2
GET  /api/runs/:id/overlaps        -> {overlaps:[Overlap]}         canSee(run)
GET  /api/global-runs/:id/overlaps -> {overlaps:[Overlap]}         canSee(run), is_global
Overlap = {runId?, title?, nodeId?, agent?, status?, mine:bool, hidden:bool,
           paths:[≤20], pathCount, strongCount, since}
          hidden=true  → only {hidden:true, since:first_at}
Run += overlapCount
Realtime: project room "runs-changed" (id nodeId) as today; global "global-runs-changed" (dispatcher+owner)

# Layer 3
POST /api/runs/contests            ?project=  {nodeId, repoId, base?, prompt?, simple?,
                                    entries:[{runnerId, agent, model}] (2..3), reviewer?:{runnerId, agent, model},
                                    onBusy?, holderIds?}
POST /api/global-runs/contests     {runnerId, repoId, base?, task, slug?, simple?,
                                    entries:[{agent, model}] (2..3), reviewer?:{agent, model}, onBusy?}
  -> 201 {contest, runs[]}
  400 CONTEST_SHAPE (staged/pipeline/fallbacks/interactive/duplicate entry) | field entries/reviewer
  412 RUNNER_OUTDATED (no CapCoord on an entry's or the reviewer's runner)
  429 BUDGET {room}
GET  /api/contests/:id             -> {contest, runs[], compare:{entries:[{label, runId, agent, model, status,
                                       files[], additions, deletions, summary}], scores|null}}
POST /api/contests/:id/judge       {runnerId?, agent, model}  -> 202 {contest}   (409 if not ready or judged)
POST /api/contests/:id/pick        {runId, apply:"local"|"pr"|"keep", discardOthers}
                                   -> 202 {contest, actions:[{runId, action, status, code?}]}
POST /api/contests/:id/cancel      -> {contest, runs[]}
Contest = {id, title, nodeId?, project?, global, state, entryCount, reviewer?, judgeRunId?, winnerRunId?,
           createdBy, createdAt, decidedAt?}
Run += contestId?, contestLabel?
Realtime: no new name. A contest change emits the existing "runs-changed" for a node contest (id nodeId) and "global-runs-changed" for a global one, and the client refetches the contest
```

Who may call:
- **Contest start:** as `Start` / `StartGlobal`. Every runner must be the actor's own; admins get no exception.
- **GET contest:** the creator or an admin, the same rule as `canSee` (creator = dispatcher = owner).
- **judge, pick:** the creator only. Admins are excluded: pick runs workspace actions, which are owner-only on the runner anyway.
- **cancel:** the creator or an admin. "Only the owner or an admin may stop another's run."
- **Overlaps:** `canSee` on the run asked about. The *other* side is trimmed by `canSee` on that run, and a cross-member pair is always `hidden`, even for an admin (the paths behind it are runner-reported and unchecked).

Runner wire (F1, `runnerwire/coordination.go`, plus `terminal.go`):
- `CapTouched`, `TypeTouched`, `Touched`;
- `CapCoord` (it covers `Job.Coordinated`, `Job.Contest`, `JobCompare`, `WorkspaceOpenPR` and `--toolset coordinated`);
- `Job.Coordinated bool`;
- `JobCompare = "compare"`;
- `JobContest{ID, Label, Of}`;
- `WorkspaceOpenPR = "open_pr"`;
- the End message (G1's `TypeEnd`, terminal.go) gains `NoPR bool`: keep the branch, open no PR (node take-over);
- `RunHolder`, `BusyResponse`, `BusyContinue`, `Overlap`, `Contest`, `CreateContestRequest`, `CreateGlobalContestRequest`, `PickRequest`;
- the `OnBusy*` constants;
- `RepoKey()`;
- `MaxContestEntries = 3`, `MaxHolderIDs = 8`.

`checkGlobal` on the runner allows `Contest` and kind `compare` for global jobs (R1's global.go, Wave 2 part C3r).

---

### C.7 Packages and waves

The packages are in §16, with their file lists and the hand-over table (§16.7):
- **Wave 0:** F1 carries the wire (`runnerwire/coordination.go`, `Envelope.Touched`, `Job.Coordinated`, `Job.Contest`, End `NoPR`, the `Run` and request fields), migration `0045_run_coordination`, `createWith(q)` / `createPipelineWith(q)` / `lockBusy`, `StartBusy` and `StartError.Busy`, the hub side of `open_pr`, the `jobKinds` line, the Pump skips and the `offer` / `Changes` / `afterEnd` / `RunRetries` hooks, `coordhooks.go`, the `SecretPath` export, the stubs (`busy.go`, `overlap.go`, `contest.go`, `agents/runclaims.go`, the HTTP 501 files) and the `server.go` line. F2 carries the `busy.*`, `overlap.*` and `contest.*` keys, the two plain-word families, `onBusy` / `BusyError` / busy and overlap calls, `api/contests.ts`, `api/index.ts` and the `34-coordination.css` slot.
- **Wave 1:** G2 restructures `StartGlobal` and the group start into the C.2.3 shape and only calls `applyBusy` / `afterBusy` / `continueBusy`; its usage-limit hold stays in `Finish`. C3h (contests, hub) and CW (web components) run beside G1, R1, R2, A1, W1 and W2.
- **Wave 1b:** C1 (busy, hub) once G1 and G2 have merged, because it calls the real `FollowUp` (with G1's `QueuedBehind`) and `End`, and its server tests drive G2's `StartGlobal`. J1 (the group follow-up route and `CapContinue`) runs beside it.
- **Wave 2:** C2 (touched files and overlap) beside W3, W4 and W5, which mount the CW components: W3 the busy flow and the 比稿 toggle (not in Simple) and the e2e busy case; W4 the contest rows, overlap badge, "waiting behind" line and the `ResultActions` lock; W5 the `OverlapNotice` and contest pairs. W1 already ships `RunComposer.prefill`.
- **Wave 2b:** C3r (contests and coordinated runs, runner), after C2, because both touch `jobs.go` and `interactive.go`.
- PR #16 (agent mail) is merged, so C2's mail and C3r's toolset have what they need.

---

### C.8 Web and i18n (`web/src/i18n.ts`, en + zh-TW; Simple variants use plain words, checked by `i18nPlainWords.test.ts` once F2 adds the two families)

Components (CW):
- **`BusyChoice`**
  - Props: `{busy: BusyResponse, simple, onChoose(choice), onCancel}`.
  - Each visible holder is a card. With a pack active, the card is the holder's character still (`castArt`, idle), not agent/model text. Without a pack, it is a plain initials card with no logos. A waiting holder is shown dimmed with `busy.waiting`.
  - Hidden holders appear as one muted card, "someone else".
  - Buttons appear in server `choices` order. `continue` shows the agent and model it will use (from `busy.continue`), as a card in pack mode.
  - With `maxConcurrent` 1, the both-choice text adds the "one after another" note.
  - It is a radio group with keyboard support, not `confirm()`.
- **`OverlapNotice`**
  - Props: `{overlaps, run, simple, onTell?}`.
  - A one-line summary that expands to the paths (own pairs) or stays one line (hidden).
  - "Tell it" appears only for global runs the viewer may type into, or may follow up.
- **`ContestForm`**: 2-3 agent/model card pairs, reusing W2 `AgentCards` / `ModelCards` once they exist. Until then CW uses its own minimal radio list behind the same props, and W3 swaps it out. It also has an optional reviewer card and a budget note.
- **`ContestView` / `ContestCompare`**
  - Side by side at ≥ 900 px, stacked tabs at phone width.
  - Each column: a label (A/B/C) with the character still; status; a short summary; files with +/-; a "show changes" link to the existing `DiffViewer` for that run; a score chip (`contest.score`).
  - Buttons: Use this one, then Apply to my folder / Open a pull request / Keep for now. Below them, the "Throw away the others" checkbox, ticked by default.
  - An inline confirm step lists what will be discarded.

| key | en | zh-TW |
|---|---|---|
| busy.title | Someone is already on this | 已經有人在做這件事 |
| busy.holder | {agent} ({model}) since {time}, started by {who} | {agent}（{model}）從 {time} 開始，由 {who} 啟動 |
| busy.holderRepo | {title} in {repo} | {repo} 裡的「{title}」 |
| busy.waiting | Waiting its turn | 排隊中 |
| busy.others | {count} more by someone else | 另有 {count} 個別人的工作 |
| busy.queue | Start mine after it ends | 等它做完再開始我的 |
| busy.continue | Add this to its work instead (uses {agent}, {model}) | 改成接著它的成果做（用 {agent}、{model}） |
| busy.takeOver | Stop it and start mine | 停下它，改做我的 |
| busy.takeOverKeeps | Its work so far is kept | 它目前的成果會保留 |
| busy.takeOverKeepsNoPR | Its work so far is kept on its branch; no pull request is opened for it | 它目前的成果會留在它的分支上，不會為它開 pull request |
| busy.takeOverLoses | Its unfinished changes are not kept | 它還沒完成的修改不會保留 |
| busy.both | Run both | 兩個都做 |
| busy.bothSerial | This computer does one at a time, so mine starts after it | 這台電腦一次只做一件，所以會排在它後面 |
| busy.outdated | Update nodevas on this computer to run both or wait in line | 請更新這台電腦上的 nodevas，才能兩個都做或排隊 |
| busy.askOwner | Only its owner or an admin can stop it | 只有它的擁有者或管理員能停下它 |
| busy.skip | Skip the busy folders | 略過忙碌中的資料夾 |
| busy.changed | Something changed; check again | 狀況有變，請再看一次 |
| busy.waitingBehind | Waiting for an earlier run to end | 等前一個工作結束 |
| busy.simple.title | Your helper is still working in this folder ({minutes} min) | 你的助手還在這個資料夾工作（{minutes} 分鐘） |
| busy.simple.queue | Wait, then do mine | 等它做完再做我的 |
| busy.simple.takeOver | Finish it (its work is kept), then do mine | 讓它收尾（成果會保留），再做我的 |
| busy.simple.both | Do both | 兩個都做 |
| busy.simple.continue | Add this to what it is doing | 加到它正在做的事裡 |
| overlap.title | Also being changed by another run | 另一個工作也在改這些檔案 |
| overlap.mine | {title} also changes {count} of these files | 「{title}」也在改其中 {count} 個檔案 |
| overlap.hidden | Another person's work also changes some of the same files | 另一位成員的工作也在改一些相同的檔案 |
| overlap.kept | A result you have not applied yet also changes {count} of these files | 一個你還沒套用的成果也改了其中 {count} 個檔案 |
| overlap.addOnly | Both only add to these files | 兩邊都只是在這些檔案裡新增內容 |
| overlap.paths | {first} and {count} more | {first} 等 {count} 個檔案 |
| overlap.tell | Tell it | 告訴它 |
| overlap.tellText | Another task is also changing {files}. Keep changes to them small. | 另一個工作也在改 {files}，請盡量少改這些檔案。 |
| overlap.badge | {count} overlap | {count} 處重疊 |
| overlap.simple.title | Two jobs are changing the same file | 兩個工作在改同一個檔案 |
| overlap.simple.hint | You may need to pick one when they finish | 做完時可能要挑一個 |
| contest.toggle | Compare agents (比稿) | 比稿 |
| contest.title | {count} agents on the same task | {count} 位 agent 做同一件事 |
| contest.entry | Entry {label} | 第 {label} 份 |
| contest.addEntry / removeEntry | Add another / Remove | 再加一位 / 移除 |
| contest.reviewer | Let a reviewer score them | 請審查者評分 |
| contest.reviewerNone | No reviewer: I will look myself | 不用審查者，我自己看 |
| contest.budget | Uses {count} runs of today's limit | 會用掉今天上限的 {count} 次 |
| contest.state.running / judging / ready / decided / cancelled | Working / Being scored / Ready to compare / Decided / Stopped | 進行中 / 評分中 / 可以比較了 / 已決定 / 已停止 |
| contest.score | Score {score}/10 | {score}/10 分 |
| contest.why | Why | 理由 |
| contest.noScores | The reviewer gave no scores | 審查者沒有給分 |
| contest.advice | Scores are advice; you decide | 分數僅供參考，由你決定 |
| contest.pick | Use this one | 用這份 |
| contest.apply.local / pr / keep | Apply to my folder / Open a pull request / Keep for now | 套用到我的資料夾 / 開 pull request / 先保留 |
| contest.discardOthers | Throw away the others | 丟掉其他份 |
| contest.discardList | These will be thrown away: {labels} | 這些會被丟掉：{labels} |
| contest.discardRefused | {label} was kept: {reason} | 第 {label} 份保留下來：{reason} |
| contest.noChange | Changed nothing | 沒有任何修改 |
| contest.err.shape | Compare works on plain tasks only (no stages or pipelines) | 比稿只用於一般任務（不含階段或管線） |
| contest.err.outdated | Update nodevas on the runner to compare agents | 請更新 runner 的 nodevas 才能比稿 |

Simple mode shows the contest only as a result: no toggle and no start. Only the contest keys that view shows (`contest.title`, `entry`, `state.*`, `score`, `why`, `noScores`, `advice`, `pick`, `apply.local`, `apply.keep`, `discardOthers`, `discardList`, `discardRefused`, `noChange`) avoid "branch / PR / runner / worktree", and none names the actor (the single-user actor's name is `local`, `identity.go:88`). The rest (`contest.toggle`, `addEntry`, `removeEntry`, `reviewer*`, `budget`, `apply.pr`, `err.*`) may use those words and are never rendered in Simple; `contest.*` is not a plain-word family, so `ContestCompare.test.tsx` checks the Simple render shows none of them.

---

### C.9 Tests to write first (each package red first)

**F1:**
- `TestRepoKeyNormalisesRemotes`: ssh, https, userinfo stripped, `.git`, case; no remote gives the runner/repo form; output is 32 hex.
- `TestCoordinationWireShapes`: `onBusy` values (including `continue`, and `skip` for groups only), `holderIds` ≤ 8, contest entries 2..3, `Touched` caps, End `NoPR`.
- `TestMigrationsAddCoordination`: the columns; `runs_global_shape` admits only `compare` with a contest; `runs_contest_shape` (no `retry_of`; a real headless entry row with `mode = ''` and a reviewer row of kind `compare` insert, an interactive entry is refused); `open_pr` in the action check.
- `TestImportTableListCoversPGSchema` stays green with the new exclusions.
- `TestStoreContestNoPROnlyInContest`, `TestStoreCompareOnlyAsReviewer`, `TestStoreContestRefusesFallbacks`.
- `TestCreateWithReadsBackInsideTx` (the row is returned before commit, on one writer connection).
- `TestPumpAsksWaitsBehind`, using a stub-injected `waitsBehind`.
- `TestEndedHookRunsAfterSuccessorsAndForGlobal` (deferred: a fallback successor exists when `ended` runs).
- `TestNoUsageLimitWaitOrFallbackForContestEntry`.
- `TestRunRetriesCallsReleaseWaiting`.

**C1:**
- `TestStartAsksWhenNodeBusy`: 409 BUSY; visible holder fields; `others` counts the hidden one.
- `TestBusyHidesOtherMembersRun`: no id, title, agent or since leaks; nothing beyond `CountOthersActive` and Station's `Sessions`.
- `TestBusyAdminSeesAndMayStop`.
- `TestBusyIgnoresOwnLineage`: pipeline, contest, `work_root`, group.
- `TestBusyCountsWaitingRunsAsHolders`.
- `TestBusyReadOnlyKindsNeverHold`.
- `TestTwoAutopilotTasksInOneRepoRunInParallel`; `TestTwoNodeDispatchesInOneRepoNeverBusy`.
- `TestNodeStartHeldByOwnGlobalRepoRunOnly`; `TestGlobalStartHeldByOwnNodeRunInRepo`.
- `TestBusyRefusalLeavesNoPipelineRow` (a staged start refused BUSY leaves no `run_pipelines` row).
- `TestBusyTxNeverDeadlocksOnOneWriter` (pipeline start, take-over and continue each finish within a short deadline).
- `TestQueueGoesToTheTail`; `TestTwoQueuedBehindOneHolderGoInOrder`; `TestThreeStartsOnOneNodeRunOneAfterAnother` (three people, in turn).
- `TestQueuedWaitsForEveryHolder` (a node holder and an own global repo holder).
- `TestQueuedWaitsForHolderLineage` (fallback, usage-limit successor, next pipeline step).
- `TestReadOnlyPipelineStepNeverHeld`.
- `TestWaitsBehindHasNoCycle` (property test over random queues).
- `TestQueueOnOwnGlobalIsAFreshRun`; `TestContinueOnlyForSameRunnerAndRepoGlobal`; `TestContinueBecomesFollowUp` (calls G1 `FollowUp` with `QueuedBehind`, outside any tx).
- `TestTakeOverRefusesHiddenHolder` (403), `TestTakeOverRefusesChangedHolderSet` (409), `TestTakeOverEndsInteractiveAndCancelsHeadless`, `TestNodeTakeOverEndsWithoutPR`, `TestTakeOverCancelsPipeline`.
- `TestTakeOverReleasesHoldersClaim`; `TestQueuedRunOnDoneNodeIsCoordinated`; `TestBothRunIsCoordinated`; `TestOutdatedRunnerOffersOnlyTakeOver`.
- `TestReleaseRunClaimOnlyItsOwnSession` (agents).
- `TestTwoConcurrentStartsOneSeesBusy` (two goroutines on real Postgres).
- `TestAutopilotQueuesWithoutAsking`.
- `TestPipelineStepWaitsWhileOtherLineageRuns`.
- `TestAfterRunReleasedAcrossRunners`, `TestAfterRunReleasedAfterRestart`.
- `TestAfterSectionOwnVsHidden` (no leak of another member's summary).
- `TestBusyAuditHasNoTaskText`.
- copilot: `TestStartBreakdownBusyCreatesNoGoalNode`, `TestStartBreakdownBusyAnswerListsHolders`, `TestReviseGoalBusyIsAnAnswer`, `TestCopilotToolsNeverSendOnBusy`.
- server: `TestBusyRoutesPersonOnly`, `TestGlobalBusyOwnRunnerOnly`, `TestStartGlobalAsksWhenRepoBusy`, `TestSimpleStartQueueIsFreshRun` (against G2's merged `StartGlobal`), `TestGroupBusyOffersOnlyBothAndSkip`.

**C3h:**
- `TestContestStartsNEntriesInOneTx`, `TestContestBudgetWhole`, `TestTwoConcurrentContestsCannotOvershootBudget`, `TestContestRefusesStagedPipelineInteractive`, `TestContestOwnRunnersOnly`, `TestContestEntriesForcedNoPRAndCoordinated`.
- `TestContestReviewerStartsWhenTwoSucceeded`, `TestContestSkipsReviewerWhenFewerThanTwo`.
- `TestComparePromptAnonymisesAndRotates`, `TestComparePromptFitsMaxPrompt`, `TestComparePromptRedactsPatch`, `TestComparePromptDropsSecretFiles`, `TestParseScoresLastBlockOnly`, `TestParseScoresRejectsUnknownLabel`, `TestParseScoresRedactsWhy`.
- `TestPickAppliesWinnerDiscardsOthers`, `TestPickReportsRefusedDiscard`, `TestPickCreatorOnlyNotAdmin`, `TestCancelByCreatorOrAdmin`.
- `TestGlobalContestReviewerIsGlobalCompareRow`.

**C2:**
- runner:
  - `TestTouchedFromGitStatus` (new, modified, deleted, renamed, untracked);
  - `TestTouchedFromEditToolsRepoRelative`; `TestTouchedDropsAbsoluteAndOutsidePaths`;
  - `TestTouchedSkipsSecretPaths`; `TestTouchedIgnoresLockfilesAndGenerated`;
  - `TestTouchedMarksAddOnlyAndResendsOnChange`;
  - `TestTouchedSendsEachPathOnce`;
  - `TestTouchedBacksOffWhenSlow`;
  - `TestTouchedRunsForInteractiveByPollOnly`;
  - `TestCapabilitiesAdvertiseTouched`.
- hub:
  - `TestTouchedOnlyOwnRunningRun`; `TestTouchedDropsBadPathKeepsRest`;
  - `TestOverlapPairOnceAndCounts`;
  - `TestOverlapNotifiesOnFirstStrongPath`; `TestOverlapAddOnlyIsWeak`;
  - `TestOverlapScopeProjectOrOwn` (two members' global runs are never paired);
  - `TestOverlapCrossMemberHasNoPathsOrCount` (also for an admin);
  - `TestOverlapCrossMemberNeverMails`;
  - `TestOverlapIncludesKeptUnappliedResult`; `TestOverlapSkipsKeptResultWithPR`;
  - `TestOverlapGroupSiblingsSharingRemotePair`;
  - `TestOverlapMailsNodeRunsOncePerPair`;
  - `TestFollowUpPromptCarriesOverlapNote`;
  - `TestChangesAddsCommittedFiles`;
  - `TestTouchedPrunedAfter7DaysEvenWithRetentionOff`.
- agents: `TestCoordinationNoticeNotHubNotice`, `TestCoordinationNoticeCapPerRun`, `TestCoordinationNoticeEscapesTitles`, `TestCoordinationNoticeNeverReachesOtherMembersSession`, `TestCoordinationNoticeWaitsForSession`, `TestCoordinationMailRetriedAfterRateLimit`, `TestCoordinationSenderPerRecipient`.

**C3r:**
- `TestOpenPRActionOwnerOnlyOwnBranch`, `TestOpenPRRefusesExistingPR`, `TestOpenPRNoForcePush`.
- `TestCompareJobIsReadOnly`, `TestCheckGlobalAllowsContestAndCompare`.
- `TestCoordinatedToolsetDropsClaimAndStatus`; `TestCoordinatedPromptHasNoClaimStep` (headless, interactive and role prompts).
- `TestContestPromptSection`.
- `TestEndNoPRKeepsBranchOpensNoPR`.
- `TestCapabilitiesAdvertiseCoord`.

**CW:**
- `BusyChoice.test.tsx`: choices follow the server's; the hidden card shows no name; a waiting holder is marked; the take-over button is absent without `takeOver`; `continue` shows its agent and model; the pack shows a still with no text; no pack shows initials and no `img`; arrow keys; Simple wording with no `{who}`.
- `OverlapNotice.test.tsx`: hidden (no paths, no count) vs mine; weak paths are not counted; "Tell it" calls `prefill` with `overlap.tellText`; the background case opens the follow-up composer.
- `ContestForm.test.tsx`: 2..3 entries; duplicate agent+model refused; reviewer optional; budget note.
- `ContestCompare.test.tsx`: side by side vs phone tabs; labels and not agent names before scores; the score chip; pick → apply → discard confirm lists the labels; a refused discard is shown; the Simple render shows none of `contest.apply.pr`, `contest.err.*`, `contest.toggle`, `contest.reviewer*` or `contest.budget`.
- `contests.test.ts` (api paths and bodies), and `runs.test.ts` (`BusyError` parsing).
- i18n parity, and the plain-words check on `busy.simple.*`, `overlap.simple.*` (F2 adds the families).

**W3 / W4 / W5 additions:**
- the busy flow in DispatchDialog (node preflight), GlobalAgentForm and SimpleAgentBox (global preflight): note, 409 → choice → re-send with `holderIds`;
- the contest row in GlobalAgentList; the overlap badge;
- `ResultActions` locked on an undecided contest entry;
- the overlap notice in StoryTalk;
- e2e: `runs.spec.ts` busy case.

---

### C.10 Security guards (in addition to §14)

- **Stricter than Station, on purpose.**
  - Station lists every online session with agent, model, node, run id and `Doing` text (`station/assemble.go:186-196`), and counts other members' runs (`OthersActive`). Busy and overlap answers add nothing to either: a run the viewer cannot `canSee` is only a count, or `{hidden, since}`. They never add a name, task text, node title or file path, even where Station shows that run's session.
  - Mail to an agent is only about the recipient's own pairs. No agent is ever mailed about another member's run.
  - Realtime events carry ids only, as today.
- **Runner-reported data is untrusted when it concerns other members.** Any member may run their own runner and report any `touched` paths for their own runs. So a cross-member overlap reveals at most one bit per pair ("some file in common"), with no paths or count, never mails, and never moves after it is first announced. Paths and counts are shown only for pairs of the viewer's own runs, where the paths are the viewer's own runners' reports.
- **Stopping.**
  - Take over stops only holders that are both `canSee` and listed in the request's `holderIds`.
  - End keeps G1's `mayType` set; Cancel keeps today's rights.
  - A hidden holder can never be stopped from the busy flow.
  - Contest cancel is limited to the creator or an admin. Pick (which runs workspace actions) is the creator only, and the runner checks its owner again.
- **Claims.** The hub releases a claim only when its owner is the stopped or ended run's own session.
- **No new path into a running agent.** Global agents are told only by text that a person sends through the existing composer or follow-up. Node agents are told through the existing mail, from a hub sender that carries no person's authority (no `HubNotice` stamp).
- **Models never choose a busy answer.** The copilot's tools never send `onBusy` or `holderIds`; a model that reads board text cannot take over anyone's run.
- **The runner decides on disk.**
  - `touched` paths are dropped on the runner unless repo-relative, and checked again with `validPath`; secret paths, lockfiles and generated files are never sent.
  - `open_pr` pushes only the run's own branch (ownBranch / registered worktree), never with force; it refuses when a PR already exists.
  - Compare is read-only and strips repo config like review runs.
- **Prompts.**
  - Stored patches are raw `git diff` output. Contest compare prompts drop secret files' hunks and run `redact.Output` on every patch in the hub before the prompt is built.
  - Reviewer `why` text is redacted again.
  - The person's task is never audited.
  - `afterSection` carries a summary only from the actor's own prior.
- **Budget.** A contest's entries and reviewer, like a group's members, are checked as one block inside the start's transaction under the `budget:<owner>` lock. Each "both" or queued run is one ordinary run, checked as today.
- **Locks and transactions.** The busy check, pipeline insert and run insert run in one short transaction on the single writer, with the advisory lock. No other write, no runner round trip and no follow-up delegation happens inside it.

---

### C.11 Risks

- **R1. `maxConcurrent` 1 (the default) turns "both" and contests into serial runs.** Nothing breaks, but "compare two agents" takes twice as long. The UI says so (`busy.bothSerial`). It is not fixed in v1 (answer 8: no per-runner lane).
- **R2. Interactive is now the default (§8), so holders stay "running" until End or the 20-minute quiet timeout.** Many starts will meet a busy node or folder held by an idle interactive session. The hub cannot see idleness (the PTY sends no events, and the quiet timer is on the runner). The mitigation is wording: `since` is shown, and take-over of an interactive holder is End, which keeps its work (Simple says "Finish it (its work is kept), then do mine"). The quiet timeout bounds the wait.
- **R3. Overlap noise.**
  - Lockfiles and generated files are dropped on the runner, and add-only overlaps are weak (no notice, no mail). Notices and mail fire on the first *strong* shared path.
  - Formatter runs that rewrite whole files still overlap. A per-repo ignore list is v2.
  - Kept results with a PR are left out; kept results without one overlap for 24 h.
- **R4. `409 BUSY` changes the default for every `POST /api/runs` caller.**
  - The copilot's server tools now preflight and answer BUSY as plain text for the person, without creating a goal node. They never choose; the person starts it from the board (C1 owns `internal/copilot/{servertools.go, revise.go}`).
  - Any script that dispatched twice on purpose now gets a question; e2e specs that dispatch twice on one node must send `onBusy: "both"`.
- **R5. Agents rarely read mail without a nudge.** Delivery depends on #16 (merged: `RunTools` has the network tools) and on the unread hints. A headless Codex or Gemini run may never call `check_inbox`. People are still told; agent notice is best-effort.
- **R6. `git status` on huge monorepos.** It runs with a 4 s timeout, `GIT_OPTIONAL_LOCKS=0` and backoff. Windows file locking during `git status` is low risk, because it is read-only with optional locks off. Interactive runs learn of overlaps only from the poll (up to 30 s, longer after backoff).
- **R7. Coordinated runs and contest entries do not move the node's status.** They cannot claim the node, so the board may show it "not started" or "done" while they work. The run row in the story room and Station must make the work visible (W4/W5), and the person sets the status.
- **R8. `open_pr` widens §17**, which keeps a general "open PR later" action out of scope. The owner approved it for contest winners only (round 2).
- **R9. Reviewer bias.** It is reduced by anonymous labels and rotation, but a model may still prefer its own family's style. The scores are labelled advice, and the person decides.
- **R10. The transactional store helpers are shared by C1, G2 and C3h.** F1 must ship `createWith(q)`, `createPipelineWith(q)` and `lockBusy`, or the packages invent three versions.
- **R11. Queue behind another runner's run.** Release depends on the `ended` hook pumping the waiting run's runner. If the hub restarts between the end and the pump, `releaseWaiting` on the `RunRetries` tick closes the gap (`TestAfterRunReleasedAfterRestart`).
- **R12. A team-hub overlap still reveals one bit.** Telling A "another person's work also changes some of the same files" says that B's active node run in the same project touches something A's run touches. No name, node, path or count is shown, and a member probing with a runner of their own learns at most one bit per run they pay for. The owner kept this (round 2); turning it off would be a one-line scope change.
- **R13. Strict queue order can hold a queued run behind a long pipeline.** That is the intent ("after its work ends"). The waiting line shows what it waits for, and Withdraw is the ordinary cancel.

### Owner answers to the §C questions (2026-11-18)

1. **`open_pr`:** yes. Contests get `open_pr`, so the winner can become a pull request (C.4.4). §17 still keeps a general "open PR later" action out of scope.
2. **Cross-member overlap:** keep it as designed. The notice says only "some of the same files", with no path or count, and no agent is mailed (C.3.2).
3. **Queue vs continue:** as designed. Queue is always a fresh run; "continue its work" is a separate choice, only for your own global run in the same folder (C.0.6, C.0.7). Continue inherits the PR-update rules of §6.5.

---

### C.12 Review record

#### Revision after review

What changed, by critique (numbers follow the review list):
1. **Single writer (blocker).** C.0.3, C.2.3 and C.10 now spell out the transaction: checks first with no writes; one short transaction for lock, holders, `createPipelineWith` and `createWith` (read back through the tx); everything else after commit. A BUSY refusal leaves no pipeline row. `continue` runs before any transaction.
2, 16. **Queue order (blockers).** Waiting runs are holders. A new queued run goes to the tail. Pump's `waitsBehind` waits for every holder by a strict lineage-age order, so runs go one at a time and first come, first served holds after queueing. Tests added.
3. **Claims (blocker).** New C.2.5: the hub releases a stopped or waited-on holder's claim (only its own session's), and busy and contest node runs that would meet a held or done node get `Job.Coordinated` (no claim or status tools). `CapContest` became `CapCoord`; old runners get only take-over.
4. **Lineage.** `ended` is deferred to the end of `afterEnd`, and `waitsBehind` counts the holder's successors and running pipeline. Read-only steps are never held.
5, 19. **Follow-up.** Queue is always a fresh run. `continue` is a separate choice, only for a global start behind your own global run on the same runner and repo, and BUSY shows its agent, model and openPR. Simple wording was rewritten without `{who}`. SimpleAgentBox is a global start.
6. **Repo rule.** It is stated once (C.0.2, C.2.1). A node start sees node holders plus own global repo holders. A global start sees own global and node repo holders. There is a parallel-autopilot test.
7. **Contest shape.** No usage-limit wait, no fallback and no fallbacks for contest entries. `runs_contest_shape` also forbids `retry_of`. Adoption of a holder into a contest was dropped from v1.
8. **Compare redaction.** `compare.go` drops secret files and runs `redact.Output` on patches. C.10 is corrected. F1 exports `SecretPath`.
9, 21, 22, 24. **Ownership.** `ended` is F1's fixed list (`busyEnded`, `contestEnded`), and G2's hold stays in `Finish`. `applyBusy`/`afterBusy`/`continueBusy` live in busy.go, and G2 only calls them. C1 moved to Wave 1b, after G1. The capabilities go into `interactive.go`'s `capabilities()` (C2, then C3r). The `Changes` hook, `RunRetries` tick, `offer` hook and `overlapCount` subquery went to F1. `i18nPlainWords.test.ts` went to F2.
10. `legacysqlite_test.go` exclusion added to F1.
11. **Names.** `set_node_status`; `update_node_body` removed; kinds are `('', 'test', 'revise', 'plan')`; the realtime event stays `runs-changed`; SimpleAgentBox is global.
12. **Retention.** A separate fixed 7-day step before the `days <= 0` return.
13. **Ownership.** Copilot files went to C1 (with the overlapCount and plain-words items under 9, 24).
14. **Group siblings.** Group lineage is no longer an overlap exclusion.
15, 18. **Mail recipient.** The claim-owner fallback was removed. Mail goes only to the run's own session, otherwise it is retried on the next `touched`. A test proves it never reaches another member.
17. **Cross-member overlap.** No paths, no count, no mail, announced once. Runner data is documented as untrusted.
20. **Noise.** A built-in lockfile and linguist-generated ignore set on the runner; add-only paths are weak; notices fire on the first strong path; kept results with a PR are left out.
23. **Rate limit.** One sender session per recipient run, and the mail is retried after `ErrRateLimited`.
25. **Interactive news.** The poll is the only signal for interactive runs, and non-relative paths are dropped on the runner.
27. **Station baseline.** Corrected. Busy and overlap are stricter than Station on purpose, and the tests compare against both.
28. **Node take-over.** End carries `noPR`, so the holder keeps its branch and opens no PR.

#### Rejected critiques

- **17, the part "only start overlap from paths the hub can check: RunChanges at Finish".** `RunChanges` is sent by the same runner as `touched` (`Dispatcher.Changes`, dispatcher.go:518), so the hub cannot check it either. The design instead treats every runner report about another member as untrusted, and limits the cross-member answer to one bit with no mail.
- **1, the alternative "call EndPipeline on BUSY".** Rejected in favour of inserting the pipeline inside the busy transaction (`createPipelineWith`). A rollback then leaves nothing, and no compensating write is needed.
- **3, the alternative "do not offer queue when the holder left the node done".** A person may want their own attempt after someone else's finished one. The coordinated job handles a done node without a claim, so queue stays.
- **9, the alternative "F1 stubs Dispatcher.FollowUp and End".** A stub would let C1 compile but not pass its follow-up and take-over tests, and every package must end green. C1 is scheduled after G1 instead (Wave 1b), at the cost of a short delay.
- **9 and 21, the suggestion of a `d.groupEnded` hook for G2.** Plan §16 already puts G2's usage-limit hold in `Finish` (dispatcher.go, which G2 owns), so G2 needs no hook in `ended`.
- **19, the suggestion "offer End and start without asking when the holder is idle".** The hub cannot see idleness: the PTY sends no events, and the quiet timer is on the runner (plan §7). The design uses the wording alternative from the same critique: take-over of an interactive holder says its work is kept.
- **20, the suggestion "files only appended to are ignored".** They are kept as *weak* overlaps (shown, no notice, no mail), not ignored. Two appends to one place still conflict in git, so the person should still see them.
- **27, the suggestion "show a hidden holder's agent picture when its session is on Station".** Rejected in favour of being stricter than Station on purpose. Pairing a busy card with a Station session would link a hidden run to a node and a person's work in a new way.
- **28, the alternative "let busy.takeOverKeeps say its PR will open separately".** Two competing PRs for one node is the problem itself, so End carries `noPR` instead.
