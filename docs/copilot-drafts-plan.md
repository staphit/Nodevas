# Copilot: type (or say) what you want, get the nodes

Base: master 21169dd6, branch `feat/copilot-drafts`. Design only; no code changed yet. This file is the work order for building it on another machine; delete it before the PR is merged (shipped plans are not kept in the repo).

## 0. Owner decisions (2026-11-17) and how to build

Answered by the owner:
- **Q1 caps**: yes, 40 nodes per draft, 12k output tokens, 150 outline nodes in the prompt as defaults, **and the person can raise them** (a Copilot setting, stored server-side per user; a server-config ceiling the setting cannot exceed, so a team hub admin keeps the upper bound; the single-user edition's ceiling is generous). Show the current limit when a draft hits it, with a link to the setting.
- **Q2 coding agent plan → draft**: yes, build P6 (the plan run's nodes land as a draft to preview and edit first).
- **Evals (P7)**: yes, about 30 cases, half zh-TW and half en, run only against local free models, not in CI.

Decided by default (the owner can still change these): Q3 Nano last for both Ask and drafting (it is declared English-only and is the weakest); Q4 keep the UI locale for speech, no switch in v1; Q5 document `OLLAMA_CONTEXT_LENGTH=16384` in setup and do not change the provider; Q6 remove the non-authoring tools (claim/release/index/inbox/team) from the copilot; Q7 keep SimpleAgentBox and drafts apart in v1; Q8 default intent is always "Make nodes"; Q9 the 10 s toast plus an undo endpoint without a time limit while the nodes are unchanged.

How to build (same rules as the rest of Nodevas):
- Packages in §13 have disjoint file sets. Order: P0 and P1 in parallel; then P2 (after P1) with P3 alongside; then P4 (after P1, P2) and P6, P7; P5 last. P0 can ship on its own.
- Test-first: write the failing tests listed in §12 for a package, then the code. Targeted tests per package; the whole suite once at the end: `gofmt -l ./cmd ./internal` (empty), `go build ./...`, `go vet ./...`, `go test ./...` (run alone, not alongside vitest), and from `web/`: `npm exec -- tsc -b`, `npm run check:api-imports`, `npx vitest run`, plus `npm --prefix desktop test`.
- Every UI string in `web/src/i18n.ts`, en and zh-TW (Taiwan usage); the i18n tests must stay green. No new dependencies. Postgres only; migrations append-only.
- Single-user edition and team hub each as described in §9; security in §10 must all hold.
- No local model was installed on the machine that wrote this plan, so the drafter has only been designed, never run. Before calling it done, run the P7 evals and a real smoke test with a local free model (e.g. Ollama with qwen3:8b), and the P6 path with a coding agent on a free model (e.g. `opencode/deepseek-v4.1-flash-free`).

Owner feedback (2026-11-17): 「右下角的語音 co-pilot 感覺不太精準。目標是要讓使用者不用慢慢創建 node，應該打字也行。」

Goal: a person types (or says) a description, sees the proposed nodes and dependencies on the board, fixes them in a few clicks, presses one **Create**, and can undo the whole batch. Typing is the main way in; voice is optional.

---

## 1. Why it is imprecise today (short)

There is no "describe → graph" path. The copilot is a general chat-and-tool loop (about 27 tool schemas) with a prompt written for speech. It writes straight to the board, nothing is drafted first, and the board is not in its context. The precise decomposition prompt exists only in the plan run (`internal/runner/adapter/adapter.go:319-354`).

Ranked causes (full list in the diagnosis):
1. **Weakest model first, declared English-only.** Auto picks browser Gemini Nano before the runner's or loopback Ollama (`choose.go:145` before `:149`, `:153`). Nano is created with `languages: ["en"]` (`web/src/copilot/browserModel.ts:81-82`). Its budget is 16 messages × 2 KiB, about 6000 characters in all.
2. **No structure for the small model.** The Nano tool guide shows parameter names only (`create_subgraph` shows as `edges?, nodes`), the input is constrained to `{"type":"object"}`, and each step can make one call (`browser.go:195-213`, `:332-363`). The Nano prompt also leaves out node types and `start_breakdown`.
3. **No authoring guidance; speech-style prompt for typed text.** Nothing says "read the outline, use one `create_subgraph`, set dependsOn". `prompt.go:157-160` asks for "short, speakable, no long lists" on every turn, because `req.Source` is never passed to `systemPrompt` (`prompt.go:133`).
4. **The board is not in the context.** Only `activeTab` is sent as the selection (`session.ts:42-51`). Existing nodes, kinds and edges reach the model only if it calls `get_graph_outline`. The results are duplicates, wrong kinds and wrong link targets.
5. **No draft or undo; one bad field fails the batch.** Raw JSON is cut to 300 B on the permission card, there is one ask per write, and there is no Ctrl+Z for copilot writes. `create_subgraph` advertises per-node `acceptance`, which the role-less copilot is always refused (`store/roleperm.go:296`). Because the call is all-or-nothing, that one refusal fails every node. `unusableInputs` strips only top-level inputs (`toolbridge/bridge.go:148-171`), so the nested `nodes[].acceptance` stays.

Secondary causes: `turnMaxTokens` 4096 can truncate a big subgraph, and `normalizeCalls` then silently turns the input into `{}` (`turn.go:635-649`). Ollama gets no `num_ctx` or temperature. Voice text is sent unedited. Nothing measures quality: all tests use fake providers.

Typing already works (`Composer.tsx`: Enter sends, IME-safe) but is hidden. The button says 「語音 Copilot」 with a mic icon, the empty state says 「按住 Alt+Space 說話」, and below 480px only a round mic shows.

---

## 2. Decisions proposed (owner to confirm; see §13)

| # | Decision | Why (diagnosis) |
|---|---|---|
| D1 | A **dedicated drafter**: one structured model call with its own prompt, the board outline fed in, output a fixed JSON draft schema. It is **not** the tool loop. | Causes 2–4: the small model no longer has to find the tools, the board and the schema by itself. |
| D2 | **Every write from a description goes through a draft and one Create click**, in every permission mode, bypass included. | Cause 5: precision comes from showing the draft and letting the person fix it cheaply. |
| D3 | **Copilot model drafts by default.** The person's **coding agent** (plan run) drafts only on an explicit 「交給我的 coding agent 規劃」 button, which is suggested for repo-wide goals. | §4. Seconds versus minutes; the coding agent costs the person's quota and needs an online runner with a repo. |
| D4 | **Drafter model order**: runner model, then server key, then loopback Ollama/LM Studio, then Nano. Nano is declared in the UI language. | Cause 1. |
| D5 | **Typing first.** The button becomes 「Copilot」 with a text icon. Voice fills the text box and does not send by itself; a setting turns on "send when I stop talking". | Owner's request; cause 9 (unedited voice). |
| D6 | v1 drafts **create new nodes and link to existing ones only**. Editing or deleting existing nodes stays in the chat tool loop. | Keeps the preview, commit and undo simple and safe. |
| D7 | **Batch undo** on the server: the created nodes go to the trash (restorable). Nodes someone changed since are refused and listed. | No undo today. |

---

## 3. User flow

### 3.1 Entry (typing first)
- The FAB label becomes 「Copilot」 / "Copilot", with a pencil-and-sparkle icon. The mic becomes a small button inside the composer. Below 480px the round button shows the text icon, not the mic.
- Clicking the FAB opens the panel with focus in the text box (already the case, `CopilotPanel.tsx:42-44`). Ctrl/Cmd+Shift+Space does the same. Holding Alt+Space still dictates.
- The composer gets a two-way switch above the box, **建立節點 / 問問題** ("Make nodes" / "Ask"):
  - Default is **Make nodes**. It is remembered per viewer in localStorage, wrapped in try/catch.
  - **Make nodes** goes to the drafter (§5).
  - **Ask** is today's chat tool loop, with the prompt fixes from §8.
  - The chat model also gets a `propose_nodes(description)` client-visible tool. A chat that turns into "make these" can hand off to the drafter, and the result is the same draft card.
  - The switch is deterministic on purpose. No small model guesses the intent.
- Empty state in Make nodes: three example chips, for example 「規劃產品上線：寫文件、測試、發布」, 「把選取的節點拆成小步驟」 and 「小說第三章：三個場景」. Tapping one fills the box; it does not send.
- The box grows to 8 rows. Pasting a multi-line spec or a bullet list is expected and encouraged.
- With canvas nodes selected, a chip shows 「接在 3 個選取節點之後」 ("after the 3 selected nodes"). The canvas selection is sent; it is not limited to the active tab (§5.2).

### 3.2 Voice
- The final transcript lands in the text box, editable and highlighted, with a 「送出」 hint. A pending permission answer by voice (yes/no) stays as is.
- `continuous=true` while the key is held, so long dictation is one text.
- Speech `lang` follows the interface locale, as today. Open question Q4 covers a mixed zh/en override.

### 3.3 Draft card and board preview
After Make nodes, within seconds:
- **Panel**: a Draft card. Its header reads 「草稿：8 個節點、7 條依賴」 and names the model that drafted it. The body is an ordered list: kind icon, title, 「在 X 之後」 chips, ✕ to remove. Validation problems (cycle, unknown kind, duplicate of an existing node) show inline as a warning icon.
- **Board**: ghost nodes and dashed edges on a draft overlay layer, built like `components/canvas/PeerOverlay.tsx`.
  - Placement: right of or below the selected nodes, otherwise in the first empty area to the right of the content.
  - Layout: simple layered layout by dependency depth, done on the client with no new library.
  - The view pans to fit the ghosts. Ghosts cannot be dragged into real nodes, and they are not part of the graph state.
- **Tweaks**, each applied locally and re-validated with a dry-run (§6.1):
  - Rename: double-click a ghost or a list item.
  - Remove: ✕. Its dependents inherit its dependencies, so the chain stays connected.
  - Merge: select two items, then 「合併」. The second title goes into the body, dependencies are unioned and self-links dropped.
  - Reorder or change dependencies: drag from one ghost's handle to another, or use the 「在…之後」 picker in the list. Existing nodes can be targets.
  - Change kind: pick from the kind icon menu.
- **Refine by typing**: with a draft open, the box placeholder becomes 「修改這份草稿…」 (for example 「把第 2 步拆開」, 「發布前加一個審查」). The drafter gets the current draft, including the person's manual edits, plus the instruction, and returns a full new draft. The card marks what changed: + added, ~ renamed or re-linked, − removed. Manual edits are kept unless the instruction contradicts them.
- **Buttons**:
  - 「建立 8 個節點」 is the primary button. Enter in the empty box does the same once the draft has no errors.
  - 「捨棄」 discards the draft.
  - 「交給 coding agent 規劃」 is secondary (§4).
- After Create:
  - The ghosts turn into real nodes in place: the batch is created with positions (§6.2).
  - The new nodes are selected.
  - A toast says 「已建立 8 個節點 · 復原」 for 10 s. Ctrl+Z also undoes while that commit is the last undo entry.

### 3.4 With a theme pack active (pictures over text)
- Ghost nodes become picture cards: a large kind glyph tinted with the theme skin colors, and the title only. The body and dependency chips stay behind a click.
- The draft is "presented" by the theme character that matches the drafting model or agent (`ThemeCharacter.match.agents/models`, `theme-pack/themeManifest.ts:48-58`). It appears in the visual-novel dialogue box with a one-line summary taken from the theme's `lines`, for example 「我排了 8 步，看一下？」. A fallback character is used otherwise. No new art is needed and none is generated; that follows the "art only via theme packs" rule.
- The panel list collapses to a row of kind glyphs with a count. 「展開清單」 opens the text list.
- Without a theme, the compact text list and plain ghost boxes are used.

---

## 4. Who drafts: copilot model or coding agent

| | Copilot drafter (default) | Coding agent plan run (button) |
|---|---|---|
| Time | 3–30 s | minutes |
| Cost | free local model, or the configured key | the person's agent quota |
| Needs | any copilot model (§5.4) | the person's own online headless runner with a repo (`runs/start.go:214-232`, `:549-557`) |
| Knows the code | no | yes (it reads the repo) |
| Good for | lists of steps, story or non-code boards, "after these nodes", refinement | "plan feature X in this repo", large goals |

**Rule:**
- Make nodes always drafts with the copilot model first.
- The card shows 「交給 coding agent 規劃（較深入，約數分鐘，用你的額度）」 when a capable runner is online. It is **highlighted** when either holds:
  - The draft has more than 15 nodes, or the drafter set `needsCodeKnowledge: true` (a field in the schema: the model says "this depends on the code").
  - The person's text names repo paths, files or functions. This is a simple regex on the client and server; no model is involved.
- Pressing it calls the existing `start_breakdown` path (`servertools.go:346-426`). It still asks every time and still files a goal node.
- Phase 3 (P6): the plan run writes **into a draft**, not onto the board. A plan run started with `draft:true` has its `create_subgraph` calls stored as a draft (dry-run plus save) and not applied. The follow-up (`followup.go`) then opens that draft on the card, so the coding agent's plan gets the same preview, tweak, Create and undo. Until P6 ships, the plan run writes directly, as today, and the card says so.

Why not the coding agent by default: it needs a runner and a repo (story boards have none), takes minutes, and spends the person's quota on every small request. Most of the imprecision comes from the context and structure problems the drafter fixes (causes 2–4), not from model size alone.

---

## 5. Drafter (backend)

### 5.1 Draft schema (model output, also the API shape)
```json
{
  "summary": "one line, person's language",
  "needsCodeKnowledge": false,
  "questions": ["optional: only when the description cannot be drafted"],
  "nodes": [
    {"key": "n1", "title": "…", "kind": "task", "body": "≤ 600 chars, optional",
     "after": ["n0", "existing:<id>"], "refs": ["existing:<id>"]}
  ]
}
```
- At most 40 nodes per draft (open question Q1); `create_subgraph` allows 100.
- `kind` is an enum built per project from the outline's `nodeTypes`. No `acceptance`, `assignee`, `writeAccess` or `deadline` in v1.
- `after` replaces `dependsOn` to keep the model's vocabulary simple. The server maps it to `SubgraphNode.DependsOn`, and an `existing:` prefix to an existing node id, which is then checked to exist.
- If `questions` is non-empty and `nodes` is empty, the card shows the questions as chips the person can answer by typing. Structured clarifying replaces the single "if ambiguous, ask" line.

### 5.2 Prompt (new `internal/copilot/draft_prompt.go`)
Modeled on `planPrompt` (`adapter.go:319-354`), with the speech style removed:
- Role: "Turn the person's description into nodes for a project board. Output only the JSON object."
- Kind rules: `nodeTypesParagraph` plus the project's custom types and layout from the outline. Story layout: scene/event/note.
- Rules:
  - One node per actionable step.
  - Titles of at most 60 characters, in the person's language.
  - Add `after` only for real order.
  - Never duplicate an existing node: link to it with `existing:<id>`.
  - Attach to the selected nodes when the text says "after these" or "split this".
  - Leave out what the person did not ask for.
- **Board context, built on the server**: a compact outline as a DATA block marked untrusted (the existing injection rule).
  - Selected nodes in full: id, title, kind, status, deps.
  - Up to 150 other nodes as `id | title | kind | status`, preferring nodes near the selection and open ones.
  - Edges among the listed nodes, project layout and node types.
  - The board is read through the bridge as the person (`get_graph_outline` plus a selection fetch), so per-project read rules apply.
- Two short few-shot examples (zh-TW, en), each under 400 tokens.
- Refine: the previous draft JSON plus the instruction, then "return the full updated draft; keep keys of unchanged nodes".

### 5.3 Call, validation and repair (new `internal/copilot/draft.go`)
- One provider call with a JSON-schema response format where the provider supports it:
  - OpenAI-compatible: `response_format: json_schema` (Ollama supports `format` with a schema; LM Studio supports `json_schema`).
  - Anthropic: a single forced tool, `tool_choice` set to `emit_draft`.
  - Nano: `responseConstraint` with the **full** draft schema. Nano does one structured answer, which is what it is good at; there is no multi-step tool loop.
  - Runner remote (`remote.go`): forward the schema; the runner wire gains an optional `responseSchema` (P3).
- Max tokens for drafts: 12000 (open question Q1). Effort `medium` where supported. Ollama gets `temperature 0.2`; `num_ctx` comes from the runner or server setting or `OLLAMA_CONTEXT_LENGTH` (open question Q5).
- Parse, then map to a `SubgraphSpec`, then **dry-run** `store.CheckSubgraph` (§6.1) as the person.
  - On errors: one repair call that sends the exact error list ("nodes[3].after[0]: unknown key n9; cycle n2→n4→n2").
  - Still invalid after that: return the draft with the per-node errors marked. The person fixes them by hand or by typing. A truncated or invalid JSON answer is never silently turned into `{}`.
- Dedupe hint: titles that normalize (case, width, punctuation, whitespace) to an existing node title in the listed outline get a "maybe duplicate of X" warning with a one-click "link instead".
- The draft is saved as a conversation entry, `EntryDraft` (Postgres, per account and project, `store.go`). Refinements append new entries, and the card shows the latest. Drafts expire 7 days after the last touch.

### 5.4 Model choice for drafting (`choose.go`)
- New `planDraft(...)` that reuses `Choose` inputs:
  1. Admin-set provider, server key, or `keySource=runner`: as today.
  2. Auto with no key: runner auto-local model, then loopback Ollama/LM Studio, then Nano (when available or downloadable). Nano goes last instead of first.
- Nano is created with `expectedInputs/expectedOutputs languages` set from the UI locale, always with `"en"` included. When Chrome rejects a language (zh is not supported by the Prompt API today), it falls back to `["en"]` and the card shows 「目前模型較小，中文結果可能較差」 with the coding-agent button.
- The chat (Ask) order stays as today unless the owner chooses otherwise (Q3).

### 5.5 API (new routes under the copilot handler)
- `POST /api/copilot/drafts` with `{conversationId?, project, text, source, selection[], refineOf?, edited?}` streams SSE: `status` (reading board, drafting, checking), then `draft` or `error`. It shares the per-account turn slot (max 2 on the hub server model).
- `POST /api/copilot/drafts/{id}/check` with `{spec}` returns dry-run issues. Used after manual edits; debounced on the client.
- `POST /api/copilot/drafts/{id}/commit` with `{spec, baseRev, positions}` returns `{batchId, ids, rev}`. The client sends its edited spec, and the server re-validates everything.
- `POST /api/graph/batches/{batchId}/undo` returns `{trashed[], refused[{id, reason}]}`.
- `DELETE /api/copilot/drafts/{id}` discards a draft.

---

## 6. Store changes

### 6.1 Dry-run
- `store.CheckSubgraph(actor, spec) (SubgraphResult, error)`: the same path as `CreateSubgraph` (`subgraph.go:153-379`), stopping before the write, with no history entry and no realtime event.
- `CreateSubgraph` is refactored into `prepareSubgraphLocked` followed by `commitSubgraphLocked` so the two share every check (role, kind, cycle, size).
- HTTP: `POST /api/graph/subgraph?dryRun=1` (`internal/httpapi/graph/subgraph.go`).

### 6.2 Batch record and positions
- `SubgraphSpec` gains optional `Positions map[key]{x,y}`, so the nodes land where the ghosts were, and `BatchID`. When positions are absent, today's placement applies.
- `recordBatchLocked(batchID, actor, ids, perNodeRev)`, alongside `created_by_run` (`roleperm.go:570-623`). A batch is `{id, project, actor, via:"copilot-draft:<draftId>", ids, createdAt}`, stored with the project data the same way `created_by_run` is.
- History entry note: 「由 Copilot 草稿建立」 / "created from a Copilot draft".

### 6.3 Batch undo
- `store.UndoBatch(actor, batchID)`:
  - Allowed for the batch's actor, the project owner, or an admin.
  - Moves each node to the trash only if it is unchanged since the batch (same node rev), not claimed, and has no run on it. Edges to and from it go with it, as a normal delete does.
  - Changed nodes are kept and listed as `refused`. The UI offers 「仍要刪除」 per node, through the normal delete with its usual checks.
  - The trash keeps them restorable.
- Web: `pushUndo({kind:"batch", batchId})` after the commit. Undoing it calls the endpoint, and redo restores from the trash (`undo.ts` gets a new kind).

---

## 7. Web packages (new and changed)
- `web/src/copilot/draft/`:
  - `draftStore.ts`: a pure reducer for the draft, edit ops and refine diff. Edit ops: rename, remove (bridging deps), merge, relink, changeKind. Includes client cycle detection for instant feedback.
  - `DraftCard.tsx`, `DraftList.tsx`, `draftLayout.ts`: layered layout to ghost positions.
  - `draftApi.ts`
- `web/src/components/canvas/DraftOverlay.tsx`: ghost nodes and edges, drag-to-link, theme picture cards.
- `CopilotRoot.tsx`: FAB label and icon. `Composer.tsx`: intent switch, voice into the box, growing box. `Transcript.tsx`: empty-state chips; draft entries render `DraftCard`.
- `session.ts`: send the canvas selection, `sendDraft`, `refineDraft`.
- `15-copilot.css`: text-icon FAB, draft card, theme variants.

---

## 8. Fixes to the existing chat path (small and independent; ship first)
1. Pass `req.Source` into `systemPrompt`. Text turns get "clear, structured; lists are fine". Voice keeps the speakable style.
2. Strip nested `nodes[].acceptance` from `create_subgraph` for the copilot. Extend `withoutInputs` with path support (`"nodes.items.acceptance"`).
3. Add an authoring paragraph: "before creating several nodes read get_graph_outline; prefer propose_nodes (or one create_subgraph); set dependsOn; don't duplicate".
4. `normalizeCalls`: a malformed or truncated input gets a tool error result telling the model the input was invalid, not `{}`.
5. Nano: language from the UI locale (as in §5.4). Add `nodeTypesParagraph` and the `start_breakdown` confirm step to `browserSystemPrompt`.
6. `turnContext`: send the canvas multi-select (up to 50), not only `activeTab`. This needs a store selector for the LaneView selection; if it is truly component-local, lift it into `useApp`.
7. Drop the non-authoring tools `claim_task`, `release_task`, `index_documents` and `check_inbox` from the copilot offer (open question Q6).

---

## 9. Editions

**Single-user (loopback)**
- The drafter runs on the local chain: runner auto-local or loopback Ollama, then Nano. With an API key set, that is used.
- No turn limit. The one person commits. Undo works the same.
- Simple mode: the Copilot stays mounted. `SimpleAgentBox` stays a separate "do it in a folder" path. Optional link (Q7): 「先排成步驟？」 sends its text to Make nodes.
- The coding-agent button uses the built-in runner, which starts right away.

**Team hub**
- Drafter model: `keySource=runner` is recommended, so the key stays on the person's PC (owner-only, `runner/llm.go:354`). Otherwise the server key from `--config`, `NODEVAS_CONFIG` or env only. Off-machine providers and base URLs are never settable from the UI (README security model).
- Drafts are **private to their author** (account plus project). Peers see nothing until Create, and ghosts are not broadcast.
- Drafting counts against the 2-turn-per-account server slot. Runner-model drafts follow the runner's own limits (2 concurrent, 40/min).
- The coding-agent button needs the person's own runner. On a hub the plan run may wait as an offer until accepted on that runner, and the card says so.

---

## 10. Security
- **The model never writes.** Drafter output is data shown to the person. Only the person's Create click writes, through `CreateSubgraph` **as the person** (session cookie identity), not as the `copilot:<name>` agent. All per-project write rules, node-type rules and role checks apply. A viewer without project write sees 「你在這個專案沒有寫入權限」 and the Create button is disabled. The server refuses regardless.
- **Commit and check endpoints:**
  - The draft must belong to the caller's account and project (404 otherwise, no existence leak).
  - The project must be writable by the caller.
  - The spec is re-validated in full on the server. The client spec is untrusted: same limits, plus the draft cap of 40.
  - `baseRev` mismatch: re-run the dry-run against the current board. If it still validates, commit; otherwise return 409 with the issues.
- **Undo**: only the batch actor, the project owner or an admin. Changed nodes are refused (§6.3).
- **Prompt injection**: board content goes into the drafter prompt as a marked data block, with the existing "data, not instructions" rule. The output is constrained by the schema, `existing:` ids are checked to exist and to be readable by the person, and titles and bodies are stored as plain text, as for any node.
- **Bypass mode** never auto-commits a draft (D2).
- **Audit**: the history records the person, `via copilot-draft:<id>`, and the model label.
- **Keys**: the drafter adds no new provider config surface. It uses `Choose`/`planDraft` outputs only.

---

## 11. i18n keys (en / zh-TW, in `web/src/i18n.ts`)
| key | en | zh-TW |
|---|---|---|
| copilot.fab | Copilot | Copilot |
| copilot.intent.make | Make nodes | 建立節點 |
| copilot.intent.ask | Ask | 問問題 |
| copilot.placeholder.make | Describe what you want to get done… | 描述你想完成的事… |
| copilot.placeholder.refine | Change this draft… | 修改這份草稿… |
| copilot.empty.make | Type what you want; you'll see the nodes before anything is created. | 打字描述就好；建立前會先讓你看到節點。 |
| copilot.example.1/2/3 | (three examples) | 規劃產品上線：寫文件、測試、發布 / 把選取的節點拆成小步驟 / 小說第三章：三個場景 |
| copilot.selectionChip | After {n} selected nodes | 接在 {n} 個選取節點之後 |
| copilot.voice.review | Check the text, then send | 確認文字後送出 |
| copilot.voice.autoSend | Send when I stop talking | 說完自動送出 |
| draft.title | Draft: {n} nodes, {e} links | 草稿：{n} 個節點、{e} 條依賴 |
| draft.status.reading/drafting/checking | Reading the board… / Drafting… / Checking… | 讀取看板中… / 草擬中… / 檢查中… |
| draft.create | Create {n} nodes | 建立 {n} 個節點 |
| draft.discard | Discard | 捨棄 |
| draft.deeper | Plan with my coding agent (deeper, a few minutes, uses your quota) | 交給 coding agent 規劃（較深入，約數分鐘，用你的額度） |
| draft.after | After {title} | 在「{title}」之後 |
| draft.merge | Merge | 合併 |
| draft.remove | Remove | 移除 |
| draft.dup | Maybe the same as "{title}". Link to it instead? | 可能和「{title}」重複，改成連結它？ |
| draft.cycle | These steps wait for each other | 這幾步互相等待，形成循環 |
| draft.smallModel | The current model is small; results in Chinese may be weaker. | 目前模型較小，中文結果可能較差。 |
| draft.noWrite | You can't write to this project | 你在這個專案沒有寫入權限 |
| draft.created | Created {n} nodes · Undo | 已建立 {n} 個節點 · 復原 |
| draft.undoRefused | {n} nodes were changed since, so they were kept | {n} 個節點之後被改過，已保留 |
| draft.fromDraft (history) | created from a Copilot draft | 由 Copilot 草稿建立 |
| draft.questions | A few questions first | 先確認幾件事 |

Plus a COPILOT.md rewrite of "Using it": typing first, then Make nodes and Ask, the draft, and undo.

---

## 12. Tests first (written before code in each package)

**Go**
- `store/subgraph_check_test.go`: the dry-run returns the same errors as create and writes nothing (rev unchanged, no history, no event). Positions are applied. The batch is recorded.
- `store/batch_undo_test.go`: undo trashes unchanged nodes and refuses changed, claimed or running ones. Another account is refused, the owner is allowed, and a second undo is a no-op.
- `httpapi/graph/subgraph_test.go`: `?dryRun=1`, plus a viewer without write refused on commit.
- `copilot/draft_schema_test.go`: `after` to `dependsOn` mapping, `existing:` ids checked, kind enum from the outline, the 40-node cap, and acceptance absent.
- `copilot/draft_prompt_test.go`: a golden snapshot of the prompt for zh and en, story-layout and selection cases. The outline is clipped at 150 nodes, and the data block is marked.
- `copilot/draft_test.go` (fake provider):
  - valid on the first call
  - invalid, one repair, then valid
  - invalid twice, so errors are marked
  - truncated JSON gives an error, not `{}`
  - questions-only
  - cross-account draft gives 404
  - commit acts as the person, not the copilot agent
  - `baseRev` drift gives a re-check
  - bypass mode still needs a commit
- `copilot/choose_draft_test.go`: the drafter order puts Nano last; keySource=runner uses only the runner.
- `copilot/prompt_test.go`: source text versus voice style; `toolbridge/bridge_test.go`: nested acceptance stripped; `turn_test.go`: malformed input gives an error result.
- `runner/llm_test.go`: `responseSchema` is forwarded and size-limited.

**Web (vitest)**
- `draftStore.test.ts`: rename, remove-bridges-deps, merge (deps union, no self-link), relink, changeKind, cycle detection, refine diff markers, and manual edits kept on refine.
- `draftLayout.test.ts`: depth layers, no overlap with existing node rectangles.
- `DraftCard.test.tsx`: list, warnings, Create disabled on errors or without write, questions as chips, and the theme variant shows glyph cards and collapses the text.
- `DraftOverlay.test.tsx`: ghosts at their positions, drag-to-link calls relink; built from `boardTestKit.tsx`.
- `Composer.test.tsx`: the intent switch is remembered, and a localStorage throw is safe. The voice final text fills the box without sending unless auto-send is on.
- `CopilotRoot.test.tsx`: the FAB label is "Copilot", with the text icon below 480px.
- `session.test.ts`: the canvas selection is sent.
- `undo` test: the batch kind calls the endpoint.
- i18n parity: the existing en/zh key check covers the new keys.

**e2e** (`web/e2e/copilot-draft.spec.ts`, fake model): type, see the draft, remove one node, Create, nodes appear, Undo, nodes gone.

**Quality eval** (new; not in CI): `internal/copilot/evals/`, run with `go test -tags copilot_eval ./internal/copilot/evals -model ollama:qwen3:8b`.
- About 30 cases (15 zh-TW, 15 en), each a fixture board, a description, and expected properties: node count range, required title keywords, kinds, required order pairs, no duplicates of existing nodes, attaches to the selection.
- Reports a score per model. Run against local and free models only; no paid API is called unless the owner passes a key explicitly.
- It gives a baseline before and after the drafter.

---

## 13. Packages (disjoint files; order P0 → P5, P6 optional)

| Pkg | Scope | Files (only these) | Depends |
|---|---|---|---|
| **P0** chat fixes | §8 items 1–5, 7 | `internal/copilot/prompt.go`, `prompt_test.go`, `internal/copilot/toolbridge/bridge.go`, `bridge_test.go`, `internal/copilot/turn.go` (normalizeCalls only), `turn_*_test.go`, `internal/copilot/browser.go`, `browser_test.go`, `web/src/copilot/browserModel.ts`, `browserModel.test.ts` | none |
| **P1** store dry-run, batch, undo | §6 | `internal/store/subgraph.go`, new `internal/store/subgraph_check.go`, new `internal/store/batch.go`, tests; `internal/httpapi/graph/subgraph.go`, `routes.go`, new `internal/httpapi/graph/batch.go`, tests | none |
| **P2** drafter backend | §5 | new `internal/copilot/draft.go`, `draft_prompt.go`, `draft_schema.go`, `draft_http.go`, `choose_draft.go`, tests; `internal/copilot/store.go` (EntryDraft); provider structured output in `internal/copilot/provider/openai.go`, `anthropic.go`; routes registered in the copilot routes file | P1 (store API) |
| **P3** runner wire | `responseSchema` forwarding | `internal/runnerwire/llm.go`, `internal/runner/llm.go`, `internal/copilot/remote.go`, tests | P2 contract only |
| **P4** web draft UI | §3.3–3.4, §7 | new `web/src/copilot/draft/*`, new `web/src/components/canvas/DraftOverlay.tsx` + test, `web/src/state/undo.ts` (batch kind), `web/src/copilot/Transcript.tsx`, `web/src/copilot/session.ts`, `web/src/i18n.ts` (**all** new keys, including P5's), `web/src/styles/15-copilot.css` | P1, P2 APIs |
| **P5** typing-first entry | §3.1–3.2 | `web/src/copilot/CopilotRoot.tsx`, `Composer.tsx`, `speech.ts`, their tests, `COPILOT.md`, `web/e2e/copilot-draft.spec.ts` | after P4 (i18n keys) |
| **P6** plan run into a draft (phase 3) | §4 | `internal/copilot/servertools.go`, `followup.go`, `internal/runs/start.go` (draft flag), `internal/mcp/tools_author.go` (draft-mode create_subgraph routing), tests | P1, P2, owner yes on Q2 |
| **P7** evals | §12 | new `internal/copilot/evals/*` | P2 |

Shared-file notes:
- `i18n.ts` belongs to P4 only.
- `turn.go` belongs to P0 only; P2 adds no turn changes (drafts have their own handler).
- `session.ts` belongs to P4. P5 calls the functions P4 adds.
- The canvas selection lift (§8 item 6) touches `LaneView.tsx` and an app slice. It goes in P4 with `session.ts`.

---

## 14. Out of scope (v1)
- Drafts that edit, retitle, re-status or delete **existing** nodes. The chat Ask path handles these, with today's permission cards.
- Acceptance criteria, assignees, deadlines and writeAccess in drafts. They are set after creation in the inspector.
- Server-side speech-to-text (Whisper). Voice stays on the Web Speech API, Chrome/Edge only.
- Shared or co-edited drafts across hub members, and live ghost broadcast.
- Plan runs without a repo (story projects) and a team-hub SimpleAgentBox.
- New theme art. Only existing pack characters, skin colors and words are used.
- Improving Nano's Chinese quality itself.

---

## 15. Open questions for the owner
- **Q1** Caps: 40 nodes per draft, 12k output tokens, 150 outline nodes in the prompt. OK?
- **Q2** P6: should the coding agent's plan also land as a draft (preview first), or keep writing straight to the board as today?
- **Q3** Should the Ask (chat) model order also put Nano last, or only drafting?
- **Q4** Speech language: keep the UI locale, or add a 「語音語言」 switch (zh-TW, en) for mixed technical terms?
- **Q5** Ollama context: set `num_ctx` through the native `/api/chat` (a provider change), or document `OLLAMA_CONTEXT_LENGTH=16384` in setup?
- **Q6** Remove the non-authoring tools (claim/release/index/inbox) from the copilot entirely?
- **Q7** Simple mode: link SimpleAgentBox to Make nodes (「先排成步驟？」), or keep them apart?
- **Q8** Default intent: always Make nodes, or Ask when the board is empty of a project? (Proposal: always Make nodes.)
- **Q9** Undo window: the toast lasts 10 s, but the batch undo endpoint has no time limit while nodes are unchanged. OK?
