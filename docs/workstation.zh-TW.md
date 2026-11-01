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
  - 戰情室：把整個工作區畫成一棟像素風大樓，有這個專案的辦公室、其他專案的辦公室、閒置 agent 共用的休息室，以及每個 runner 一座機櫃的機房。每個 agent 都是按鈕：它的選單可以開啟它的 run、改動、節點或目標，暫停或恢復它的自動推進，或取消這次 run；
  - 過去一小時誰做了什麼；
  - 側欄有今天的花費、runner，以及可以直接發訊息的 Agent 對話。

  agent 提問、權限提示、用量達上限、每日預算用完時，瀏覽器也會通知你。
- **安全預設**：
  - run 需要你在 runner 的終端機上同意。遠端派工需要 step-up passcode，加上你在 runner 上設定的 auto-accept 白名單。
  - 遠端 merge 預設關閉，要自己打開。
  - 審查、拆解與評審（目標 QA 與檢查清單檢查）一律唯讀，而且不載入 repo 自帶的 agent 設定；Claude Code 在這些 run 裡也不能用 WebFetch、WebSearch 或 Monitor。
  - run 新增或修改的 agent 設定（`.claude/settings.json`、`.mcp.json`、`.codex/`、`.gemini/`、`opencode.json` 等）不會進它的 commit，worktree 會保留下來，讓你自己決定要不要 commit。`nodevas runner status` 會列出保留的 worktree；`nodevas runner prune-kept [--dry-run]` 移除只剩被擋下設定的那些（分支保留）；不會自動移除。
  - headless run 的權限提示一律拒絕，除非你[轉給 Nodevas](#權限提示預設關閉)；自動接受的 run 只有在打開 `allow-shell` 時才會轉送。
  - 專案總管的 **檔案** 分頁只讓你本人唯讀瀏覽你 runner 上的 repo，並隱藏看起來像祕密的檔案（`.env`、金鑰、`.ssh`、憑證）。執行 `nodevas runner set repoBrowse off` 並重啟 runner 即可關閉。
  - hub 放在 Cloudflare Access 後面，runner 用 service token 連線。
  - 可以用驗證器 App 開啟兩步驟驗證（**設定 → 帳號**）。開啟後登入還要輸入 App 的驗證碼，step-up 也改問 App，不再寄 email。

app 裡與 agent 有關的部分（runner、run、自動推進、Agent 對話與用量）預設隱藏，直到你打開 **設定 → Agent 與 Runner → 讓 agent 在這裡工作**，或這個工作區已經有 runner、run 或連線中的 agent。

設定只要兩步：

1. 部署 hub：見 [Getting started: Team server](./getting-started.md#team-server)。
2. 在 hub 上打開 **設定 → Agent 與 Runner**，打開 **讓 agent 在這裡工作**，再按 **連結新裝置**，複製一次性連結。在你的電腦上執行 [`scripts/install.sh --hub <網址> --link`](./install.md#set-up-a-new-pc-with-a-device-link)，帶入允許 run 使用的 repo，出現提示時貼上連結。連結只能用一次、十分鐘內有效；若管理員存過 hub 的 Access service token，連結也會一併帶上，不必再從 Cloudflare 手動複製。

只在一台電腦上自己用時，一個指令就能同時啟動兩者：`nodevas serve --project ./workspace [--repo ~/code/myapp]`（只要 hub 時加 `--no-runner`）。runner 仍是獨立的程序、用自己的 token 連線，每個 run 仍要你在執行 `serve` 的終端機接受。見 [Hub and runner on one machine](./install.md#hub-and-runner-on-one-machine)。

## 回覆你的 agent

agent 可以停下來透過 Nodevas 問你（`ask_user`）：請你核准（**核准** 或 **拒絕**，可附備註）、問一個要你用文字回答的問題，或請你從 2 到 8 個選項中選一個。問題與選擇都可以 **不回答**。agent 最多等 10 分鐘。

可以在 Agent 對話（Ctrl+Shift+A，或工作站側欄的卡片）、工作站的 **等你處理**，或戰情室裡的那個 agent 上回覆；瀏覽器也會通知你。run 的時間軸（節點面板 → **活動** → 該 run）會列出它的每個提問與結果；run 結束時仍未回覆的提問會失效。只有發問 agent 的主人或管理員可以回覆。

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
- runner 只會從自己建立、屬於同一個 repository 與節點、且仍停在它記錄的 commit 的交接分支接續。hub 可以指名這樣的分支，但不能指定其他任何 ref。runner 每建立一個交接分支，就會刪除超過 30 天或超出最新 200 個的交接分支，前提是它們仍停在記錄的 commit。

登入無法使用的模型或不存在的 CLI，會在 agent 第一輪之前就失敗；這時沒有成果可保留，等待也沒用，所以這類失敗仍和以前一樣只沿著鏈往下換模型。

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

https://github.com/user-attachments/assets/8e494381-8aeb-4c58-9cf8-251f4c0f3060

### 自動推進

在節點面板按目標的 **自動推進** 按鈕，就會在工作站開啟。目標拆成任務後，每個任務依序實作、跑 CI、由其他模型審查與修改，最後等你審查。

需求中途可以改。直接跟語音 Copilot 說要怎麼改：它會暫停這個目標的自動推進、把變更記在目標的 **Revisions** 底下，再由一次重新規劃重新評估任務：已完成的保留、過時的略過（保留歷史）、進行中的只標記、需要的新任務會加入。規劃期間按鈕顯示 **修訂中…**。結束後 Copilot 會告訴你保留、修改、略過與新增了哪些，並問要不要恢復推進；你說好之前不會恢復。詳見 [COPILOT.md](../COPILOT.md#revising-a-goal-mid-way)。

https://github.com/user-attachments/assets/1108f05c-50f3-414c-8541-ad1fab9fbbe9

### 兩步驟驗證與裝置連結

在 **設定 → 帳號** 開啟驗證器 App、保存復原碼，再從 **設定 → Agent 與 Runner** 連結一台新電腦：step-up 改問 App，hub 產生一次性連結。

https://github.com/user-attachments/assets/f7f93bb4-304d-4c0f-b39a-5af4d7acebe8

### Token 用量

在 **設定 → Agent 與 Runner → 進階 → 用量明細** 依日期、agent、模型與 session 看 token 用量與 API 等價成本；每日預算另有一列。

https://github.com/user-attachments/assets/57324ad9-5170-495a-8024-159ce63102aa

### Agent 對話

誰在做什麼、agent 之間的訊息，以及一則等你放行的暫留訊息。

https://github.com/user-attachments/assets/fa0c5a33-1d02-41bd-9dbc-6ff0aac0e75d

