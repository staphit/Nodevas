# Nodevas

English documentation: [README.md](./README.md)

> 將文件、相依關係、工作流程、排程與 AI agent 連在一起的視覺化工作區。

Nodevas 將分散的筆記與任務整理成一個可以看見、編輯並持續推進的工作區。每個節點都將 Markdown 內容與工作脈絡——metadata、負責人、優先度、標籤、連結、狀態與歷史——放在一起。關係在畫布上呈現，進度則可以透過時間軸規劃與回顧。

[快速開始](./docs/getting-started.md) · [核心概念](./docs/concepts.md) · [MCP 文件](./docs/mcp.md)

## 功能

| 功能 | 使用者得到什麼 |
| --- | --- |
| [圖形化工作區](./docs/concepts.md#nodes-and-relationships) | 用圖形、看板或時間軸檢視文件、任務與關係。 |
| [依賴條件邏輯](./docs/concepts.md#dependency-gates) | 使用 `MUST`、`AND`、`OR`、`XOR`、`NAND`、`NOR` 找出目前可以開始的工作。 |
| [計畫與進度](./docs/timeline.md) | 比較預期里程碑與實際狀態，不混淆計畫與歷史。 |
| [MCP 整合](./docs/mcp.md) | 讓 AI agent 找出 ready task、認領工作、更新節點、管理依賴並回報狀態。 |
| [本機或共享工作區](./docs/collaboration.md) | 可在本機私人使用，也能部署共享 server 進行團隊協作。 |

## Beta

以下功能已可使用,但仍在 Beta 階段,後續版本可能調整:

- [MCP 整合](./docs/mcp.md):AI agent 工具、資源與 prompts。
- [即時共編](./docs/collaboration.md):共享 server、WebSocket 上線狀態與 CRDT 文件 session。
- 雲端備份:Google Drive 同步、備份與還原。
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

## 其他能力

- 在同一個工作區樹狀結構中管理多個專案與子專案。
- 支援 Markdown、純文字、HTML 與 Word 文件頁面。
- 可匯入 Markdown、JSON Canvas 與 `.veproj`；可匯出 Word、純文字、HTML、Markdown 與 PDF。
- 依狀態、負責人、標籤、優先度與專案篩選，並儲存常用檢視。
- 支援草稿、歷史版本、垃圾桶、衝突偵測與檔案變更通知。
- 專案檔案保持可檢查，也能搭配 Git 與一般文字工具使用。

## 快速開始

必要環境：Go 1.25.12、Node.js 22 與 npm、Git。

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
- [核心概念](./docs/concepts.md)：節點、連線、依賴條件邏輯、ready 狀態與節點 metadata。
- [時間軸](./docs/timeline.md)：預期里程碑、實際事件與日期判定。
- [MCP 整合](./docs/mcp.md)：連接 Claude Code、Codex 或其他 MCP client。
- [儲存與協作](./docs/collaboration.md)：專案檔案、共享 server、WebSocket 與 CRDT 行為。
- [OCI 部署](./deploy/oci/README.md)：建立與維運共享雲端部署。
- [協助開發](./docs/contributing.md)：開發檢查與程式位置。

## 程式碼簽章

Windows 版本規劃使用 [SignPath.io](https://signpath.io) 提供的免費程式碼簽章,憑證由 [SignPath Foundation](https://signpath.org) 提供。詳見[簽章政策](./docs/code-signing-policy.md)。

## 授權

Nodevas 採用 [GNU General Public License v3.0](./LICENSE) 授權。
