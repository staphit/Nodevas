# Agent 工作站

English: [workstation.md](./workstation.md)

server（**hub**）負責顯示看板、run 與改動，但從不啟動任何程式。你電腦上的 **runner**（`nodevas runner`）主動連到 hub，接下你同意的 run，用你自己的登入執行 agent CLI。程式碼、agent 登入與 `gh` 登入都只留在 runner 上，而且它只執行你同意的工作，並守住自己的限制（見[安全模型](../README.zh-TW.md#安全模型)）。

- **Run**：分成 headless 與互動兩種。headless 把事件串流到節點上；互動模式把 agent 的 TUI 放進瀏覽器。runner 會 commit、push，再用 `gh` 開 PR，並回報 diff 與 CI 狀態。
- **模型**：每次 run 可以自己選 agent 與模型，也可以交給路由規則與 fallback 鏈決定。遇到 rate limit、額度用完或 CLI 不存在時，就改用鏈上的下一個模型。OpenCode 也可以改走 [Agent Client Protocol](https://agentclientprotocol.com)（`nodevas runner set opencodeProtocol acp`，實驗性，僅限 OpenCode 1.x；OpenCode 2 的 run 會改用 `opencode run`）：每個工具呼叫即時串流；權限提示見[權限提示](#權限提示預設關閉)。
- **審查管線**：實作完成後，可以交給其他 agent 或模型審查、交給人審，或兩者都要。要求修改時會再跑一次 revise run。runner 只把修改推到它自己的 run 為那個節點開的分支（記在它的 PR 監看清單）；別的成員的 PR，或監看紀錄已不在的 PR（例如關閉後又重開），會被拒絕。
- **Agent 對話**：agent 可以看到還有誰在工作、互相傳訊息，也能透過 hub 停下來問你（`ask_user`）；見[回覆你的 agent](#回覆你的-agent)。
- **工作站**：每個專案的工作站（頂列的「看板｜工作站｜設定」切換）把這些都放在同一個畫面：
  - 頂端的 **全部暫停**：暫停你啟動的自動推進，並取消還沒開始的 run（也可以一併取消執行中的）。之後按 **全部恢復**，這些自動推進就從停下的地方繼續；
  - **等你處理**：提問與權限提示、暫留訊息、審查、要合併或修正的 PR、runner 還沒接受的 run、用量上限與每日預算；
  - 目標的自動推進：在目標節點面板頂端按 **自動推進**，就會在這裡開啟；
  - Agent 動態：把整個工作區畫成一棟像素風大樓，有這個專案的辦公室、其他專案的辦公室、閒置 agent 共用的休息室，以及每個 runner 一座機櫃的機房。每個 agent 都是按鈕：它的選單可以開啟它的 run、改動、節點或目標，暫停或恢復它的自動推進，或取消這次 run；
  - 過去一小時誰做了什麼；
  - 側欄有今天的花費、runner，以及可以直接發訊息的 Agent 對話；
  - **全域 agent**：與這個專案並列的分頁，列出在你 runner 的 repo 裡工作、不屬於任何專案的 agent；見[全域 agent](#全域-agent)。

  agent 提問、權限提示、用量達上限、每日預算用完時，瀏覽器也會通知你。
- **安全預設**：
  - **只在一台電腦上自己用（單人版）**：你在 app 裡開始的 run 會直接在這台電腦上開始，不需要終端機：按下按鈕就是同意。只保留一道防線：自動開始的 run（自動推進、審查或修改回合、後備模型，或用量上限後接續）若用的是你設定為 **在這台電腦上執行任何指令** 的 agent，會在 app 裡等你確認，除非你對那個 agent 勾了 **之後不用再問**。agent 會寫看板節點，自動推進又會把節點變成提示，所以被提示注入的 agent 可能埋下一個工作，讓有完整權限的 agent 不經詢問就執行。出廠設定下沒有任何 agent 能執行任何指令。**每個工作開始前都先問我** 會在每個 run 開始前詢問。只有 `serve` 為自己啟動、直接從這台電腦連線的 runner 會這樣處理。
  - **共用的電腦**：單人版信任所有能使用這台電腦的人。同一台電腦上的其他使用者帳號可以透過 loopback 開啟 app，而 run 會直接開始，所以它能以你的身分啟動 agent。請只在你一個人使用的電腦上用單人版；共用的電腦請改用有帳號的團隊 hub。那道防線也不是沙箱：這台電腦上任何沒有被沙箱限制的程式（包括可以執行任何指令的 agent）都能以你的身分回答它。
  - **團隊 hub**：run 需要你在 runner 所在的電腦上、在它的終端機同意；hub 只顯示「等待在某台電腦上核准」與原因。hub 永遠不會核准 run，所以被入侵的 hub 無法在那裡啟動 run；hub 所說的「誰開始了這個 run」只會顯示，從不採信。遠端派工需要 step-up passcode，加上你在 runner 上設定的 auto-accept 白名單。
  - 遠端 merge 預設關閉，要自己打開。
  - 審查、拆解與評審（目標 QA 與檢查清單檢查）一律唯讀，而且不載入 repo 自帶的 agent 設定；Claude Code 在這些 run 裡也不能用 WebFetch、WebSearch 或 Monitor。
  - run 新增或修改的 agent 設定（`.claude/settings.json`、`.mcp.json`、`.codex/`、`.gemini/`、`opencode.json` 等）不會進它的 commit，worktree 會保留下來，讓你自己決定要不要 commit。`nodevas runner status` 會列出保留的 worktree；`nodevas runner prune-kept [--dry-run]` 移除只剩被擋下設定的那些（分支保留）；不會自動移除。讀寫這份清單時會加鎖（runner 資料夾裡的 `kept-worktrees.lock`），所以 `prune-kept` 或共用同一個資料夾的另一個 runner，不會弄丟 run 剛保留的 worktree。
  - headless run 的權限提示一律拒絕，除非你[轉給 Nodevas](#權限提示預設關閉)；自動接受的 run 只有在打開 `allow-shell` 時才會轉送。
  - 專案總管的 **檔案** 分頁只讓你本人唯讀瀏覽你 runner 上的 repo，並隱藏看起來像祕密的檔案（`.env`、金鑰、`.ssh`、憑證）。執行 `nodevas runner set repoBrowse off` 並重啟 runner 即可關閉。
  - run 的 **工作區**（它的 worktree、分支與變更）只有你本人能瀏覽、在本機合併或捨棄，管理員與 agent 都不行；見 [Agent 工作區](#agent-工作區)。執行 `nodevas runner set workspaceActions false` 並重啟 runner，即可關閉合併與捨棄。
  - **全域 agent** 只能在你自己的 runner、已登記的 repo 裡啟動，沒有看板；除非你要求開 PR，分支都留在 runner 上。見[全域 agent](#全域-agent)。執行 `nodevas runner set globalRuns off` 並重啟 runner，即可拒絕全域 agent。
  - hub 放在 Cloudflare Access 後面，runner 用 service token 連線。
  - 可以用驗證器 App 開啟兩步驟驗證（**設定 → 帳號**）。開啟後登入還要輸入 App 的驗證碼，step-up 也改問 App，不再寄 email。

app 裡與 agent 有關的部分（runner、run、自動推進、Agent 對話與用量）預設隱藏，直到你打開 **設定 → Agent 與 Runner → 讓 agent 在這裡工作** 或進入工作站，或這個工作區已經有 runner、run 或連線中的 agent。只在一台電腦上自己用時，內建的 runner 一直都在，所以要等你替它加入 repo 之後才算。

設定只要兩步：

1. 部署 hub：見 [Getting started: Team server](./getting-started.md#team-server)。
2. 在 hub 上打開 **設定 → Agent 與 Runner**，打開 **讓 agent 在這裡工作**，再按 **連結新裝置**，複製一次性連結。在你的電腦上執行 [`scripts/install.sh --hub <網址> --link`](./install.md#set-up-a-new-pc-with-a-device-link)，帶入允許 run 使用的 repo，出現提示時貼上連結。連結只能用一次、十分鐘內有效；若管理員存過 hub 的 Access service token，連結也會一併帶上，不必再從 Cloudflare 手動複製。

只在一台電腦上自己用時，一個指令就能同時啟動兩者：`nodevas serve --project ./workspace`（只要 hub 時加 `--no-runner`），或使用桌面 App。runner 內建在 app 裡：用 **＋ 新增 repo**（或 **設定 → Agent → 這台電腦**，也在那裡選每個 agent 可以做的事）新增 repo，你開始的 run 會直接開始，完全不需要終端機。見 [Hub and runner on one machine](./install.md#hub-and-runner-on-one-machine)。

## 回覆你的 agent

agent 可以停下來透過 Nodevas 問你（`ask_user`）：請你核准（**核准** 或 **拒絕**，可附備註）、問一個要你用文字回答的問題，或請你從 2 到 8 個選項中選一個。問題與選擇都可以 **不回答**。agent 最多等 10 分鐘。

可以在 Agent 對話（Ctrl+Shift+A，或工作站側欄的卡片）、工作站的 **等你處理**，或 Agent 動態裡的那個 agent 上回覆；瀏覽器也會通知你。run 的時間軸（節點面板 → **活動** → 該 run）會列出它的每個提問與結果；run 結束時仍未回覆的提問會失效。只有發問 agent 的主人或管理員可以回覆。

### 權限提示（預設關閉）

headless run 沒有人可以問，所以 CLI 要求的權限一律拒絕。若要改成問你，在 runner 上執行 `nodevas runner set permissionRelay true` 並重啟 runner（`nodevas serve` 啟動的 runner 要再加上 `serve` 啟動時印出的 `--config` 路徑）。之後下列 CLI 會把權限提示轉給 Nodevas：

- Claude Code，`plan` 與 `dontAsk` 以外的任何權限模式；
- Antigravity 的 `accept-edits`，限 shell 指令；
- 走 ACP 的 OpenCode 的 `edit`，限 shell 指令。

Codex、Gemini CLI 與 `opencode run` 無法轉送權限提示；互動 run 則在它自己的終端機裡問。有轉送的 run，開頭那一行以「permission prompts relayed to Nodevas」結尾。

只有 runner 的主人可以回覆權限提示，回覆的地方和其他提問相同：

- **允許**：只限這一次呼叫。
- **本次執行剩餘期間一律允許 *工具***：這次 run 之後呼叫同一個工具都不再詢問。shell 指令的按鈕是 **本次執行剩餘期間一律允許這條完全相同的指令**，只涵蓋那一條指令。輸入超過 4 KB 被截斷時，兩者都不提供。
- **拒絕**。

網頁上的權限提示沒有備註欄。沒人回覆的權限提示 5 分鐘後視同拒絕；run 結束時，尚未回覆的權限提示會失效。

轉送打開後，你的回覆會經由 hub 送到 runner，所以被入侵的 hub 可以替你允許一個你從沒看過的權限提示。除非你信任這個 hub，否則請保持關閉。轉送不會放寬任何 run：CLI 要先過自己的拒絕規則與權限模式才會來問，所以唯讀的 run 仍是唯讀。沒有執行 `nodevas runner autoaccept allow-shell on` 而被自動接受的 run 不會轉送；它的開頭那行會以「permission prompts not relayed: auto-accepted without allow-shell (denied headless)」結尾。

## 用量上限與交接

headless 實作 run 若因 agent 達到用量上限或 rate limit 而停下，runner 會保留已完成的部分，不會丟掉：

- 它把 worktree 提交到本機分支 `nodevas/handoff/<run id>`，只存在 runner 上，不推送、也不開 PR。
- 它寫一份**交接筆記**：agent 被要求做什麼、最後的訊息、改了哪些檔案、最後一個指令，以及它自己計畫中還沒做完的項目。祕密會被遮蔽，筆記上限 8 KB。
- 這個任務的下一次 run 會從該分支接續，不論是 fallback 鏈上的下一個模型，還是額度重置後的同一個模型。筆記以「資料，而非指令」的形式放進它的 prompt。PR 仍以原本的 base 為目標，審查涵蓋整份 diff。
- 若鏈上沒有 fallback 了，run 會等額度重置。重試時間取自 agent 自己的訊息（「resets 3pm」、「try again in 2 hours」、Retry-After），否則用預設退避（rate limit 5 分鐘、額度 1 小時）。run 上會顯示**等待額度重置 · HH:MM 重試**，並有**立即重試**與**取消**。等待在 hub 重啟後仍然有效。時間到時，run 會像一般 run 一樣派送：仍須 runner 接受，也受每日預算限制。一個任務最多等待三次。Autopilot 會把等待中的任務算成排隊中，而不是失敗。
- runner 只會從自己建立、屬於同一個 repository 與節點、且仍停在它記錄的 commit 的交接分支接續。hub 可以指名這樣的分支，但不能指定其他任何 ref。較舊的 runner 在還沒檢查節點時記錄、沒有節點的交接分支不會被接續，該工作會被拒絕（「has no node on record」）。runner 每建立一個交接分支，就會刪除超過 30 天或超出最新 200 個的交接分支，前提是它們仍停在記錄的 commit。

登入無法使用的模型或不存在的 CLI，會在 agent 第一輪之前就失敗；這時沒有成果可保留，等待也沒用，所以這類失敗只沿著鏈往下換模型。

## Agent 工作區

每個 run 都有一個 **工作區** 面板：可以從 run 的那一列、工作站的 run 選單，或 Agent 動態裡的人物或座位開啟。它會顯示 agent 與模型、節點、runner、run 的分支與基準分支、run 的狀態，以及 PR 與 CI。分頁有 **檔案**（runner 上這次 run 的 worktree，唯讀、即時）、**變更**（相對基準分支的 diff），以及互動 run 的 **終端機** 或其他 run 的 **紀錄**。

只有 runner 的擁有者看得到檔案、能使用動作。管理員也不例外（磁碟是擁有者的），agent 一律不行，而且 runner 會再確認請求來自它的擁有者。檔案的規則與專案總管相同：隱藏祕密與 `.git`，不會穿過任何通往 worktree 外面的連結（symlink 或 Windows junction）讀取，也不會執行 repo 的 git filter。`nodevas runner set repoBrowse off` 也會關閉檔案分頁。

- **合併**：有開啟中 PR 的 run，照舊在 PR 合併。沒有 PR 的 run（autoPR 關閉、推送失敗、離線）可以由你的 runner **在本機合併到基準分支**，不會推送。runner 只合併這次 run 自己的分支（`agent/<節點>-<run>`），只在你看過的那個 commit，而且絕不碰有未提交變更的工作目錄：
  - 基準分支簽出在登記的 repo 裡，追蹤中的檔案沒有變更、也沒有進行到一半的 merge、rebase、cherry-pick、revert 或 bisect：在那裡以 `--no-ff` 合併（未追蹤與被忽略的檔案沒關係，合併不會覆寫它們，所以你自己的 `.env` 或本機設定不會被換掉），而且只在合併開始時那個工作目錄仍在基準分支上才合併；
  - 基準分支沒有在任何地方簽出：在 runner 資料夾下的暫時 worktree 合併，之後移除；repo 設定（在那裡讀到的）指定的所有 filter 都會清空，也不會進入 submodule；若合併要用 repo 自己設定的程式簽署（或驗證簽章），就拒絕；
  - 基準分支簽出在其他 worktree、run 的 worktree 有未提交的檔案、repo 設定了 merge driver，或還有其他未結束的 run（或這次 run 的流程）在用這個分支：拒絕並說明原因；
  - 有衝突：中止合併，一切維持原狀。

  合併後會移除這次 run 的 worktree 與分支。其他種類的 run（審查、修訂）不會從這裡合併。
- **捨棄**（run 結束後）：先關閉開啟中的 PR 並刪除 GitHub 上的 PR 分支（`gh pr close --delete-branch`），再移除 run 的 worktree（包含未提交的檔案）、刪除本機分支，並從保留的 worktree 清單中移除。`gh` 關不掉 PR 時，什麼都不會移除。修訂或審查 run 的 PR 屬於實作 run，所以捨棄它們不會動到那個 PR 與它的分支。交接分支會保留。run 之後才加到分支（或 run 的 worktree）上、其他分支都沒有的提交，會讓捨棄在移除任何東西之前就停下。還有其他 run（同一個 PR 的修訂或審查）或這次 run 的流程在用這個分支時，捨棄要等它們結束。重複捨棄，或 worktree 已經不在的 run，都沒問題。從沒有 worktree 的 run（被拒絕、開始前就取消）只在 hub 上捨棄。確認視窗只列出 runner 真的會移除的東西，數不出未提交檔案時也會直說。

兩個動作都會先請你確認。run 上會顯示已合併或已捨棄，但它的狀態不變，所以歷史、評分卡與自動推進都不受影響。runner 離線時，請求最多等 15 分鐘；同一個請求送兩次，runner 只會處理一次。每個請求、回覆與逾時都記在稽核紀錄裡（`run.workspace.*`）。

## 全域 agent

工作站的 **全域 agent** 分頁可以啟動不屬於任何專案、也沒有看板的 agent：在你 runner 的某個 repo 裡做一件事，適合不是節點的工作（整理 repo、試個想法、修個腳本）。寫下任務即可，第一行會成為它的標題。

- **誰、在哪裡**：只有你本人，只能用你自己的 runner（管理員也不例外），而且只能在那台 runner 已加入的 repo 裡；runner 還沒有 repo 時，表單會列出要在那台電腦上執行的 `nodevas runner repo add <路徑>`。hub 從不指定資料夾，agent 也不能啟動全域 agent。分頁列出你啟動的、或在你 runner 上執行的全域 agent，以及你的 runner。
- **沒有看板**：runner 不會給全域 agent `nodevas` MCP server，所以它不認領任務、不設定狀態，也無法問你任何事，權限提示也一律不轉送。在背景執行時它自己做決定，並在摘要裡說明它的假設；想和它對話請選 **互動式**，在它的終端機裡談。你 runner 本身的限制（權限模式、沙箱、自動接受規則）照常適用。Claude Code 與 Gemini CLI 完全不載入 MCP server；OpenCode 會關掉所有 server，包括你自己的 `nodevas` 設定；Codex 與 agy 仍會載入你自己設定檔（`config.toml`、`~/.gemini/config`）列出的 server，但其中的 `nodevas mcp` 在全域 agent 的 run 裡會拒絕服務：runner 在 agent 的環境變數放 `NODEVAS_NO_BOARD`，並在 agent 執行期間於它工作樹的 git 資料夾放一個 `nodevas-no-board` 檔（給 Codex 用，它在工作樹裡以清空的環境變數啟動 MCP server）。它在讀取參數、連上 hub 之前就停下，所以不論是 run 經由 runner 的通道，還是 loopback hub 對本機的信任，都不會讓它碰到看板。不涵蓋：你 `PATH` 上比 runner 舊的 `nodevas`，以及你在會清空環境變數的 CLI 裡、設定成在工作樹以外啟動的 MCP 項目。沒有指定專案或節點、又不是全域 agent 的 run，runner 一律拒絕。
- **分支與 PR**：分支是 `agent/<slug>-<run>`，slug 取自標題（小寫英文字母、數字與單一連字號，最多 40 個字元；標題裡沒有這些字元時用 `task`），也可以自己取。預設不開 PR：commit 留在那個分支、runner 上它的 worktree 裡，可以從它的[工作區](#agent-工作區)**在本機合併**或**捨棄**。勾選 **完成後開 PR**，runner 就會像節點的 run 一樣推送並開 PR，前提是它的 `autoPR` 是開著的。commit 與 PR 會註明 run（`Nodevas-Run`），但沒有節點。
- **全域 agent 不支援**：審查管線、拆解、角色與階段、路由規則、fallback 鏈、等待用量重置與交接、自動推進。全域 agent 因用量上限停下就算失敗，已做的部分會 commit 在它的分支上。
- **runner 有多忙**：分頁會逐台列出 runner 正在執行與排隊中的 run（包含所有專案與全域的），以及今天的 run 數、token 與花費。**同時執行** 可以從 hub 設定 runner 的 `maxConcurrent`，上限是它的 `maxConcurrentCeiling`（預設 8；`maxConcurrent` 更大時取它）。runner 會立即套用，並寫進它的 `runner.json`，只改這一個設定，而且是重新讀檔後再寫，所以 runner 啟動後用 `nodevas runner set` 改過的設定都會保留；調低不會停掉任何 run，只是先不接新的。

在 runner 上：`nodevas runner set globalRuns off` 拒絕全域 agent，`nodevas runner set remoteSettings off` 讓 `maxConcurrent` 只能在那台電腦上改，`nodevas runner set maxConcurrentCeiling <1-16>` 設定 hub 最多能要求多少。這些都要重啟 runner 才生效。只有 runner 的擁有者能從 hub 變更它的設定，runner 也會再確認一次。

全域 agent 的更新只會送給你和它 runner 的擁有者，不會送到任何專案。啟動全域 agent 會以 `run.global.start` 記進稽核紀錄（不含任務內容），從 **同時執行** 做的變更則記為 `runner.settings.update`。

## Agent 角色與工作流程階段

每個 run 都以一個**角色**執行：規劃者、QA、測試者、執行者、審查者、研究者，或專案自訂的 `custom-role-*`。角色決定 run 看得到哪些 MCP 工具（工具定義越少，每次模型呼叫的 token 越少），以及 hub 允許它做什麼。不論 agent 送什麼，hub 都會在每個請求上強制角色：測試者不能認領，執行者只能寫自己的節點、在還有階段未過時不能標完成，審查者什麼都不能寫，任何 agent 都不能改專案的角色、階段或已鎖定的驗收標準。run 的角色來自 run 紀錄，絕不來自 header。

每個專案在 **設定 → Agent 與 Runner → 進階 → 工作流程階段 · \<專案\>** 選擇階段：輕量（只有執行者，約 1× token）、標準（測試者 → 執行者 → 審查者，約 2–3×）、完整（標準加上目標 QA、CI 關卡與人工審查目標，約 3–4×），或自訂。目標可以覆寫專案的階段。新專案預設為標準；既有專案維持現狀，直到有人選擇預設組合。誰來執行各角色（agent、模型、fallback、預算）、角色能做什麼，以及自訂角色，都在 **設定 → Agent 與 Runner → 進階 → 角色**。

在標準階段下，任務必須先有**驗收標準**（顯示在內容分頁頂端），測試者或執行者才會執行。分段管線是：測試者寫出會失敗的測試並開 draft PR → runner 執行 repo 的測試指令，測試必須**失敗**（紅）→ 執行者讓測試通過 → 測試指令必須**通過**（綠）→ hub 檢查執行者的 diff 沒有動到測試者的檔案或測試基礎設施 → CI（完整）→ 審查 → hub 把節點標為完成，紅、綠與 diff 檢查的證據都記在歷史上。

測試指令只存在 runner 上，hub 只知道「有設定」：

```bash
nodevas runner repo set myapp --test-command "go test ./..." [--test-timeout 20]
nodevas runner autoaccept verify on   # 選用：auto-accept 打開時，在白名單 repo 中自動接受 verify 工作
```

測試指令不經過 shell 執行，需要 `&&` 或管線時請寫成 `sh -c '…'`；`--test-command ""` 會清除它。逾時以分鐘計（預設 20，最多 180）。改完要重啟 runner。

verify 工作不執行 agent 也不呼叫模型，但它會以你的身分、在 agent 沙箱之外執行 agent 寫的程式碼，所以必須逐一 repo 開啟，並有獨立的自動接受開關（預設關閉）。它在 PR head 的拋棄式 worktree 中執行，不跑 git hook，環境變數中不含 runner 與 GitHub 的 token，逾時會停止整個程序樹。目標 QA 評審也會跑測試指令：在它的 base 上、agent 開始前執行，所以只有打開 `autoaccept verify on` 時才會被自動接受；在終端機上，它的派工會多顯示 `base:` 與 `tests:` 兩行。角色是看板上的防護欄，不是作業系統的隔離：在你帳號下擁有 shell 的 agent 仍可能碰到其他 run 的 proxy 位址。紅綠驗證、diff 檢查與 hub 的關卡才是 agent 無法干預的檢查。

## 自動推進的合併

開啟 **合併已核可的 PR** 後，任務的 PR 核可且檢查通過時，自動推進會由你的 runner 以你的 `gh` 登入合併。這需要人工審查（**由誰核可 PR** 選 **由人審查** 或 **先 agent 共識，再由人審查**），除非另外開啟 **不經人工審查就合併**。沒有人工審查時，符合下列任一情況的 PR 會等你處理（「未經你核可不會合併：…」；按 **核可** 合併，或 **要求修改**）：

- 動到敏感路徑（見下方）；原因最多列出 8 個；
- 讀不到它變更的檔案、runner 沒有回報任何檔案，或變更了 2000 個以上的檔案；
- PR 的 head 不是自動推進自己的 run 做出的 commit，或不知道是哪個 run 開了這個 PR。

路徑中任一資料夾或檔案本身的名稱是下列之一時，就是敏感路徑：`.github`、`.gitlab`、`.circleci`、`.buildkite`、`.husky`、`.devcontainer`、`.claude`、`.cursor`、`.vscode`、`scripts`、`.codex`、`.gemini`、`.agents`、`_agents`、`.agent`、`.opencode`。下列檔案不論在哪個資料夾，都依檔名視為敏感：

- CI 與 hook：`.gitlab-ci.yml`、`.travis.yml`、`azure-pipelines.yml`、`Jenkinsfile`、`bitbucket-pipelines.yml`、`.drone.yml`、`appveyor.yml`、`.pre-commit-config.yaml`（或 `.yml`）、`lefthook.yml`；
- git：任何以 `.git` 開頭的名稱（`.gitignore`、`.gitattributes`、`.gitmodules`）；
- 環境變數：`.env`、`.env.*`、`.envrc`；
- 容器：以 `Dockerfile`、`Containerfile` 或 `docker-compose` 開頭的名稱、以 `.dockerfile` 結尾的名稱、`compose.yaml`、`compose.yml`、`.dockerignore`；
- 建置：`Makefile`、`GNUmakefile`、`justfile`、`Taskfile.yml`；
- agent 指示與設定：`CLAUDE.md`、`AGENTS.md`、`GEMINI.md`、`.mcp.json`、`opencode.json`、`opencode.jsonc`、`tui.json`、`tui.jsonc`；
- JavaScript：`package.json`、`package-lock.json`、`npm-shrinkwrap.json`、`yarn.lock`、`pnpm-lock.yaml`、`pnpm-workspace.yaml`、`bun.lock`、`bun.lockb`、`.npmrc`、`.yarnrc`、`.yarnrc.yml`、`.pnpmfile.cjs`；
- Go：`go.mod`、`go.sum`、`go.work`、`go.work.sum`；
- Rust：`Cargo.toml`、`Cargo.lock`、`build.rs`；
- Python：`pyproject.toml`、`poetry.lock`、`uv.lock`、`Pipfile`、`Pipfile.lock`、`setup.py`、`setup.cfg`、`tox.ini`、`noxfile.py`、`conftest.py`、`pytest.ini`、`requirements*.txt`；
- Ruby、PHP、JVM 與 .NET：`Gemfile`、`Gemfile.lock`、`composer.json`、`composer.lock`、`pom.xml`、`build.gradle`、`build.gradle.kts`、`settings.gradle`、`settings.gradle.kts`、`gradle-wrapper.properties`、`NuGet.config`、`Directory.Build.props`、`Directory.Build.targets`、`packages.lock.json`；
- 測試執行器，名稱開頭為：`vitest.config.`、`vitest.workspace.`、`vite.config.`、`jest.config.`、`playwright.config.`、`cypress.config.`、`karma.conf.`、`.mocharc`、`phpunit.xml`。

名稱比對不分大小寫，改名時新舊路徑都會檢查。分段管線的 diff 檢查也用同一份清單：執行者動到這些路徑時，需要審查者核可；階段裡沒有審查者時，則需要人工核可。

## Agent 表現

**設定 → Agent 與 Runner → 進階 → Agent 表現** 把 run 的歷史整理成 **洞察**（每個交付任務的費用、一次通過率、重工、浪費）、依角色、agent 與模型的 **成績單**，以及等級（資深、中階、試用），全部依固定規則計算：不呼叫模型，也不花 token。它的建議要等你確認後，才會改動路由規則、角色的每日預算或自動分派。

在 **等級與分派** 中，**這個專案自動分派**（預設關閉）會在沒有任何路由規則符合時，替派工或自動推進的任務挑選 agent 與模型：達到該角色要求等級中最便宜的組合，並把一部分任務（預設 10%）交給試用組合以累積樣本。預算照常套用。

hub 每 6 小時刪除超過 180 天的原始事件（已結束 run 的紀錄行、agent session 的動作與分派紀錄，以及稽核紀錄中的 `auth.signin.failed`、`auth.stepup.failed`、`auth.totp.failed`、`runner.link.failed`），並清空舊的 diff，但保留檔案清單與行數（diff 畫面會註明是歷史保留期限清除的）；run、用量、判定、成績單與其餘稽核紀錄都會保留。管理員可在 **原始事件保留** 修改（30 到 3650 天，或永久）。

## 示範影片

由 `web/` 裡的 `npm run demo:videos` 從實際的 app 錄製；影片中的 runner、agent 與 hub 資料是模擬的。

### 把節點派給 agent

選 runner、agent 與模型，在 runner 上接受，看 OpenCode 透過 ACP 工作，讀 diff，合併 PR。

https://github.com/user-attachments/assets/ab98a136-677c-473e-9f94-45a330ec74e6

### 自動推進

在節點面板按目標的 **自動推進** 按鈕，就會在工作站開啟。目標拆成任務後，每個任務依序實作、跑 CI、由其他模型審查與修改，最後等你審查。

需求中途可以改。直接跟 Copilot 說要怎麼改：它會暫停這個目標的自動推進、把變更記在目標的 **Revisions** 底下，再由一次重新規劃重新評估任務：已完成的保留、過時的略過（保留歷史）、進行中的只標記、需要的新任務會加入。規劃期間按鈕顯示 **修訂中…**。結束後 Copilot 會告訴你保留、修改、略過與新增了哪些，並問要不要恢復推進；你說好之前不會恢復。詳見 [COPILOT.md](../COPILOT.md#revising-a-goal-mid-way)。

https://github.com/user-attachments/assets/dbbfbb85-5ddd-484d-a669-4582c849ab5b

### 兩步驟驗證與裝置連結

在 **設定 → 帳號** 開啟驗證器 App、保存復原碼，再從 **設定 → Agent 與 Runner** 連結一台新電腦：step-up 改問 App，hub 產生一次性連結。

https://github.com/user-attachments/assets/df225a24-840e-4ade-b297-7a6d9dd6660e

### Token 用量

在 **設定 → Agent 與 Runner → 進階 → 用量明細** 依日期、agent、模型與 session 看 token 用量與 API 等價成本；每日預算另有一列。

https://github.com/user-attachments/assets/19fba922-8c4f-4ad9-8c8f-7cfac0ebe8c3

### Agent 對話

誰在做什麼、一則轉送過來等你允許的權限詢問、agent 之間的訊息，以及一則等你放行的暫留訊息。

https://github.com/user-attachments/assets/d9943e60-9a91-4f0b-97fe-dd9c31754ba9

