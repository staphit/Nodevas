# Nodevas

English documentation: [README.md](./README.md)

> 將文件、相依關係、工作流程、排程與 AI agent 連在一起的視覺化工作區。

Nodevas 將分散的筆記與任務整理成一個可以看見、編輯並持續推進的工作區。每個節點都將 Markdown 內容與工作脈絡——metadata、負責人、優先度、標籤、連結、狀態與歷史——放在一起。關係在畫布上呈現，進度則可以透過時間軸規劃與回顧。

它也是個人的 agent 工作站：看板是指揮中心，每個節點都是一個任務，可以交給你自己電腦上的 coding agent 去做。run 的進度即時可見，改動完成後會以 pull request 的形式回到節點上。

[快速開始](./docs/getting-started.md) · [核心概念](./docs/concepts.md) · [MCP 文件](./docs/mcp.md) · [安裝 runner](./docs/install.md) · [示範影片](#示範影片)

## 功能

| 功能 | 使用者得到什麼 |
| --- | --- |
| [圖形化工作區](./docs/concepts.md#nodes-and-relationships) | 用圖形、看板或時間軸檢視文件、任務與關係。 |
| [依賴條件邏輯](./docs/concepts.md#dependency-gates) | 使用 `MUST`、`AND`、`OR`、`XOR`、`NAND`、`NOR` 找出目前可以開始的工作。 |
| [計畫與進度](./docs/timeline.md) | 比較預期里程碑與實際狀態，不混淆計畫與歷史。 |
| [MCP 整合](./docs/mcp.md) | 讓 AI agent 找出 ready task、認領工作、更新節點、管理依賴並回報狀態。 |
| [Agent run](./docs/install.md) | 把節點派給你電腦上的 Claude Code、Codex、Gemini CLI、OpenCode 或 Antigravity CLI。每次 run 有自己的 git worktree 與 branch，最後開成 pull request。 |
| [Autopilot](./docs/nodevas-workstation-plan.md) | 把目標拆成任務圖，相依的任務完成後，依序實作、審查、修改並合併每個任務。 |
| [語音 copilot](./COPILOT.md) | 用語音或文字讀取、編輯圖，權限模式比照 Claude Code。 |
| [Token 用量](./docs/usage.md) | 依模型查看 run 與 agent session 用了多少，成本以 API 等價估算。 |
| [本機或共享工作區](./docs/collaboration.md) | 可在本機私人使用，也能部署共享 server 進行團隊協作。 |

## Agent 工作站

server（**hub**）負責顯示看板、run 與改動，但從不啟動任何程式。你電腦上的 **runner**（`nodevas runner`）主動連到 hub，接下你同意的 run，用你自己的登入執行 agent CLI。程式碼、agent 登入與 `gh` 登入都只留在 runner 上。

- **Run**：分成 headless 與互動兩種。headless 把事件串流到節點上；互動模式把 agent 的 TUI 放進瀏覽器。runner 會 commit、push，再用 `gh` 開 PR，並回報 diff 與 CI 狀態。
- **模型**：每次 run 可以自己選 agent 與模型，也可以交給路由規則與 fallback 鏈決定。遇到 rate limit、額度用完或 CLI 不存在時，就改用鏈上的下一個模型。OpenCode 也可以改走 [Agent Client Protocol](https://agentclientprotocol.com)（`nodevas runner set opencodeProtocol acp`，實驗性）：每個工具呼叫即時串流，而它要求的任何權限一律拒絕，因為 headless run 沒有人可以核准。
- **審查管線**：實作完成後，可以交給其他 agent 或模型審查、交給人審，或兩者都要。要求修改時會再跑一次 revise run。
- **Agent network**：agent 可以看到還有誰在工作、互相傳訊息，也能透過 hub 問你問題（`ask_user`）。
- **安全預設**：
  - run 需要你在 runner 的終端機上同意。遠端派工需要 step-up passcode，加上你在 runner 上設定的 auto-accept 白名單。
  - 遠端 merge 預設關閉，要自己打開。
  - 審查與拆解一律唯讀，而且不載入 repo 自帶的 agent 設定。
  - hub 放在 Cloudflare Access 後面，runner 用 service token 連線。
  - 可以用驗證器 App 開啟兩步驟驗證（**專案設定 → 帳號安全**）。開啟後登入還要輸入 App 的驗證碼，step-up 也改問 App，不再寄 email。

設定只要兩步：

1. 部署 hub，例如[在 TrueNAS 上透過 Cloudflare Tunnel 部署](./deploy/truenas/README.md)。
2. 在 hub 上打開 **Runner → 連結新裝置**，複製一次性連結。在你的電腦上執行 [`scripts/install.sh --hub <網址> --link`](./docs/install.md#set-up-a-new-pc-with-a-device-link)，帶入允許 run 使用的 repo，出現提示時貼上連結。連結只能用一次、十分鐘內有效；若管理員存過 hub 的 Access service token，連結也會一併帶上，不必再從 Cloudflare 手動複製。

## Beta

以下功能已可使用,但仍在 Beta 階段,後續版本可能調整:

- [Agent 工作站](./docs/install.md):runner、agent run、審查管線與 autopilot。
- [MCP 整合](./docs/mcp.md):AI agent 工具、資源與 prompts。
- [即時共編](./docs/collaboration.md):共享 server、WebSocket 上線狀態與 CRDT 文件 session。
- 備份:排程快照至本機資料夾,並可還原。
- 歷史紀錄:文件草稿、歷史版本與還原。

## 使用情境

- [AI harness engineering](./demo/ai-harness-engineering)：搭配 MCP 讓 agent 安全處理具依賴關係的任務佇列。
- [日常工作排程](./demo/daily-work-schedule)：管理優先度、負責人、預期里程碑與時間軸判定。
- [小說故事撰寫](./demo/novel-writing)：將場景、抉擇、角色、閘門與修稿歷史組成故事圖。

## 實際畫面

功能導覽維持瀏覽器頁面的正常比例，只在青色局部聚焦視窗中放大正在介紹的介面區域。

![Nodevas 局部聚焦功能導覽](./docs/screenshots/nodevas-feature-tour.gif)

容易閱讀的聚焦截圖與完整截圖索引請見 [`docs/screenshots/`](./docs/screenshots/README.md)。

介面預設為 English，也可以切換繁體中文（`zh-TW`）。語言與深色模式截圖請見 [nodevas-language-zh-tw.png](./docs/screenshots/nodevas-language-zh-tw.png)。

## 示範影片

由 `web/` 裡的 `npm run demo:videos` 從實際的 app 錄製；影片中的 runner、agent 與 hub 資料是模擬的。點影片可開啟原尺寸 MP4。

### 把節點派給 agent

選 runner、agent 與模型，在 runner 上接受，看 OpenCode 透過 ACP 工作，讀 diff，合併 PR。

[![把節點派給 OpenCode，看 run 進行、讀 diff 並合併](./docs/videos/agent-run.gif)](./docs/videos/agent-run.mp4)

### Autopilot

目標拆成任務後，每個任務依序實作、跑 CI、由其他模型審查與修改，最後等你審查。

[![Autopilot 讓任務經過實作、審查與人工審查](./docs/videos/autopilot.gif)](./docs/videos/autopilot.mp4)

### 兩步驟驗證與裝置連結

開啟驗證器 App、保存復原碼，再連結一台新電腦：step-up 改問 App，hub 產生一次性連結。

[![設定兩步驟驗證，再建立裝置連結](./docs/videos/two-factor-and-device-link.gif)](./docs/videos/two-factor-and-device-link.mp4)

### Token 用量

依日期、agent、模型與 session 看 token 用量與 API 等價成本。

[![用量對話框：每日圖表、session 與價格](./docs/videos/usage.gif)](./docs/videos/usage.mp4)

### Agent network

誰在做什麼、agent 之間的訊息，以及一則等你放行的暫留訊息。

[![Agent network 面板放行一則暫留訊息](./docs/videos/agent-network.gif)](./docs/videos/agent-network.mp4)

## 其他能力

- 在同一個工作區樹狀結構中管理多個專案與子專案。
- 支援 Markdown、純文字、HTML 與 Word 文件頁面。
- 可匯入 Markdown、JSON Canvas 與 `.veproj`；可匯出 Word、純文字、HTML、Markdown 與 PDF。
- 依狀態、負責人、標籤、優先度與專案篩選，並儲存常用檢視。
- 支援草稿、歷史版本、垃圾桶、衝突偵測與檔案變更通知。
- 專案檔案保持可檢查，也能搭配 Git 與一般文字工具使用。

## 快速開始

必要環境：Go 1.26.10、Node.js 22 與 npm、Git。

```bash
git clone https://github.com/staphit/Nodevas.git
cd Nodevas
npm ci --prefix web
npm run build --prefix web
go run ./cmd/nodevas serve -project ./workspace -port 5666
```

開啟 <http://127.0.0.1:5666>。正式建置、開發模式、設定與安全開放網路存取，請參考[快速開始文件](./docs/getting-started.md)。

## 文件

- [快速開始](./docs/getting-started.md)：建置、啟動、設定與安全開放 server。
- [安裝 runner](./docs/install.md)：一個 `scripts/install.sh` 指令就能設定好 agent 執行的機器。
- [核心概念](./docs/concepts.md)：節點、連線、依賴條件邏輯、ready 狀態與節點 metadata。
- [時間軸](./docs/timeline.md)：預期里程碑、實際事件與日期判定。
- [MCP 整合](./docs/mcp.md)：連接 Claude Code、Codex 或其他 MCP client。
- [Token 用量](./docs/usage.md)：run 與 agent session 的用量，以及成本怎麼估算。
- [語音 copilot](./COPILOT.md)：介面內的 AI copilot、它的工具與權限模式。
- [工作站計畫](./docs/nodevas-workstation-plan.md)：hub 與 runner 的設計、agent adapter、安全與 autopilot。
- [儲存與協作](./docs/collaboration.md)：專案檔案、共享 server、WebSocket 與 CRDT 行為。
- [OCI 部署](./deploy/oci/README.md)：建立與維運共享雲端部署。
- [TrueNAS 部署](./deploy/truenas/README.md)：透過 Cloudflare Tunnel 公開 Nodevas，不開放 origin port。
- [協助開發](./docs/contributing.md)：開發檢查與程式位置。

## 程式碼簽章

Windows 版本規劃使用 [SignPath.io](https://signpath.io) 提供的免費程式碼簽章,憑證由 [SignPath Foundation](https://signpath.org) 提供。詳見[簽章政策](./docs/code-signing-policy.md)。

## 授權

Nodevas 採用 [GNU General Public License v3.0](./LICENSE) 授權。
