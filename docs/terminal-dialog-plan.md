# Themed terminal dialog, interactive by default, picture-card pickers, multi-repo global agents

Merged design, 2026-11-17. Base: the **reuse-first** design. Grafted from **user-first**: IME guard, the `user` event at seq 0, queued replies while a background run is still running, a one-follow-up-per-run unique index, the resume fast-fail retry, the global prompt that invites a closing question, the subscribe-in-view rule for the story list, the larger terminal box with "open large", the polite screen-reader announcer, and the text-to-picture table.

Worktree checked: `H:\visual-editor\.claude\worktrees\easy-start` (feat/terminal-dialog, 21169dd6).

Work order for building on another machine (handed off 2026-11-17; nothing here is built yet). Delete this file before the PR is merged (shipped plans are not kept in the repo). Overall handoff: `docs/handoff-2026-11-17.md`.

## Owner's words (the requirements, verbatim)

- Story room dialogue box: 「我希望對話框是一個 terminal 的 dialog 只是經過外觀美化」; typing into it: 「能打字（像真的 terminal）」; content: 「agent 說的話＋工具一行摘要」.
- 「應該要是預設就是互動式的才對」, 「選取 agent 模型時應該要有圖卡才對」.
- 「這個主題包的目標就是把圖片代替繁雜文本」: with a theme pack active, art replaces text clutter (§11).
- Global agents: 「全域 agent 的目的就是處理統一的問題 比如微服務版本升級 cicd 調整 漏洞修復等等」 (§10).
- Coordination: 「三層處理沒問題」, approved but NOT designed yet: see §C below. Design it first (append to this file, fold its files into F1/F2 and the waves) before building.

## C. Coordination: who does it (approved, to design)

Today nothing decides up front: hub dispatch has no node or repo busy check (`internal/runs/start.go`, `dispatcher.go`, web `DispatchDialog.tsx`); only MCP `claim_task` is exclusive (`ALREADY_CLAIMED`, `internal/httpapi/node/claims.go:21`); the second local merge aborts on conflict and changes nothing (`internal/runner/workspace_actions.go:31`). Three layers, the person always decides:
1. **First come, first served by default.** Starting a run on a node or repo that an unfinished run already works on shows who and for how long, with: queue behind it (start after it ends, continuing from its result via §6), stop it and take over, or run both on purpose (layer 3). Automatic starts (autopilot, follow-ups) queue by default.
2. **Same files caught early.** The hub already gets the files each run writes (`internal/agenthook/changes.go`, run events). When two active runs in one repo write the same file, tell both people in the UI and both agents by agent mail. Global agents have NoBoard and get no mail today: decide how they get told.
3. **Deliberate competition (比稿).** Same task to two agents (e.g. Claude and GPT), separate worktrees, side by side when both finish, optionally scored by a reviewer role; the person picks one to apply, the other is discarded.
Team hub: another member's run shows as "someone is on it"; only its owner or an admin can stop it; never show more of their task than Station already does.

## M. Agent mail finding (verified 2026-11-17 with two DeepSeek runs)

Agent-to-agent mail between two dispatched runs works (4 messages delivered, attachment stored on the hub, `node:<id>` addressing works) and the loop guard trips after 13 messages in one agent thread (first + 12 replies, `maxConsecutiveAgentTurn=12`), released by a person's reply. **Gap:** runs get `nodevas mcp --toolset run` (`internal/runner/jobs.go` mcpArgs), and `RunTools` (`internal/mcp/toolset.go:49-62`) leaves out `list_team` and `save_attachment`; no built-in role grants them (`internal/engine/roles.go`). A run cannot list the team or open an attachment, while `check_inbox` tells it to use `save_attachment` (the reviewer run lost 10 minutes on an unanswered `ask_user`). Fix: add both to `RunTools` and to the roles that have `check_inbox` (or inline small text attachments in `check_inbox`). Cross-account isolation was not testable on a standalone hub. Fold this into F1 or ship it on its own first.

---

## 0. Decisions

1. **Edition first.** Single-user (built-in runner, actor `local`) gets every feature. The team hub runs the same code with every existing gate unchanged (§1).
2. **One dialog component, `RunDialog`.** It is used in the story room's dialogue box, the Simple describe box and the Station global list.
   - Background run: a transcript of the run's stored events. The agent's text is shown in full. Each tool call and its result form one plain line (`› 執行 npm test`) that expands to the raw input and output.
   - Interactive run: the existing xterm `RunTerminal`, coloured by the theme, plus one input line and a key row.
3. **Interactive (PTY) is the default**, as the owner asked ("應該要是預設就是互動式的").
   - It is the only path today in which a person can answer a global agent's questions and permission prompts: global runs have no board, no `ask_user` and no relay, and their asks list is always empty (`httpapi/runs/global.go:219-226`).
   - Its known costs are handled, not avoided:
     - "完成，保留成果" (End) ends the session and publishes the work.
     - A quiet timeout ends an idle session the same way.
     - The view-only state now reaches the browser.
     - For Claude, the session's own transcript file is tailed into structured events (package R3), so Claude interactive runs also get step lines and history that lasts.
   - Background is used where interactive cannot work (§8).
4. **Interactive opens no window.** The agent's TUI runs in a hidden PTY on the runner (`internal/runner/interactive.go:119-262`, ConPTY on Windows), and its bytes are relayed to the in-app xterm. The i18n text `terminal.modeNoteLocal` / `terminal.modeNote` ("opens … on this computer") is wrong and is rewritten.
5. **Follow-ups continue the same work.** Typing after a run ends starts a new run, linked by `continue_of`:
   - same runner;
   - same worktree folder, reused in place (`<dataDir>/worktrees/<workRoot>`);
   - same branch;
   - the CLI's own resume where it exists (flags checked on the installed CLIs, §6.5); otherwise a carry-over prompt.
   - The hub only names run ids. The runner resolves folder, branch, HEAD and session from its own `work.json` and git.
6. **Queued reply (graft).** A line typed while a *background* run is still running becomes a queued follow-up. It starts when that run ends, and it can be withdrawn.
7. **Picture cards replace the agent, model, folder and mode dropdowns.**
   - Art comes from `figureFor(manifest, {agent, model}, "idle")`, frame 0 as a still.
   - With no pack, no match or a broken image, a plain card shows `agentName` plus initials on `station-avatar agent-<kind>`. No logos.
   - Radio-group semantics (or native checkboxes for folders); works at phone width.
8. **Multi-repo default = B, fan-out.**
   - One ordinary global run per repo, tied together by a `group_id`.
   - Groups run in the background. Talking to a member happens through follow-ups once it has ended.
   - Shape A (one session over N repos) is designed but deferred, because of its cost (§10.4).
9. **No theme-manifest change.** The terminal look comes only from the `--story-*` CSS variables, so the Go / TS / docs / `doc_test` chain is not touched.

---

## 1. Editions

| | Single-user (built-in runner) | Team hub |
|---|---|---|
| Who starts a global run or group | the local person | the caller, **on their own runner only** (`runs/global.go:96-99`); all repos on that one runner; never across runners |
| Offer acceptance | `askBuiltIn` treats `Origin=person` as a click (`builtin.go:95-121`) | unchanged: console y/N, auto-accept policy, or app ask. Each group member and each follow-up is a separate offer; there is no hub-side "accept all" |
| Typing into a PTY | always on (`jobs.go:632-634`) | hub `mayType` (dispatcher or runner owner; never an agent or an admin, `httpapi/runs/terminal.go:67-69`) **and** runner `remoteTyping` (`AllowRemoteTyping` or a console yes). New: the view-only state is shown in the browser; it is not widened |
| Permission prompts | `local` answers them all | the runner owner only (`MayAnswer`); runScope tool/command/once unchanged; truncated input means once |
| Follow-up, End, queued reply | the local person | global: `actor == prior.CreatedBy == runner.OwnerID`. Node follow-ups are not in v1 |
| Default mode | interactive, by the §8 rule | interactive only on the person's own runner when its policy allows; background on a teammate's runner |
| Simple describe box | shown | hidden, as today (`SimpleAgentBox.tsx:70-71`) |

---

## 2. User flow

1. **Station > 全域 agent.**
   - Runner card (only when there are several).
   - Agent cards (character still).
   - Model cards.
   - Folder cards, multi-select with All / None.
   - Mode cards: 互動 shows the character's `speak` still; 背景 shows its `write` still. Pre-picked by §8.
   - Task box, then Start.
2. **One folder, interactive.** The run's dialog opens under its list row and in the story room.
   - Header: character plate, model, one flavour line (typewriter on this line only).
   - Body: the themed xterm.
   - Footer: input line plus key row (Enter, Esc, 1/2/3, Up, Down, from `keySequence`), with "完成，保留成果" and "停止".
   - Permission prompts and questions appear on screen. The person answers them by typing or with the key row.
3. **It ends** (End, quiet timeout, the person quits the TUI, or the agent exits). The work is published as today: commit, keep the branch, open a PR if asked.
   - The screen stays for 10 minutes (`DefaultTermKeep`). After that the body shows the summary's last lines, or Claude's steps (R3).
   - Typing "按鈕再大一點" starts a follow-up in the same folder and branch.
   - The dialog shows each run as a "turn", headed `› 你：…` from the `user` event.
4. **Background run.** The body is the live transcript. The input line says "它做完就會看到". Sending creates a queued follow-up, shown as a pending "你：" line with "收回" (withdraw).
5. **Several folders.** A group row with one folder card per repo, each with that member's pose still and status. Opening one shows that repo's dialog. Group buttons: 全部停止, 套用全部已完成的, 告訴全部 / 告訴失敗的, 立即繼續 (after a usage-limit pause).
6. **Simple describe box.** The same dialog renders inline in place of `StartedLine`.

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
  - 16 px font on phones (prevents iOS zoom). Sticky at the bottom.
  - Its state comes from the §4 table.
- `web/src/runs/dialog/RunDialog.tsx`: `{runId, variant: "story"|"plain"}`.
  - Header slot (plate and flavour line, supplied by the caller), body (transcript or screen, with Screen/Steps tabs when both exist), composer, and End/Stop.
  - "放大" opens the same dialog in a `GameDialog`.
- `web/src/runs/dialog/useRunDialog.ts`:
  - Loads the chain (`GET …/thread`).
  - Calls `watchRun` for the turn in view (ref-counted).
  - Derives the composer state.
- `RunTerminal.tsx` gains:
  - a `palette` / container-theme option: `terminalTheme(el)` reads `--story-night`, `--story-ink`, `--story-soft` and `--story-accent` when it is inside `.px-story-talk`;
  - an `onConnection(conn)` prop, so the composer sends through the **same** socket.

### 3.2 Mount points
- **StoryTalk `TalkBox`** (`StoryTalk.tsx:194-244`): for an actor with a run row, `<RunDialog variant="story">` replaces `StoryLine` + `px-story-meta`.
  - Narrator, visitor and session actors keep today's box.
  - The plate stays. One flavour `StoryLine` goes into the header.
  - The facts become pictures (§11).
  - Choices (`ActorItems`/`RunItems`) stay unchanged.
- **SimpleAgentBox:** inline, in place of `StartedLine`.
- **GlobalAgentList:** inside the expanded row. Only one row is expanded at a time, so there is at most one xterm and one `watchRun`.

### 3.3 Subscriptions
- Story **list** layout: only boxes in view are subscribed (IntersectionObserver, at most 6). The rest show the flavour line, plus the last `text` event if it is already loaded.
- Story **stage** layout: only the focused member.
- GlobalAgentList: only the expanded row.

### 3.4 Sizing and motion
- `.px-story-talk.has-run .px-story-said` uses `max-height: min(52vh, 520px)` in place of the 166 px cap (`warroom.css`).
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
| `log` | hidden behind "顯示 runner 紀錄", except `hub:` notes and a non-zero "exited with code N" (dimmed) |
| `usage` | hidden (tokens appear as the energy bar, §11) |
| unknown | dimmed log line, as `RunTimeline` already does |

---

## 4. Typed input by run state

| Run state | Composer | Enter does | Guard |
|---|---|---|---|
| queued / offered | disabled: "等待 runner 開始" (team: "等待 {who} 在那台電腦上同意"); Stop shown | — | — |
| running, interactive, viewer may type | enabled; raw keys also go straight to xterm | `conn.sendInput(text + "\r")` on the same terminal socket | hub `mayType` + runner `remoteTyping`; socket audit, 64 KiB/s bucket, Origin/Sec-Fetch checks, `StillAuthorized`, 64 KiB cap |
| running, interactive, read-only (hub) or view-only (runner) | disabled, with the reason (`ph.notYours` / `ph.viewOnly`) | — | new `view_only` signal (§7.3) |
| running, background, node run, pending question | becomes the answer field of the inline `AskCard` | `answerAsk {decision:"answer", answer}` | `canAnswerAsk`, project scope, `POST /api/agents/asks/:id/answer` |
| running, background, pending permission / approval | `AskCard` buttons (number keys, runScope tool/command/once); composer disabled | — | `MayAnswer`: runner owner only; truncated input means once |
| running, background, global | enabled: "它做完就會看到" | `POST …/follow-up` creates a **queued** follow-up that Pump holds until the prior run is final; shown as a pending "你：" line with "收回" | §6.3 checks; "收回" cancels the queued run |
| ended (succeeded / failed / cancelled / usage limit), latest turn, worktree kept or never needed, no open PR | enabled: "還想改什麼？" | `POST …/follow-up` | §6.3 checks |
| ended, usage limit, before `ResetAt` | enabled: "{time} 後可再繼續" | queued follow-up with `RetryAt = ResetAt` | same |
| merged_local / discarded / PR merged | button "用這句開始新工作" | an ordinary new global run: same repo, agent and model, from base | normal start checks |
| PR open (v1) | disabled: "請在它的 pull request 上繼續" | — | gate `has_pr` |
| a later turn exists | read-only: "接續在下方" | the dialog scrolls to the latest turn | — |
| viewer is not the dispatcher / owner | read-only: "只有 {name} 能回覆" | — | — |
| node run, ended | disabled, with a link to "再派一次" (Dispatch). No node follow-up in v1 | — | — |

**Cancelled prior.** The worktree may hold uncommitted changes (`jobs.go:514-518`). The follow-up may start on that dirty folder only if the runner's own record says the prior run was cancelled or failed.

---

## 5. Plain one-line tool summary (`web/src/runs/toolLine.ts`)

**Normalise.** Lower-case the name. `mcp__<srv>__<tool>` gives a server and a tool. OpenCode's `nodevas_x` is already mapped to `mcp__nodevas__x`.

| Verb | Names (first match wins) | Key | Target |
|---|---|---|---|
| run | bash, shell, run_shell_command, run_command, command, terminal, exec_command, local_shell; any name containing `command` or `terminal` | `runDialog.tool.run` 執行 {target} | `command`, `cmd`, `CommandLine`, `commandLine`. An array `["bash","-lc",X]` gives X; any other array is joined with spaces. A codex `shell` input is a plain string |
| write | write, write_file, create_file | `.write` 寫入 {target} | file key |
| edit | edit, multiedit, replace, replace_file_content, str_replace*, patch, notebookedit | `.edit` 修改 {target} | file key |
| patch (codex) | apply_patch: input is lines of `<kind> <path>` | 1 path: `.edit`; several: `.files` "{first} 等 {count} 個檔案" | paths |
| read | read, read_file, view_file, view, notebookread, cat | `.read` 讀取 {target} | file key |
| search | grep, glob, search*, find, ls, list*, codebase_search, find_by_name | `.search` 搜尋 {target} | `pattern`, `query`, `Query`, `glob`, `regex`, `path` |
| web | webfetch, web_fetch, fetch, websearch, web_search, google_web_search | `.web` 查詢網路 {target} | `url`, `Url`, `query` |
| todo | todowrite, write_todos, update_plan, plan; any name containing `todo` | `.todo` 更新待辦清單 | — |
| agent | task, subagent, agent | `.agent` 派出助手：{target} | `description`, `prompt` (first line) |
| mcp | `mcp__s__t` | `.mcp` 使用 {server} 的 {tool}; `nodevas` → `.board` 看板; `claim_task` → 接下任務, `get_node` → 讀取任務, `set_node_status` → 回報狀態 | — |
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
- **Tests** take tool names from every `internal/runner/adapter/testdata/*_stream.jsonl` fixture.

---

## 6. Continue (follow-up) mechanism

### 6.1 The work folder stays fixed
- `workRoot` is the id of the first run in the chain. The folder is `<dataDir>/worktrees/<workRoot>`, and its branch is the root's `agent/<slug>-<root8>`.
- In-place reuse keeps the cwd the same. Claude and Gemini key their sessions by cwd, and codex filters by cwd unless `--all` is given.

### 6.2 Wire (`internal/runnerwire`)

**`runs.go`**
- `EventUser = "user"`.
- `Job` gains `ContinueOf`, `WorkRoot`, `ContinueTask` (the prior run's task) and `ContinueSummary` (the prior summary, already redacted, ≤ 4 KiB).
- `Run` gains `continueOf`, `workRoot`, `continuedBy`, `viewOnly` and `groupId`.
- `Accept` gains `ViewOnly bool`.

**`wire.go`**
- `Agent.Interactive bool` in the hello.
- `TypeEnd = "end"`: an envelope with `RunID`.

**`global.go`**
- `CapContinue = "continue"`: covers follow-up, End, view-only and the work registry.
- `FollowUpRequest {message, mode?}`.
- `MaxFollowUpBytes = MaxGlobalTaskBytes`.
- Group request and response types; `MaxGroupRepos = 30`.
- Realtime name `global-group-changed`.

**`workspace.go`**
- `WorkspaceGateContinued = "continued"`.
- `WorkspaceActionJob.WorkRoot`.

**`terminal.go`:** doc comment fix (no window is opened).

### 6.3 Hub: `Dispatcher.FollowUp` (`internal/runs/followup.go`, new)

Checks, in order. Each refusal is a `*StartError` with a code and a field.

1. The actor is a person (`personOnly`, not an agent) and can see the prior run (`canSee`).
2. The prior run is global (v1 scope).
3. `actor == prior.CreatedBy == runner.OwnerID`.
4. Prior state:
   - final: start normally;
   - queued / offered / running and background: allowed as a *queued reply*, held by Pump;
   - running and interactive: refused with `RUN_LIVE` (the person should type into the screen instead).
5. The prior is the latest turn: no non-withdrawn run has `continue_of = prior`. Otherwise 409 `CONTINUED {latestId}`.
6. No other unfinished run in the same `work_root`, apart from the prior when this is a queued reply.
7. The prior workspace is not `merged_local` or `discarded`; `PRState` is not merged; no PR is open (v1). Otherwise `WORK_GONE` or `HAS_PR`.
8. Message: trimmed, valid UTF-8, no NUL, ≤ `MaxFollowUpBytes`.
9. The runner has `CapContinue` (else `RUNNER_OUTDATED`). Interactive mode also needs `ModeSupported` and the §8 rule.
10. `BudgetStatus(owner)` has room.

**Create.** `New{Global, NodeTitle: title(message), BranchSlug, NoPR, RepoID, Base: prior's, Agent/Model: prior's, Mode: request or §8 default, ContinueOf: prior.ID, WorkRoot: prior.WorkRoot || prior.ID, GroupID: prior.GroupID, Origin: person}`. A prior that stopped at a usage limit with `ResetAt` in the future sets `RetryAt`.

**Then:**
- Write the message as event **seq 0, type `user`** (`Store.UserFirst`, the `NoteFirst` pattern). This is also done for every person-started global run and group member, so each turn opens with "你：".
- Audit `run.global.followup {prior, root, mode, messageBytes}`. The text itself is never audited.
- `Submit`.

**Store and gates:**
- `Store.Create` accepts `continue_of` and `work_root` only on global rows.
- `WorkspaceGate` returns `continued` for any run that has a non-withdrawn successor, so merge, discard and apply act on the latest turn only. This is derived, so the `runs.workspace` CHECK (`postgres.go:1125`) is not changed.
- `WorkspaceInUse` also matches `work_root`.
- **Pump** (`dispatcher.go:306-321`, one added skip, done in F1): skip a queued run whose `continue_of` prior is not final.
- **Withdraw** = the ordinary cancel route on the queued follow-up. A follow-up cancelled before it was accepted counts as withdrawn and is ignored by the latest-turn check and by the unique index.

### 6.4 Runner (`internal/runner/work.go`, new)

**Registry.**
- `<dataDir>/work.json`, mode 0600, the same pattern as `handoffs.json`.
- One entry per root: `{root, repoID, branch, head, base, agent, session, lastRunID, lastStatus, at}`.
- Written at every finish of a global run, including roots whose folder was dropped because nothing changed.
- Kept 30 days, at most 500 entries.

**Offer with `ContinueOf`.**
- `checkGlobal` allows `ContinueOf` and `WorkRoot` (both match the run-id regex). It still refuses `Branch` and `HandoffBranch`, so `global_test.go:51,225` keep their meaning.
- `prepareContinue(job)` runs before the accept ask, like `prepareHandoff`. Using only the runner's own record, it checks:
  - `entry.lastRunID == job.ContinueOf` and `entry.repoID == job.RepoID`;
  - the folder's git dir belongs to that root (the GitFile/GitDirs check) and its branch is `entry.branch` (`ownBranch`);
  - `HEAD == entry.head`;
  - the tree is clean, unless `entry.lastStatus` is cancelled or failed;
  - no active run on this runner uses that folder.
- If the folder was removed because the prior changed nothing (`publish.go:48-54`), it is re-created at the same path from base, with the root's branch name.
- `execute` uses that folder instead of `CreateWorktreeFrom` (`jobs.go:475`). `markNoBoard`, `guardGitDir` and `publish` run unchanged.

**Run-id lookups re-keyed by root:**
- `findRunWorktree` (`workspace.go:54`)
- kept.json (`RecordKept`, `PruneKept`)
- the `watched` PR map (`publish.go:82-88`)
- `sharedWith`
- preview grant and `browseRun`
- workspace actions (`doWorkspaceAction`): the hub passes `WorkRoot`, and the runner checks `work.json lastRunID == job.RunID`. Earlier turns get gate `continued`.

**Session ids stay on the runner.** The hub never supplies a session id, ref or path.

### 6.5 Session resume (`adapter.Resumer`, optional)

| Agent | Session id source | Resume (checked on this PC 2026-11-17) |
|---|---|---|
| claude 2.1.299 | headless: `system/init session_id` (`claude.go:395`). Interactive: the runner picks a UUID and passes `--session-id <uuid>` (flag exists) | headless `-p --resume <id>` (prompt on stdin); TUI `claude --resume <id> <prompt>` |
| codex 0.156.0 | `thread_id` (`codex.go:206`) | `codex exec resume <id> -` (prompt on stdin). Accepted after `resume`: `--json`, `-m`, `--skip-git-repo-check`, `--dangerously-bypass-approvals-and-sandbox`, `-c`. **`--sandbox` is not listed**, so pass the sandbox as `-c sandbox_mode=…`. Interactive: no id is known, so the fallback is used |
| opencode run 1.19.4 | `sessionID` (`opencode.go:327`) | `opencode run --session <id>` (`-s` exists); TUI `--session` |
| opencode ACP | `s.session` (`acp.go:309`) | `session/load` when initialize advertises `loadSession`; otherwise a new session plus the fallback |
| antigravity | `conversation_id` (`antigravity.go:401`) | `--conversation <id>` (in `agy --help`) |
| gemini | `session_id` (`gemini.go:281`) | `--resume <id>`: **unverified** (CLI not installed), so v1 uses the fallback only |

- **Capture.** Parsers implement `SessionReporter{Session() string}`. The id is validated with `^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$` and stored only in `work.json`. It is passed in argv only, never in a shell string (the `.cmd` shim path keeps using `wrapBatch`).
- **Resume rules.** Resume only when the agent kind matches the entry's. A different model is allowed for claude, codex and opencode.
- **Fast-fail retry (graft).** If a resume exits non-zero with no `text` event within 30 s, the runner retries once with a fresh session and the fallback prompt, and logs "resume failed; continuing without the agent's memory".
- **Fallback prompt.** `adapter.ContinueSection`, built like `handoffSection`:
  > You are continuing your earlier work in this folder on branch X. Earlier task: … You reported: … Inspect `git log <base>..HEAD` and `git status`. The person now says: …
- **Prompt on resume.** Only the person's message plus a one-line rules reminder (global: "same worktree, do not push").
- **Global background prompt wording (graft)** (`adapter/global.go:20`). "Nobody can answer questions during this run" becomes: "Nobody can answer during this run: decide sensibly. If you need a decision, ask it as the last line of your final message; the person's reply continues this work."

### 6.6 Effect on usage limits
A global run that stops at a usage limit commits on its branch (`jobs.go:526-535`). A follow-up after `ResetAt` continues it. This fills in the handoff that global runs are missing.

---

## 7. End, quiet timeout, view-only

1. **End ("完成，保留成果").**
   - Routes: `POST /api/global-runs/:id/end` and `/api/runs/:id/end`.
   - Allowed for the `mayType` set; refused for agents and admins.
   - The hub sends `TypeEnd`. The runner kills the PTY with `stopped="ended"`, giving Status **succeeded**, and publishes normally.
   - Cancel stays as it is (nothing is committed).
   - A runner without `CapContinue` gets 412.
2. **Quiet timeout.**
   - Runner setting `interactiveQuietMinutes`, default 20; 0 turns it off.
   - No PTY output and no input for that long ends the session like End, with a log line.
   - The 60-minute `runTimeoutMinutes` still applies (a failed timeout, as today).
3. **View-only.**
   - The runner sends `Accept.ViewOnly = !remoteTyping` on interactive runs.
   - The hub stores `runs.view_only` and sends `{"type":"readonly"}` to every viewer.
   - The composer shows `ph.viewOnly`.
   - This replaces today's silent drop (`interactive.go:279-281`).

---

## 8. Interactive by default

**Rule:** `defaultDispatchMode(ctx)` in `RunModePicker.tsx` is pure and is the single source of the default. It gives **interactive** when all of these hold:
- the runner has `CapInteractive` (`runnerSupportsInteractive`);
- the chosen agent's hello entry has `interactive: true` (new per-agent flag, set from `adapter.Interactive`; all 5 kinds today, gemini marked unverified);
- the runner is the viewer's own, or the edition is single-user;
- auto-accept is off, or its policy `modes` include interactive (otherwise every offer would wait at the console);
- for a node dispatch: not a staged Executor implement (`CodeStagedNeedsHeadless`, `start.go:274-280`), and no pipeline and no fallbacks (interactive skips handoff, so the fallbacks would never be used);
- one folder, not a group.

Otherwise it gives **background**, with a one-line reason under the mode cards (`picker.mode.fallback.*`).

**Always background:** autopilot, pipeline rounds, fallback and handoff runs, plan / judge / review / verify (hub rules unchanged), a teammate's runner, and group members.

**Where:**
- `GlobalAgentForm.tsx:175`: `useState("headless")` becomes an unset choice resolved by the rule. A card the person picks wins.
- `DispatchDialog.tsx:212`: same.
- `SimpleAgentBox.tsx:129-137`: send `mode` when the rule gives interactive; `startFailure(…, {interactive: mode === "interactive"})` (:145).
- Follow-ups: the same rule. A queued reply is always background.

**Preference.** `runMode: "auto" | "interactive" | "background"` (`preferences/schema.ts`), default `auto`. Picking a mode card stores it.

**i18n.** Rewrite `terminal.modeNoteLocal` / `terminal.modeNote` (§15) and `globalAgents.noAsk`.

---

## 9. Picture-card pickers

**`web/src/game/warroom/castArt.tsx` (new, foundation).**
- Moves `still()`, `Face` and `skinVars` out of `StoryRoom.tsx` (:102-137, :188).
- Adds `useCastCard(agent, model, scene = "idle")`, which wraps `useActiveThemeCast(true)` and `figureFor`. It skips `cast.broken`, works without the WarRoom provider, and always draws frame 0 as a CSS background (no canvases, no clips).

**`web/src/station/pickers/` (new):**
- `PictureCards.tsx`: generic.
  - `mode:"one"`: `role=radiogroup`, `role=radio` with `aria-checked`, roving tabindex and arrow keys (copied from `OfficeStylePicker.tsx:43-51`).
  - `mode:"many"`: `fieldset` / `legend` with native `input[type=checkbox]` inside label cards, All / None, and the count `已選 {n} 個`.
  - `aria-invalid` and `aria-describedby` sit on the group, keyed by `failure.field` (`agent`, `model`, `repoId`, `repoIds`).
  - Grid: `repeat(auto-fill, minmax(96px, 1fr))`. Rows scroll beyond 3; one column of wide cards at ≤ 400 px.
- `AgentCards.tsx`: one card per installed agent. Picture, `agentName(kind)`, version in small text.
- `ModelCards.tsx`:
  - Always shows a visible model name (`runnerModelLabel`); "CLI 預設" comes first and shows the agent's character.
  - Model ids are matched against the pack, so a deepseek model shows the DeepSeek card.
  - With more than 8 models: grouped by character, with a filter box and "還有 {n} 個模型" (OpenCode can list 64).
- `RepoCards.tsx`: plain folder card (inline neutral SVG, name, path tail); multi-select only in `GlobalAgentForm`.
- `ModeCards.tsx`: 互動 shows the `speak` still; 背景 shows the `write` still.

**Plain fallback.** Used when there is no pack, no match and no fallback character, or the image is broken. It shows `agentName` + `initials()` on `station-avatar agent-<kind>`. No logos (legal rule; `docs/theme-packs.md:325`). The unnamed `?` is not used in pickers.

**Keep the derived-on-every-render fallbacks** (`agents[0]`, `defaultModel`, `repos[0]`). In DispatchDialog, still set `touched.current.agent` / `.model` on pick (:689, :707).

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
- **Request.** `POST /api/global-runs/groups {runnerId, repoIds[2..30], agent, model, base?, task, slug?, openPR, simple}` returns `201 {group, runs[]}`. One folder keeps today's `POST /api/global-runs`.
- **Hub** (`internal/runs/groups.go`, new):
  - `StartGlobal` is split into `validateGlobal` (task, slug, base, runner ownership, agent/model, caps), run once, plus a per-repo `CheckAgainstHello`.
  - Mode must be background; interactive gives 400 `GROUP_NEEDS_BACKGROUND`.
  - Budget: refused whole when `Used.Runs + N > DailyRuns`. The message says how many fit, and the UI keeps the first that many folders picked.
  - One transaction: a `run_groups` row plus N runs with `group_id`, each with its seq-0 `user` event.
  - One audit entry `run.global.group {group, count}`.
  - One `Pump`: FIFO under `maxConcurrent`; the rest stay queued.
- **Pump** (`dispatcher.go`, G2):
  - skips members while `run_groups.held_until` is in the future (done in F1);
  - **re-checks cost and token budget** before offering a queued member, and cancels it with code `BUDGET` if over.
- **Usage limit.** A member that finishes with `FailureKind` usage_limit sets `held_until = ResetAt`, and Pump skips the queued siblings.
  - UI: "已暫停到 {time}：用量上限" and "立即繼續" (clears the hold).
  - The member that hit the limit continues through a follow-up.
- **Routes:**
  - `GET /api/global-runs/groups/:id`
  - `POST …/groups/:id/cancel`: `CancelAllGlobal` filtered by group
  - `POST …/groups/:id/resume`
  - `POST …/groups/:id/follow-up {message, only: "failed" | "all"}`: runs `FollowUp` for each eligible latest turn and returns `{started[], skipped[{runId, code}]}`
  - `Run.groupId` on every member
- **Results.**
  - Per member: Take a look / Apply to my folder / PR, via the existing `ResultActions`.
  - Group button "套用全部已完成的": sequential merge_local over finished members, stopping per repo on a conflict and reporting each.
  - PRs per repo: chosen at start (`openPR`). There is no later "open PR" action.
- **Unaffected repos** (e.g. the CVE is not present): the agent changes nothing, the worktree is dropped, and the member shows "沒有變更".
- **Team hub.** Every repo must be in that one runner's hello. Each member is accepted by that runner's rules: N console asks unless the auto-accept repo list covers them. No hub "accept all".
- **Story room.**
  - Members share agent and model, so they share a figure and already group as one `StoryGroup`.
  - Member tabs: repo name + a pose face.
  - A pip strip shows per-repo status.
  - `storyPlaces` orders members by `groupId`, then repo name.

### 10.3 Checked against typical tasks
| Task | Shape | How |
|---|---|---|
| Upgrade a dependency to vX in every service and fix the breakage | B | per-repo upgrade and tests; failures are continued per member through follow-ups |
| Change every repo's CI workflow | B | `openPR` gives one PR each |
| Fix a CVE in all affected repos with a PR each | B + `openPR` | unaffected repos end with no change |
| Bump a shared library, then make every consumer use it before it is published | A | deferred; two-step B for now |

### 10.4 A, later (behind `CapGlobalMultiRepo`)
`run_repos` table; per-repo changes, merges and workspace actions; a run-folder NoBoard marker plus the `mcp.go` lookup (required for safety); codex `--skip-git-repo-check`; per-repo web keys; one interactive dialog.

---

## 11. Text that becomes pictures (✓ = in this work)

**Station global tab**
1. ✓ agent select → character cards
2. ✓ model select → character cards with model name
3. ✓ repo select → folder cards (multi)
4. ✓ mode radios → `speak` / `write` scene cards
5. later: runner select → card using the pack's `runners` place still
6. ✓ GlobalAgentList status chip → face + pose still (status text kept visually hidden)
7. ✓ repo name column → folder mini-card
8. ✓ group per-repo status → folder card + pose still + pip strip
9. ✓ `noAsk` chip → removed (replaced by the rewritten note)
10. later: RunnerLoad busy / queued numbers → a row of faces
11. later: AutoAccept and full-access notes → icon + tooltip
12. stays text: open PR, base, slug, budget

**Describe box**

13. ✓ agent select → agent cards
14. ✓ folder "Change" → folder card
15. ✓ StartedLine → face + pose still + the dialog

**Story room**

16. ✓ facts list, when a pack is active:
    - status → already shown by the figure (kept visually hidden)
    - runner → `runners` place thumbnail with the name as a tooltip
    - tokens → EnergyHud-style bar
    - held mail → mail icon with count
    - tests → test lamp
    - role and node stay text
17. ✓ member tabs `1. working` → repo name + face
18. ✓ canned line → header flavour only
19. ✓ ask modal → inline `AskCard` row (node runs)
20. later: choice buttons → icon + short label (text kept for a11y)
21. stays text: the agent's output and the person's input; the plate's model name

---

## 12. API contracts

```
POST /api/global-runs/:id/follow-up   {message, mode?:"interactive"}  -> 201 Run (continueOf, workRoot)
  409 CONTINUED{latestId} | IN_USE | WORK_GONE | HAS_PR | RUN_LIVE
  412 RUNNER_OUTDATED   429 budget (as today)   400 field "message"
GET  /api/global-runs/:id/thread      -> {runs: Run[]}   oldest first, <= 50
POST /api/global-runs/:id/end  |  POST /api/runs/:id/end
  -> 202 Run   403 outside mayType   409 not a running interactive run   412 old runner
POST /api/global-runs/groups          {runnerId, repoIds[2..30], agent, model, base?, task, slug?, openPR, simple}
  -> 201 {group, runs}   400 GROUP_NEEDS_BACKGROUND   429 {room}
GET  /api/global-runs/groups/:id      -> {group, runs}
POST /api/global-runs/groups/:id/cancel     -> {cancelled, runs}
POST /api/global-runs/groups/:id/resume     -> {group}
POST /api/global-runs/groups/:id/follow-up  {message, only:"failed"|"all"} -> {started: Run[], skipped:[{runId, code}]}
Run += continueOf?, workRoot?, continuedBy?, viewOnly?, groupId?     RunEvent.type += "user"
Runner: hub->runner  end {runId}
        runner->hub  accept {runId, base, branch, viewOnly}
        hello        agents[].interactive, capabilities += "continue"
Realtime: global-group-changed (dispatcher + owner only)
```

All routes are `personOnly`, use `canSee`, and send global notifications only to the dispatcher and the runner owner. Hub refusals use `locale.Errorf` en/zh pairs.

---

## 13. DB

**`0040_run_continue`**
```sql
ALTER TABLE runs ADD COLUMN IF NOT EXISTS continue_of TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS work_root   TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS view_only   BOOLEAN NOT NULL DEFAULT FALSE;
-- one live follow-up per run; a follow-up cancelled before it was accepted (withdrawn) frees the slot
CREATE UNIQUE INDEX IF NOT EXISTS runs_one_continuation ON runs (continue_of)
  WHERE continue_of <> '' AND NOT (status = 'cancelled' AND <accepted-at column> = '');
CREATE INDEX IF NOT EXISTS runs_work_root ON runs (work_root) WHERE work_root <> '';
```
F1 checks the real "accepted/started" column name in `0010_runs`. If none exists, it adds `withdrawn BOOLEAN` and uses that in the index instead.

**`0041_run_groups`**
```sql
CREATE TABLE IF NOT EXISTS run_groups (id TEXT COLLATE "C" PRIMARY KEY, owner_id TEXT NOT NULL,
  runner_id TEXT NOT NULL, created_by TEXT NOT NULL, created_by_name TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL, agent TEXT NOT NULL, model TEXT NOT NULL, no_pr BOOLEAN NOT NULL,
  repo_count INTEGER NOT NULL, held_until TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
ALTER TABLE runs ADD COLUMN IF NOT EXISTS group_id TEXT COLLATE "C" NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS runs_group ON runs (group_id, created_at) WHERE group_id <> '';
```
- `runs_global_shape` (`postgres.go:1178-1181`) does not mention these columns and is unchanged.
- The `runs.workspace` CHECK is unchanged (`continued` is derived).
- Two migrations already share the number 0036. The names above are the next free ones after `0039_account_preferences`.
- Update `postgres_only_test.go:127-128`.
- Retention: `run_groups` rows are pruned with their last member (`analytics.Prune`).

---

## 14. Security guards

- **The hub names run ids only.** The runner resolves folder, branch, HEAD and session from its own `work.json` and git. It never checks out a hub-named ref and never resumes a hub-supplied session. `work.json` is 0600; ids are checked against the run-id regex; a folder is reused only after the git-dir ownership check.
- **Global runs and groups:** the runner must be the caller's own, routes are `personOnly`, agents are refused, and every repo must be in that runner's hello. Never across runners.
- **No new input path into a running agent.** The composer uses the existing terminal socket, so `mayType`, `remoteTyping`, audit, the 64 KiB/s bucket, the Origin / Sec-Fetch checks, `StillAuthorized` and the 64 KiB cap all still apply. Admins never type. Queued replies are new *offers*, not input.
- **Asks** go only through `answerAsk` with `MayAnswer` (permission prompts are the runner owner's alone), runScope tool/command/once, and truncated input means once.
  - No relay is added for global or interactive runs, and `noRelay` is unchanged.
  - Interactive permission prompts stay in the TUI, answered by whoever may type: the dispatcher or the owner. For global runs those two are the same person.
- **End** is limited to the `mayType` set. Publish runs unchanged: `guardGitDir`, the NoBoard mark, autoPR rules.
- **Offers** on a team hub still wait for the runner's console, its auto-accept policy, or the remote-typing consent. Follow-ups and group members are accepted one by one. `Origin=person` and `Simple` lift no gate.
- **Redaction.**
  - The follow-up text is the person's own task, the same as a global task today. It is stored as the seq-0 `user` event and is never put in the audit log.
  - `ContinueSummary` comes from an already-redacted summary.
  - R3 transcript events go through `emit`, so `redact.Output` applies.
  - Terminal bytes stay unredacted, as today.
- **Session ids** are validated by a regex, stay on the runner, and go in argv only.
- **Budget** is checked per follow-up and for all N group members, and re-checked at Pump.
- **Transcript tail (R3).** The runner reads only `~/.claude/projects/*/<session-id>.jsonl` for the UUID it chose. It requires a regular file (no symlink) owned by the runner user, and it only tails (read-only).

---

## 15. i18n keys (all in `web/src/i18n.ts`, en + zh-TW)

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
| cut | (longer text was cut) | （較長的內容已截斷） |
| screenGone | The screen is kept 10 minutes after the end. Its last lines: | 畫面只保留到結束後 10 分鐘。最後幾行： |
| send / done / stop / withdraw | Send / Done, keep the work / Stop / Take back | 送出 / 完成，保留成果 / 停止 / 收回 |
| newWork | Start new work with this | 用這句開始新工作 |
| continuedBelow | Continued below | 接續在下方 |
| ph.type | Type to the agent, Enter to send | 輸入要對 agent 說的話，按 Enter 送出 |
| ph.followUp | What should change next? (e.g. make the button bigger) | 還想改什麼？（例如：按鈕再大一點） |
| ph.queued | Waiting for the runner to start it | 等待 runner 開始 |
| ph.waitingOwner | Waiting for {who} to say yes on that computer | 等待 {who} 在那台電腦上同意 |
| ph.background | It will read this when it finishes | 它做完就會看到 |
| ph.resetAt | Ready again at {time} | {time} 後可再繼續 |
| ph.viewOnly | The runner's owner did not allow typing from here | runner 的擁有者不允許從這裡輸入 |
| ph.notYours | Only {name} can reply | 只有 {name} 能回覆 |
| ph.hasPr | Continue on its pull request instead | 請在它的 pull request 上繼續 |
| ph.nodeRun | Dispatch the node again to continue | 請重新派送這個節點以繼續 |
| err.continued / inUse / gone / outdated / live | Already continued; showing the latest turn / Another turn of this work is still running / This work's folder is gone / Update nodevas on the runner to continue work / Type into its screen instead | 已有後續，改顯示最新一輪 / 這份工作還有一輪在執行 / 這份工作的資料夾已不在 / 請更新 runner 的 nodevas 才能接續工作 / 請直接在它的畫面輸入 |
| resumeLost | Continuing without the agent's memory of the last turn | 這一輪 agent 不記得上一輪，改用摘要接續 |
| tool.run/write/edit/read/search/web | Run / Write / Edit / Read / Search / Look up {target} | 執行 / 寫入 / 修改 / 讀取 / 搜尋 / 查詢網路 {target} |
| tool.files | {first} and {count} more | {first} 等 {count} 個檔案 |
| tool.todo | Update the to-do list | 更新待辦清單 |
| tool.agent | Send a helper: {target} | 派出助手：{target} |
| tool.mcp / tool.board | Use {tool} from {server} / the board | 使用 {server} 的 {tool} / 看板 |
| tool.claim / getNode / setStatus | Take the task / Read the task / Report status | 接下任務 / 讀取任務 / 回報狀態 |
| tool.other / thinking / failed / running | {tool} / Thinking / failed / working… | {tool} / 思考 / 失敗 / 處理中… |

**`picker.*`**
| key | en | zh-TW |
|---|---|---|
| agent / model / repos | Who works / Which model / Which folders | 誰來做 / 用哪個模型 / 哪些資料夾 |
| all / none / picked | All / None / {count} picked | 全選 / 全不選 / 已選 {count} 個 |
| filter / more / defaultModel | Find a model / {count} more models / CLI default | 搜尋模型 / 還有 {count} 個模型 / CLI 預設 |
| mode.interactive / mode.background | Talk with it / Background | 互動 / 背景 |
| mode.group | Several folders run in the background; talk to any one after it finishes | 多個資料夾會在背景執行；任一個完成後都能接著對話 |
| mode.fallback.agent / teammate / autoAccept / staged / pipeline | {agent} cannot run interactively here / Someone else's runner runs it in the background / Its auto-accept covers background runs only / A staged node runs in the background / Fallbacks and pipelines run in the background | {agent} 在這裡不能互動執行 / 別人的 runner 會在背景執行 / 它的自動接受只涵蓋背景執行 / 分階段的節點會在背景執行 / 備援與管線會在背景執行 |

**`globalAgents.group.*`:** title `{count} folders` / `{count} 個資料夾`; progress `{done} of {count} done` / `已完成 {done}/{count}`; stopAll 全部停止; applyAll 套用全部已完成的; tellAll 告訴全部; tellFailed 告訴失敗的; paused `Paused until {time}: usage limit` / `已暫停到 {time}：用量上限`; resume 立即繼續; room `Today's limit leaves room for {count} runs` / `今天的上限只剩 {count} 次`; noChange `Nothing to change here` / `這裡沒有需要改的`.

**Rewritten:**
- `terminal.modeNoteLocal`: "Runs the agent's own terminal on this computer and shows it here; you can type to it." / 「在這台電腦的終端機中執行，畫面顯示在這裡，可以直接輸入。」
- `terminal.modeNote`: the same, with "on the runner".
- `globalAgents.noAsk`: "Background runs cannot ask you anything; reply here when it finishes to continue." / 「背景執行時無法問你問題；完成後在這裡回覆即可接續。」

---

## 16. Work packages

Rules:
- File sets are disjoint. Shared files live only in the foundation packages F1 and F2.
- Every package starts with failing tests.
- Each package ends with `go test ./...` or `npx vitest run <its files>` + `npx tsc --noEmit` green.

### Wave 0: foundations (F1 and F2 in parallel)

**F1: Go contracts, DB, store.** No dependencies.
- **Files:**
  - `internal/runnerwire/{runs.go, wire.go, global.go, workspace.go, terminal.go}` + `*_test.go`
  - `internal/db/postgres.go` (0040, 0041), `internal/db/postgres_only_test.go`
  - `internal/runs/store.go`: columns, `New` fields, Create guard, `Successor`, `ActiveInWork`, `PriorFinal`, `GroupHeld`, `UserFirst`, `SetViewOnly`, `SetGroupHold`
  - `internal/runs/dispatcher.go`: **only** the two Pump skips (prior not final; group held)
  - `internal/runner/adapter/adapter.go`: `RunEnv.SessionID` / `RunEnv.Resume`, `SessionReporter`, `Resumer`, `ContinueInput`
  - `internal/runner/jobs.go`: **only** the `run` struct fields `session`, `workRoot`
  - `internal/httpapi/runs/{global.go, api.go}`: route registrations, plus new stub files `internal/httpapi/runs/followup.go` and `groups.go` whose handlers answer 501 (G1 and G2 take over these files)
- **Tests first:**
  - `TestContinueJobShape`, `TestGroupRequestBounds` (1, 2, 30, 31), `TestValidSessionID`, `TestHelloAgentInteractive`
  - `TestMigrationsAddContinueAndGroups`
  - `TestStoreContinueColumnsGlobalOnly`, `TestOneLiveContinuationPerRun` (a withdrawn one frees the slot)
  - `TestUserFirstSeqZero`
  - `TestPumpHoldsAFollowUpUntilPriorFinal`, `TestPumpSkipsAHeldGroup`

**F2: Web foundation.** No dependencies.
- **Files:**
  - `web/src/i18n.ts` (every §15 key)
  - `web/src/api/{runs.ts, globalRuns.ts, runners.ts, terminal.ts}` (types, `followUp`, `endRun`, `thread`, group calls, `RunnerAgent.interactive`, `viewOnly`) + their tests
  - `web/src/preferences/schema.ts` (`runMode`)
  - `web/src/game/warroom/castArt.tsx` (new; move `still` / `Face` / `skinVars`) and `web/src/game/warroom/StoryRoom.tsx` (**imports only**)
  - `web/src/app.css` + new `web/src/styles/32-run-dialog.css`, `33-pickers.css` (empty, with header comments)
- **Tests first:** `globalRuns.test.ts` (paths and bodies), `terminal.test.ts` (readonly from `viewOnly`), `castArt.test.tsx` (deepseek → DeepSeek still; `default` → the agent's character; broken → undefined), and an i18n parity check (en and zh-TW keys equal).

### Wave 1: parallel after F1 / F2

**G1: Hub follow-up, End, view-only.** After F1.
- **Files:** `internal/runs/{followup.go (new), workspace.go, terminal.go}`, `internal/httpapi/runs/{followup.go, terminal.go}`, `internal/httpapi/runners/api.go`, `internal/server/followup_test.go` (new), `internal/runs/followup_test.go` (new).
- **Tests first:**
  - `TestFollowUpContinuesTheSameWork`, `TestFollowUpChainsRoot`, `TestFollowUpQueuedWhilePriorRunsInBackground`
  - `TestFollowUpRefuses{AnotherPerson, Agent, LiveInteractive, NotLatest, Discarded, MergedLocal, OpenPR, OverBudget, OutdatedRunner, TooLong, NodeRun}`
  - `TestFollowUpAfterUsageLimitWaitsForReset`, `TestFollowUpNeverAuditsText`
  - `TestWorkspaceGateContinued`, `TestWorkspaceInUseByWorkRoot`
  - `TestEndOnlyByTypists`, `TestEndOldRunner412`, `TestAcceptViewOnlyMakesViewersReadOnly`
  - server: person-only, `canSee`, notify only the dispatcher and the owner

**G2: Hub groups.** After F1.
- **Files:** `internal/runs/{global.go, groups.go (new), budget.go, dispatcher.go}` (dispatcher: Pump budget re-check, usage-limit hold on finish), `internal/httpapi/runs/groups.go`, `internal/runs/groups_test.go`, `internal/server/global_groups_test.go`.
- **Tests first:**
  - `TestStartGlobalGroup{CreatesNRunsInOneTx, OwnRunnerOnly, RepoNotOnRunner, BudgetForN, RefusesInteractive, WritesUserEvents}`
  - `TestPumpRechecksBudgetForGroupMembers`, `TestUsageLimitHoldsTheGroup`, `TestResumeGroupClearsHold`
  - `TestCancelGroup`, `TestGroupFollowUpFailedOnly`
  - The existing `TestStartGlobal*` stay green.

**R1: Runner work registry and continue.** After F1.
- **Files:** `internal/runner/{work.go (new), jobs.go, global.go, worktree.go, workspace.go, workspace_actions.go, preview.go, kept.go, publish.go, prwatch.go}` + `followup_test.go` (new).
- **Tests first:**
  - `TestFollowUpWorksInTheSameFolder`
  - `TestFollowUpRefuses{StaleLastRun, HeadMoved, BranchMismatch, InUse, ForeignWorktree, RepoMismatch, DirtyAfterSuccess}`
  - `TestFollowUpAcceptsDirtyAfterCancel`, `TestFollowUpRecreatesANoChangeFolder`
  - `TestCheckGlobalAllowsContinue`, plus `global_test.go:51,225` still refuse a handoff
  - `TestWorkspaceActionOnALaterTurn`, `TestContinueRekeysKeptAndWatched`, `TestContinueStillNoBoard`
  - `TestAcceptReportsViewOnly`, `TestResumeOnlyItsOwnSession`, `TestFallbackWhenAgentChanged`, `TestResumeFastFailRetriesFresh`

**R2: Runner interactive End / quiet / detect.** After F1.
- **Files:** `internal/runner/{interactive.go, client.go, config.go, settings.go, detect.go}`, `cmd/nodevas/runner.go`, `interactive_test.go` additions.
- **Tests first:** `TestEndPublishesAnInteractiveRunAsSucceeded`, `TestQuietTimeoutEnds`, `TestQuietZeroIsOff`, `TestInteractiveClaudeGetsSessionID` (records `r.session`), `TestHelloReportsAgentInteractive`, `TestEndUnknownRunIgnored`.

**A1: Adapters.** After F1.
- **Files:** `internal/runner/adapter/{claude.go, codex.go, opencode.go, acp.go, opencode_acp.go, gemini.go, antigravity.go, interactive.go, global.go, continue.go (new)}` + tests.
- **Tests first:**
  - `TestParsersReportSessionID` (each fixture)
  - `TestClaudeResumeArgs` (headless and TUI), `TestClaudeInteractiveSessionIDArg`
  - `TestCodexResumeArgs` (`exec resume <id> -`, sandbox via `-c`)
  - `TestOpenCodeSessionArg`, `TestAgyConversationArg`, `TestACPLoadSession`, `TestGeminiNoResume`
  - `TestContinueSection`, `TestGlobalPromptInvitesClosingQuestion` (update `TestGlobalPrompts`)

**W1: Dialog core.** After F2.
- **Files:** `web/src/runs/toolLine.ts`, `web/src/runs/dialog/{RunDialog.tsx, RunTranscript.tsx, RunComposer.tsx, runLines.ts, useRunDialog.ts}`, `web/src/runs/RunTerminal.tsx`, `web/src/runs/runStore.ts` (chain helper), `web/src/runs/RunTimeline.tsx` (`user` kind), `web/src/styles/32-run-dialog.css`.
- **Tests first:**
  - `toolLine.test.ts`: each fixture's names, codex raw string, apply_patch with several files, `["bash","-lc",X]`, truncated JSON, mcp and board names
  - `runLines.test.ts`: merge, ask pinning, turns
  - `RunTranscript.test.tsx`: full text, merged row expands, error mark, logs hidden but hub notes shown, follows the end, polite announcer throttle
  - `RunComposer.test.tsx`: every §4 row; IME Enter ignored
  - `RunDialog.test.tsx`: interactive lazily mounts the terminal; read-only on `viewOnly`; expired screen shows the summary; chain shows `你` lines; End visible only to typists
  - `RunTerminal.test.tsx`: story palette

**W2: Picture-card pickers.** After F2.
- **Files:** `web/src/station/pickers/{PictureCards, AgentCards, ModelCards, RepoCards, ModeCards}.tsx` + tests, `web/src/styles/33-pickers.css`.
- **Tests first:**
  - radiogroup + arrow keys + roving tabindex; native checkboxes + All / None + count
  - `aria-invalid` on the group
  - deepseek → DeepSeek card; `default` → the agent's character
  - plain initials and no `img` without a pack or on a broken image; no logo assets
  - more than 8 models → filter + "還有 N 個"
  - phone grid (class check)

### Wave 2: parallel after W1 / W2 (and the Wave 1 Go packages for end-to-end)

**W3: Forms + interactive default.** After W1, W2.
- **Files:** `web/src/station/{GlobalAgentForm.tsx, SimpleAgentBox.tsx}`, `web/src/runs/{RunModePicker.tsx, DispatchDialog.tsx}` + `GlobalAgentForm.test.tsx`, `SimpleAgentBox.test.tsx`, `RunModePicker.test.tsx`, `DispatchDialog*.test.tsx`, `DispatchRouting.test.tsx`.
- **Tests first:**
  - Rewrite the combobox queries to radio / checkbox (`GlobalAgentForm.test.tsx:139,239,266`; `SimpleAgentBox.test.tsx:154,166,246,272`; the DispatchDialog files).
  - `defaultDispatchMode` table: capability, agent flag, teammate runner, auto-accept modes, staged, pipeline, fallbacks, group.
  - A picked card is remembered (`runMode`).
  - A group sends `repoIds` and is background only.
  - The Simple box sends `mode` and shows the dialog.
  - `touched` is still set on a card pick.

**W4: Global list + group view + results.** After W1.
- **Files:** `web/src/station/{GlobalAgentList.tsx, GlobalAgentGroup.tsx (new), globalAgentsStore.ts, waiting.ts}`, `web/src/runs/{ResultActions.tsx, resultState.ts}`, `web/src/styles/30-global-agents.css` + tests.
- **Tests first:**
  - a chain row shows the latest turn; one row expanded at a time
  - group counts, pips, "Paused until"
  - apply-all with partial results and a conflict
  - tell-failed calls group follow-up
  - `ResultActions` acts on the latest turn only; earlier turns say "接續在下方"

**W5: Story room.** After W1.
- **Files:** `web/src/game/warroom/{StoryTalk.tsx, StoryRoom.tsx, storyPlaces.ts, office.ts, warroom.css}`, `web/src/station/StationWarRoom.tsx`, `WarRoomTheme.test.tsx` additions, `StationWarRoom.test.tsx`.
- **Tests first:**
  - a run actor shows real output, not the canned line; the canned line sits in the header; with no events the canned line is the body
  - no typewriter on the transcript; `still` honoured
  - the textarea is not hijacked by arrow and digit keys
  - list layout subscribes only boxes in view (≤ 6)
  - facts become pictures when a pack is active (visually-hidden text kept)
  - group member tabs show repo name + face
  - **screenshot bug check:** a global row resolves through `useRowOf`, so the choices never show 開啟執行 or 開啟「…」 for a global run

### Wave 3

**R3: Claude interactive transcript tail.** After R2, A1.
- **Files:** `internal/runner/transcript.go` (new) + `transcript_test.go`. The single call site is added in `interactive.go`; R3 takes that file after R2 merges.
- What it does: with `--session-id` known, tail `~/.claude/projects/*/<id>.jsonl` through `claudeParser` into `emit` (redacted). The summary becomes the last assistant text.
- **Tests first:**
  - `TestTailEmitsTextAndToolLines` (fixture jsonl recorded from 2.1.299)
  - `TestTailRefusesSymlinkOrForeignFile`
  - `TestTailStopsAtEnd`
  - `TestSummaryUsesLastText`
- After R3, an interactive Claude dialog shows **Screen while running** (prompts live there) and **Steps after the end**.

**Order:** F1 ∥ F2 → {G1, G2, R1, R2, A1} ∥ {W1, W2} → {W3, W4, W5} → R3. End-to-end check on the built-in runner after Wave 2.

---

## 17. Out of scope (v1)

- Shape A (one session over N repos).
- Follow-ups on node runs; follow-ups that push to an open PR; a later "open PR" workspace action.
- Live mid-turn input into a background run (claude `--input-format stream-json`, agy stream-json, repeated ACP `session/prompt`). These flags exist and are the next step, giving "talk while it works" with step lines.
- Structured transcripts for interactive agents other than Claude.
- Theme-manifest fields for dialog art or terminal colours.
- Picture cards for the autopilot, roles and routing pickers, and runner cards.
- Groups that span runners; a hub-side group accept.
- An automatic worktree sweep.
- Asks or a permission relay for background global runs.

---

## 18. Open questions for the owner

1. **Simple mode default.** Interactive for Simple users as well (the themed screen and key row), or background + follow-up for them? The design says interactive everywhere, as asked.
2. **Quiet timeout.** 20 minutes, ending as "done, keep the work"? Should Simple use a shorter one?
3. **Group cap.** 30 repos per group? When over budget, refuse the whole group (current design) or start the first K?
4. **Folder art.** A plain folder card only, or a pack `rooms` still picked by a stable hash of the repo id?
5. **Follow-up on a run with an open PR.** Refuse (v1), or push to the same branch so the PR updates (v2)?
6. **Group members' prompt.** Should each member's prompt list the sibling repos ("this is one of N services changing together")? That needs a small `Job` field.
7. **Gemini resume.** Unverified (CLI not installed). Agy, codex, opencode and claude flags were checked on this PC; a live smoke test per CLI is needed before R1/A1 merge.
8. **A per-runner lane** so a 30-member group does not delay project runs on the same runner, or rely on `maxConcurrent`?
