# Nodevas

[English](./README.md)

> 放筆記、任務與 AI agent 的視覺化工作區。

把工作變成畫布上的節點，連起來，就能看出哪些事現在可以開始。任務準備好了，就交給你自己電腦上的 coding agent，做完以 pull request 回來。

![Nodevas 功能導覽](./docs/screenshots/nodevas-feature-tour.gif)

## 可以做什麼

- **視覺化規劃**：每個節點是一頁 Markdown，帶有狀態、負責人、優先度與標籤。可用圖形、看板或時間軸檢視。
- **管理依賴**：用 `AND`、`OR` 等條件連接節點，Nodevas 會告訴你哪些工作可以開始。
- **把工作交給 agent**：把節點派給 Claude Code、Codex、Gemini CLI、OpenCode 或 Antigravity CLI，即時看它工作、看 diff、合併 PR。
- **接上任何 MCP client**：agent 可以找出可做的任務、認領，並回報進度。

## 資訊、工作站與設定

每個專案有三種檢視，在頂列切換：

- **資訊**：看板本身，包括圖、時間軸、頁面與相依關係，用來規劃。
- **工作站**：你的 agent 一起工作的地方。你會看到誰在做什麼、即時的戰情室或頭目地圖、過去一小時誰做了什麼的時間線、什麼在等你處理（提問、合併、用量上限、每日預算），以及 agent 之間的對話。自動推進從這裡推動一個目標的任務。
- **設定**：其餘設定集中在一頁，包括專案的狀態、里程碑與人員、通知、備份與歷史、你的外觀與語言、語音 Copilot、Agent 工作站（Runner、用量、每日預算、路由規則）；團隊 server 上還有你的帳號與兩步驟驗證，管理員另有伺服器帳號與稽核紀錄。

![工作站：agent 名單、戰情室、誰做了什麼，以及等你處理的事](./docs/screenshots/station-warroom.png)

| 戰情室：每台 runner 是一間辦公室，每個 agent 坐在座位上 | 頭目地圖：專案是頭目，完成節點就扣它的 HP |
| --- | --- |
| ![戰情室](./docs/screenshots/game-warroom.png) | ![頭目地圖](./docs/screenshots/game-boss-map.png) |

每個角色和怪物都能點進真正的節點、run、diff 或 PR。agent 問你問題、用量達上限，或每日預算用完時，瀏覽器會通知你。

**換成你的風格**：[主題包](./docs/view-packs.md)可以用你自己的圖片改造戰情室或頭目地圖。它是一個含 `pack.json` 與圖片的 zip，不含任何程式，由管理員安裝給所有人使用。[`examples/view-packs`](./examples/view-packs) 裡有兩個範例：

| 霓虹太空站（戰情室） | 糖果地城（頭目地圖） |
| --- | --- |
| ![霓虹太空站主題包](./docs/screenshots/pack-neon-station.png) | ![糖果地城主題包](./docs/screenshots/pack-candy-dungeon.png) |

## 快速開始

**單機版**：不需要帳號、郵件或兩步驟驗證。需要 Go 1.26.10、Node.js 22 與 Git：

```bash
git clone https://github.com/staphit/Nodevas.git && cd Nodevas
npm ci --prefix web && npm run build --prefix web && go build -o nodevas ./cmd/nodevas
./nodevas serve --project ./workspace
```

開啟 <http://127.0.0.1:5666>。同一個指令也會啟動執行 coding agent 的 runner；加上 `--repo ~/code/my-app` 讓 run 能在該 repo 工作，或用 `--no-runner` 不啟動它。更多內容（含 Linux 上的 Docker）：[Standalone](./docs/getting-started.md#standalone)（英文）。

**團隊 server**：在 server 或 NAS 上用 Docker（`ghcr.io/staphit/nodevas`）執行 hub，經 HTTPS 對外，並設定管理員帳號（`nodevas user add`）與寄送登入驗證碼的 SMTP relay；大家用瀏覽器開啟。每個人可在自己電腦上用 `scripts/install.sh --hub <url> --link` 加裝 runner，它會下載經 GitHub 驗證的執行檔，或在有 Go 1.26 以上的 checkout 裡從原始碼建置（[細節](./docs/install.md)，英文）。步驟：[Team server](./docs/getting-started.md#team-server)（英文）。

## 在自己電腦上跑 agent

https://github.com/user-attachments/assets/8e494381-8aeb-4c58-9cf8-251f4c0f3060

server 只負責顯示看板；你電腦上的小程式 **runner** 用你自己的登入執行 agent，程式碼不會離開你的電腦。設定、安全、自動推進與更多示範影片：[Agent 工作站](./docs/workstation.zh-TW.md)。

## 延伸閱讀

- [核心概念](./docs/concepts.md) · [時間軸](./docs/timeline.md) · [MCP](./docs/mcp.md) · [語音 copilot](./COPILOT.md) · [Token 用量](./docs/usage.md)
- [安裝 runner](./docs/install.md) · [維運](./docs/operations.md) · [版本歷史與備份](./docs/history-and-backup.zh-TW.md) · [團隊 server 與共編](./docs/collaboration.md)
- 範例：[AI harness](./demo/ai-harness-engineering) · [日常排程](./demo/daily-work-schedule) · [小說撰寫](./demo/novel-writing)
- [協助開發](./docs/contributing.md) · [程式碼簽章政策](./docs/code-signing-policy.md)

Agent 工作站、MCP、即時共編、備份與歷史紀錄目前為 Beta，之後可能調整。

## 授權

[GPL-3.0](./LICENSE)
