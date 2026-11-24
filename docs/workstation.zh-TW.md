# Agent 工作站

English: [workstation.md](./workstation.md)

server（**hub**）負責顯示看板、run 與改動，但從不啟動任何程式。你電腦上的 **runner**（`nodevas runner`）主動連到 hub，接下你同意的 run，用你自己的登入執行 agent CLI。程式碼、agent 登入與 `gh` 登入都只留在 runner 上，而且它只執行你同意的工作，並守住自己的限制（見[安全模型](../README.zh-TW.md#安全模型)）。

- **Run**：分成互動與 headless（背景）兩種。互動模式在 runner 上一個看不見的終端機裡執行 agent 的 TUI，再轉到你的瀏覽器（不會開任何視窗）；headless 把事件串流到節點上。只要支援，在你自己的 runner 上預設就是互動；見[互動 run](#互動-run)。runner 會 commit、push，再用 `gh` 開 PR，並回報 diff 與 CI 狀態。
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
  - 審查、拆解、比稿的比較（`compare`）與評審（目標 QA 與檢查清單檢查）一律唯讀，而且不載入 repo 自帶的 agent 設定；Claude Code 在這些 run 裡也不能用 WebFetch、WebSearch 或 Monitor。
  - run 新增或修改的 agent 設定（`.claude/settings.json`、`.mcp.json`、`.codex/`、`.gemini/`、`opencode.json` 等）不會進它的 commit，worktree 會保留下來，讓你自己決定要不要 commit。`nodevas runner status` 會列出保留的 worktree；`nodevas runner prune-kept [--dry-run]` 移除只剩被擋下設定的那些（分支保留）；不會自動移除。讀寫這份清單時會加鎖（runner 資料夾裡的 `kept-worktrees.lock`），所以 `prune-kept` 或共用同一個資料夾的另一個 runner，不會弄丟 run 剛保留的 worktree。
  - headless run 的權限提示一律拒絕，除非你[轉給 Nodevas](#權限提示預設關閉)；自動接受的 run 只有在打開 `allow-shell` 時才會轉送。
  - 專案總管的 **檔案** 分頁只讓你本人唯讀瀏覽你 runner 上的 repo，並隱藏看起來像祕密的檔案（`.env`、金鑰、`.ssh`、憑證）。執行 `nodevas runner set repoBrowse off` 並重啟 runner 即可關閉。
  - run 的 **工作區**（它的 worktree、分支與變更）只有你本人能瀏覽、在本機合併或捨棄，管理員與 agent 都不行；見 [Agent 工作區](#agent-工作區)。執行 `nodevas runner set workspaceActions false` 並重啟 runner，即可關閉合併與捨棄。
  - **全域 agent** 只能在你自己的 runner、已登記的 repo 裡啟動，沒有看板；除非你要求開 PR，分支都留在 runner 上。它可以接著做後續回合，也可以一次跑多個 repo。見[全域 agent](#全域-agent)。執行 `nodevas runner set globalRuns off` 並重啟 runner，即可拒絕全域 agent。
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

## 互動 run

只用一個資料夾的 run，符合下列條件時，在你自己的 runner 上預設以**互動**模式開始：runner 與選定的 agent 都支援、agent 已信任 runner 的工作資料夾、自動接受（若有打開）涵蓋互動 run 並允許遠端輸入，而且節點的 run 必須是單純的實作 run，沒有工作流程階段、審查管線或 fallback 鏈。否則就在背景開始，並用一行說明原因。多 repo 群組、比稿、自動推進、管線的回合、fallback，以及在隊友 runner 上的 run，一律在背景執行。後續回合也用同一套規則，依前一個 run 的 runner 與 agent 判斷；要等待的回覆（排在執行中的回合後面，或等用量上限重置）一律在背景執行（見[後續回合與 PR 更新](#後續回合與-pr-更新)）。

開始表單會記住你上次選的是 **互動** 還是 **背景**。記住的互動選擇，永遠蓋不過一定要在背景執行的原因：runner 或 agent 無法在終端機裡執行、資料夾還沒被它的 CLI 信任、隊友的 runner、不允許輸入的自動接受、分階段的節點、管線或 fallback、群組或比稿。這時 **互動** 卡片會停用，run 在背景開始。自動接受只涵蓋背景 run 的情況比較寬鬆：預設改成 **背景**，但你仍可以選 **互動**，自己回答提示。

agent 的 TUI 在 runner 上一個看不見的 PTY 裡執行（絕不會開出桌面上的終端機視窗），畫面串流到 run 的對話框，你可以直接在裡面輸入，或使用輸入框與按鍵列。在「簡單」模式裡，agent 的畫面外面會包一層用白話說明的框；工作資料夾還沒被信任時，會自動改成背景 run；沒有登入的 agent 在兩種模式下都無法開始。

- **唯讀觀看**：在團隊 hub 上，若 runner 的擁有者沒有允許互動 run 遠端輸入，runner 會把它標成唯讀觀看。瀏覽器把終端機顯示為唯讀，並停用輸入框，而不是默默吞掉你的輸入。
- **結束（完成，保留成果）**：和 **停止**（取消，未完成的工作會丟掉）不同，**完成，保留成果** 會把互動 run 乾淨地結束，並把它的成果當成成功發布：commit 變更、保留分支，有要求時開 PR。runner 會送出 CLI 的離開按鍵序列，最多等 10 秒讓它自己結束，之後才終止整個程序樹，所以被終止的結束碼不會被當成失敗。終端機開啟前就送出的結束會先保留，一開啟就套用。只有能在這個 run 裡輸入的人（派工的人或 runner 的擁有者；agent 一律不行，管理員也沒有額外權限）可以結束它；唯讀觀看的 run **只有 runner 的擁有者** 能結束。記為 `run.end`。
- **20 分鐘沒動靜就結束**：每個互動 run 在 runner 上都有固定 20 分鐘的閒置上限（不能調整，「簡單」模式也一樣）。若 20 分鐘內畫面沒有有意義的輸出（TUI 閒置時的重繪不算），也沒有人輸入，runner 就用和結束相同的方式收掉這個 session，保留成果。若逾時當下畫面正顯示提問或選單，對話框會說它是在等你回答時停下的，而不是一般的閒置結束。整個 run 60 分鐘的逾時（`runTimeoutMinutes`）仍然適用，逾時就算失敗。
- **run 結束之後**：即時終端機的內容還能再看 10 分鐘。Claude Code 的互動 run，runner 還會追讀它 session 的對話紀錄檔（`~/.claude/projects/*/<session-id>.jsonl`，唯讀，只讀 runner 使用者擁有、不是 symlink 的一般檔案），轉成遮蔽過祕密的結構化事件，所以畫面過期後仍看得到 **步驟** 與最後的摘要。

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

工作站的 **全域 agent** 分頁可以啟動不屬於任何專案、也沒有看板的 agent：在你 runner 的一個或多個 repo 裡做一件事，適合不是節點的工作（整理 repo、跨服務升級相依套件、調整 CI、修補 CVE）。寫下任務即可，第一行會成為它的標題。

- **誰、在哪裡**：只有你本人，只能用你自己的 runner（管理員也不例外），而且只能在那台 runner 已加入的 repo 裡；runner 還沒有 repo 時，表單會列出要在那台電腦上執行的 `nodevas runner repo add <路徑>`。hub 從不指定資料夾，agent 也不能啟動全域 agent。分頁列出你啟動的、或在你 runner 上執行的全域 agent、群組與比稿，以及你的 runner。
- **沒有看板**：runner 不會給全域 agent `nodevas` MCP server，所以它不認領任務、不設定狀態，也無法透過 `ask_user` 問你任何事，權限提示也一律不轉送。只用一個 repo 的全域 agent 預設以**互動**模式執行，你可以在它的終端機裡和它對話、回答提示（見[互動 run](#互動-run)）。在背景執行時它自己做決定；需要你決定的事，它可以在訊息最後提問，你的回覆就會接著這份工作做下去。你 runner 本身的限制（權限模式、沙箱、自動接受規則）照常適用。Claude Code 與 Gemini CLI 完全不載入 MCP server；OpenCode 會關掉所有 server，包括你自己的 `nodevas` 設定；Codex 與 agy 仍會載入你自己設定檔（`config.toml`、`~/.gemini/config`）列出的 server，但其中的 `nodevas mcp` 在全域 agent 的 run 裡會拒絕服務：runner 在 agent 的環境變數放 `NODEVAS_NO_BOARD`，並在 agent 執行期間於它工作樹的 git 資料夾放一個 `nodevas-no-board` 檔（給 Codex 用，它在工作樹裡以清空的環境變數啟動 MCP server）。它在讀取參數、連上 hub 之前就停下，所以不論是 run 經由 runner 的通道，還是 loopback hub 對本機的信任，都不會讓它碰到看板。不涵蓋：你 `PATH` 上比 runner 舊的 `nodevas`，以及你在會清空環境變數的 CLI 裡、設定成在工作樹以外啟動的 MCP 項目。沒有指定專案或節點、又不是全域 agent 的 run，runner 一律拒絕。
- **分支與 PR**：分支是 `agent/<slug>-<run>`，slug 取自標題（小寫英文字母、數字與單一連字號，最多 40 個字元；標題裡沒有這些字元時用 `task`），也可以自己取。預設不開 PR：commit 留在那個分支、runner 上它的 worktree 裡，可以從它的[工作區](#agent-工作區)**在本機合併**或**捨棄**。勾選 **完成後開 PR**，runner 就會像節點的 run 一樣推送並開 PR，前提是它的 `autoPR` 是開著的。commit 與 PR 會註明 run（`Nodevas-Run`），但沒有節點。
- **全域 agent 不支援**：審查管線、拆解、角色與階段、路由規則、fallback 鏈、用量上限後的自動交接、自動推進。全域 agent 因用量上限停下就算失敗，已做的部分會 commit 在它的分支上；你可以用後續回合接著做（若記錄的重置時間還沒到，會等到那時）。
- **runner 有多忙**：分頁會逐台列出 runner 正在執行與排隊中的 run（包含所有專案與全域的），以及今天的 run 數、token 與花費。**同時執行** 可以從 hub 設定 runner 的 `maxConcurrent`，上限是它的 `maxConcurrentCeiling`（預設 8；`maxConcurrent` 更大時取它）。runner 會立即套用，並寫進它的 `runner.json`，只改這一個設定，而且是重新讀檔後再寫，所以 runner 啟動後用 `nodevas runner set` 改過的設定都會保留；調低不會停掉任何 run，只是先不接新的。

### 後續回合與 PR 更新

全域 run 結束後（成功、失敗、取消或達到用量上限），在它的對話框回覆，就會開始一個接著同一份工作的後續回合：同一台 runner、同一個 worktree 資料夾、同一個分支。

- **互動或背景**：後續回合用和第一次開始相同的模式規則（見[互動 run](#互動-run)），依前一個 run 的 runner 與 agent 判斷，並套用你記住的選擇。**用這句開始新工作** 也一樣。
- **接續 session**：可以的話，runner 會從它本機的紀錄接續 CLI 自己的 session：Claude Code、OpenCode 與 Antigravity 兩種模式都可以，Codex 只在背景執行時可以（`codex exec resume`）。Gemini CLI 永遠不接續。其他情況（Gemini CLI、在終端機裡的 Codex、換了 agent、沒有 session 紀錄，或 headless 的接續在 30 秒內以非零結束碼結束、又沒有任何文字輸出），runner 會開新的 session。hub 每次送出後續回合時，都會附上前一次的任務與那個回合的摘要（遮蔽過祕密，最多 4 KiB）；新的 session 會在 prompt 裡收到它們，標明是資料而非指令，所以不能接續的 agent 也知道之前做了什麼。
- **排隊的回覆**：**背景**全域 run 還在執行時輸入，會建立一個排隊中的後續回合，等目前的回合結束才開始；開始前可以按 **收回** 撤回。（互動 run 執行中時，請直接在它的畫面裡輸入。）若前一個回合停在用量上限、而重置時間還沒到，後續回合會等到那時。要等待的回覆一律在背景執行。
- **回合的擁有與生命週期**：只有你（啟動者，也是 runner 的擁有者）可以回覆。只有最新的回合能接著做、在本機合併或捨棄；較早的回合維持唯讀，並連到最新的回合。開始前被收回或被拒絕的後續回合會讓出位置。工作在本機合併、被捨棄，或它的 PR 已合併或關閉後，對話框會提供 **用這句開始新工作**，從 base 重新開始。
- **更新開啟中的 PR**：工作已有開啟中的 PR 時，後續回合會更新它，而不是再開第二個 PR：
  - 回合開始前，runner 會重新檢查 PR（`gh pr view`）與遠端分支。PR 已合併或關閉（`pr_ended`）、PR 的 base 改了（`pr_moved`），或遠端分支分岔了（`branch_moved`；只有本機 HEAD 沒有未推送的 commit、worktree 也乾淨時才會快轉），就拒絕；
  - 失敗、取消或達到用量上限的回合只在本機 commit，絕不推送；下一個成功的回合會把這些 commit 一起推送；
  - 每個有新 commit 的成功回合只做快轉推送（絕不用 `--force` 或 `--force-with-lease`），掛到既有的 PR 上，並留一則 PR 留言（`Turn N: …` 加上這個回合遮蔽過祕密的摘要）。draft PR 絕不會自動改成 ready。每次推送都會重跑 CI，也可能讓已有的核可失效；PR 的標題與內文維持第一個回合的版本；
  - 回合執行期間若有人推送到這個分支，推送會被拒絕（`branch_moved`），這個回合的 commit 留在本機，也不會留 PR 留言。沒有變更的回合保留原本的分支，什麼都不推送；
  - 推送前一刻，runner 會再看一次 PR。若它在回合執行期間被合併或關閉，runner 不推送、也不留言：commit 留在本機分支上，run 上會顯示 PR 已結束（`pr_ended`）。runner 看不到的 PR 也不會推送；
  - 只要還有回合沒結束，遠端合併就會被擋下，PR 監看也會保留 worktree，直到回合結束。
- **runner 端的紀錄**：hub 只指名 run ID。worktree 路徑、分支、HEAD、已推送的 HEAD、PR 網址與 CLI 的 session ID，runner 一律只從它自己的 `work.json`（權限 `0600`，保留 30 天，最多 500 筆）取得，並在磁碟上確認 worktree 屬於它、git 狀態正確。有未提交變更的 worktree，只在前一個回合被取消或失敗時才接受；若沒有變更的第一個回合已經移除了資料夾，後續回合會在原本的分支上從 base 重建它。

### 多 repo 群組

在 **全域 agent** 裡選同一台 runner 上的 **2 到 30 個資料夾**，就能在多個彼此獨立的 repo 同時做同一件事（升級相依套件、修改 CI 工作流程、修補 CVE）。

- **分頭進行**：hub 為每個 repo 各建立一個背景全域 run，放在同一個群組列底下（在「故事」畫面裡顯示成一個人物，每個 repo 一個分頁）。每個成員的 prompt 都會列出一起修改的其他 repo。群組不用互動模式；某個 repo 的 run 完成後，可以用後續回合單獨和它對話。
- **預算與排隊**：每日的 run 上限以整個群組一起計算：剩下的額度不夠所有選取的 repo 時，整個群組都會被拒絕，並告訴你能放進幾個（不會只開始一部分）。成員在 runner 的 `maxConcurrent` 之下依先來先做的順序執行；派出每個排隊中的成員前，hub 會再檢查每日的花費與 token 上限，超過就取消剩下的成員。在團隊 hub 上，每個成員都照一般的接受規則逐一派給你的 runner（hub 沒有「全部接受」）。
- **用量上限時暫停**：有成員停在額度或 rate limit 時，群組會暫停到重置時間（**已暫停到 HH:MM：用量上限**），排隊中的其他成員跟著等；按 **立即繼續** 可以提早解除，停下的成員則用後續回合接著做。
- **群組動作**：每個 repo 都有自己的分支、（開始時選的）PR、diff 與結果動作；不需要修改的 repo 會移除它的 worktree，顯示 **這裡沒有需要改的**。群組的控制有 **全部停止**、**套用全部已完成的**（逐一在本機合併已完成的成員，某個 repo 有衝突就停在那個 repo，並回報每個結果）、**告訴全部** 與 **告訴失敗的**（對符合條件的最新回合送出後續回合）。

在 runner 上：`nodevas runner set globalRuns off` 拒絕全域 agent，`nodevas runner set remoteSettings off` 讓 `maxConcurrent` 只能在那台電腦上改，`nodevas runner set maxConcurrentCeiling <1-16>` 設定 hub 最多能要求多少。這些都要重啟 runner 才生效。只有 runner 的擁有者能從 hub 變更它的設定，runner 也會再確認一次。

全域 agent 的更新只會送給你和它 runner 的擁有者，不會送到任何專案。稽核紀錄（一律不記任務或後續回合的內容）：`run.global.start`、`run.global.followup`、`run.global.group`，以及 **同時執行** 的 `runner.settings.update`。

## 協調同時進行的 run

工作互相重疊時，Nodevas 分三層協調，決定權仍在你：要重複做之前先問你、兩個 run 動到相同檔案時及早提醒，以及刻意讓幾個 agent 並排比稿。

### 忙碌時的選擇

還沒結束的寫入型 run（節點上的實作、測試、修訂或拆解），或還在兩個步驟之間的管線，會占住它要寫的地方：

- 開始**節點 run** 時，會檢查同一個節點上還沒結束的寫入型 run（任何成員的都算），以及你自己在同一個 repo 裡還沒結束的全域 run；
- 開始**全域 run** 時，會檢查你自己在同一個 repo 裡還沒結束的寫入型 run（全域或節點的都算）。

同一個 repo 裡*不同*節點的兩個節點 run 不會互相擋住；唯讀的 run（審查、評審、驗證、比較）與同一脈絡的 run（管線、fallback 鏈、後續回合、群組或比稿）也不會。

目標忙碌時，開始表單會先提醒你，hub 也會在建立 run 之前請你選擇：

- **等它做完再開始我的**（排隊）：建立一個新的 run，以嚴格的先來先做順序，排在每個進行中或更早排隊的占用者與它的後續（fallback、等待用量重置、管線剩下的步驟）後面。開始時，會把你自己前一個 run 的摘要放進它的 prompt（絕不放別的成員的）。
- **改成接著它的成果做**（接續）：只在你要開始的全域 run 排在你自己同一台 runner、同一個 repo 的全域 run 後面時提供；把你的訊息變成那個 run 的後續回合，沿用它的資料夾、分支、agent、模型與 PR 設定。
- **停下它，改做我的**（接手）：停掉你看到的那些占用者（若期間占用者有變，會再問你一次），並把你的 run 排在它們正後方。停下執行中的互動 run 用的是**結束**，所以它已做的成果會保留（接手節點時，結束會保留分支但不開 PR，免得出現兩個互相競爭的 PR）；停下 headless run 則會取消它，以及它所屬的管線。
- **兩個都做**：馬上和占用者同時開始，並由[重疊提醒](#重疊提醒)留意共用的檔案。runner 的 `maxConcurrent` 是 1 時，兩個 run 仍會一個接一個執行。
- **多 repo 群組**只提供 **兩個都做** 與 **略過忙碌中的資料夾**（在檢查預算之前先去掉忙碌的 repo）。
- **自動開始**（自動推進）不問，直接排隊。在舊到不支援協調的 runner 上，人開始忙碌中的節點時只會看到 **停下它，改做我的**，但自動推進仍會排在占用者後面等，不會失敗。Copilot 的工具會回答目標忙碌中，並列出正在進行的工作，而不會替你選；見 COPILOT.md 的 [When the target is busy](../COPILOT.md#when-the-target-is-busy)。

**團隊隱私與看板認領**：在團隊 hub 上，別的成員的 run 只會顯示成一個數字（「另有 1 個別人的工作」），沒有名字、任務或細節，而且只有它的擁有者或管理員能停下它（所以有看不到的占用者時，不提供 **停下它，改做我的**）。兩個 run 共用一個節點時（排隊、接手、兩個都做或比稿的參賽者），hub 會釋放被停下或被等待的占用者的看板認領（只限它自己 session 的）。**兩個都做** 的 run 與每個比稿參賽者都以協調模式執行；排隊或接手的 run 若在派給 runner 時發現節點已被認領或已完成，也一樣：它的 MCP server 不提供 `claim_task`、`release_task`、`set_node_status` 與 `create_subgraph`（見 MCP 文件的 [Coordinated runs](./mcp.md#coordinated-runs)），agent 就不會搶看板；節點的狀態由你自己更新。記為 `run.busy` 與 `run.takeover`（不含任務內容）。

### 重疊提醒

run 進行期間，runner 會留意它動到哪些檔案（以 repo 為根的相對路徑）：每 30 秒跑一次 `git status`，repo 很慢時逐步放寬到最多 5 分鐘一次，再加上 headless run 的編輯工具路徑與結束時 commit 的檔案；每個 run 最多 500 個路徑。它讀 worktree 時會關掉 repo 的 filter driver，和其他讀取 run 的 worktree 的地方一樣，所以 agent 埋下的 filter 永遠不會執行。祕密路徑、常見的 lockfile（`package-lock.json`、`go.sum`、`Cargo.lock`、`yarn.lock`、`pnpm-lock.yaml` 等）與 `linguist-generated` 的檔案，在 runner 上就會略過。

hub 會比對同一個 repo 裡各個 run 動到的檔案（包括你自己過去 24 小時內已完成、還沒套用、也沒有 PR 的結果）：

- **強重疊與弱重疊**：兩邊都只新增行的檔案記為*弱*重疊（列在清單裡，但不會發提醒或信件）。只要有一邊在共用的檔案裡修改或刪除行（*強*重疊），就會出現重疊提醒與標記。
- **你自己的節點 run**：hub 會發一次協調訊息（寄件者 `nodevas`，每個 run 最多 5 則）到每個 run 自己的 MCP session，列出共用的路徑，讓 agent 可以用 `send_message` 協調，或盡量少改。
- **你自己的全域 run**：全域 run 沒有看板也沒有信件，也絕不會在執行中收到沒人要求的輸入。重疊提醒改為提供 **告訴它**，把訊息先填進對話框的輸入框（由你送進互動的終端機，或排成背景的後續回合），之後每個後續回合的 prompt 都會附上目前的重疊說明。
- **團隊 hub 的隱私**：不同成員之間，只比對同一個專案裡進行中的節點 run（兩個成員的全域 run 從不比對）。runner 回報的路徑在成員之間不可信，所以跨成員的重疊只會顯示「另一位成員的工作也在改一些相同的檔案」，沒有路徑、數量、名字，也不發 agent 信件，管理員看到的也一樣。

動到的檔案與重疊的紀錄，會在 run 結束 7 天後刪除，即使原始事件保留設成永久也一樣。

### 比稿

在單純的節點派工（沒有工作流程階段、管線或 fallback 鏈）或只用一個 repo 的全域 run 打開 **比稿**，就會讓 **2 或 3** 份 headless 參賽作品（**A**、**B**、**C**）在你自己 runner 上各自的 worktree 裡做同一個任務。比稿要從「完整」模式開始；「簡單」模式只在已有比稿時顯示比較的結果。

- **參賽規則與預算**：每份參賽作品都以 headless 執行，不開 PR、沒有 fallback 鏈，也不等用量重置；節點比稿的參賽作品以協調模式執行，所以不會認領節點或更新它的狀態。預算把所有參賽作品加上選用的審查者當成一整塊檢查：額度不夠就什麼都不開始。在團隊 hub 上，每份參賽作品都照一般的接受規則逐一派給你的 runner。
- **選用的審查者**：可以在開始時，或參賽作品完成後，挑一個審查用的 agent 與模型。所有參賽作品都結束、而且至少兩份成功並有 commit 時，就會開始一個唯讀的 `compare` run（和審查一樣，不載入 repo 自帶的 agent 設定）。hub 會拿掉祕密檔案的 hunk、遮蔽每份 diff 裡的祕密、輪換參賽作品的順序，並以 A/B/C 匿名標示，不寫 agent 或模型名稱。審查者回傳 0 到 10 的分數、簡短理由與建議的選擇；分數只是參考，不會自動套用任何東西。
- **選出一份**：全域比稿在 **全域 agent** 裡它的那一列就能打開比較。節點比稿則在節點的 run 畫面裡，從比稿的 run 按 **比較並選出一份** 打開；「故事」畫面裡比稿的那組人物也有這個按鈕。並排比較各份作品（摘要、檔案、diff 與分數），再按 **用這份**，選擇怎麼套用：
  - **套用到我的資料夾**：經過 runner 工作區的合併檢查，在本機合併勝出作品的分支；
  - **開 pull request**：不強制推送勝出作品自己的分支，並開 PR（只有比稿勝出的作品可以）；
  - **先保留**：勝出作品的工作區原封不動，之後再處理。

  **丟掉其他份**（預設勾選）會對沒選上的作品執行一般的工作區捨棄；多了額外 commit 的作品會保留，被拒絕的也會回報。節點比稿選定後，請自己更新節點的狀態。只有比稿的建立者（也是每份作品的 runner 擁有者）能請審查者評分或選出勝者；建立者或管理員可以取消。記為 `run.contest.start`、`run.contest.judge`、`run.contest.pick` 與 `run.contest.cancel`（套用與捨棄另記 `run.workspace.*`）。

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

hub 每 6 小時刪除超過 180 天的原始事件（已結束 run 的紀錄行、agent session 的動作與分派紀錄，以及稽核紀錄中的 `auth.signin.failed`、`auth.stepup.failed`、`auth.totp.failed`、`runner.link.failed`），並清空舊的 diff，但保留檔案清單與行數（diff 畫面會註明是歷史保留期限清除的）；run、群組、比稿、用量、判定、成績單與其餘稽核紀錄都會保留。管理員可在 **原始事件保留** 修改（30 到 3650 天，或永久）。動到的檔案與重疊的紀錄另有固定的保留期限：run 結束後 7 天。

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

