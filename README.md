# Nodevas

Nodevas 是一個本機優先的節點式 Markdown 編輯器。每個節點是一份文件，關係線描述依賴，時間軸則分開保存預期日期與實際紀錄。

專案資料留在自己的資料夾裡，以 YAML、Markdown、JSON 與 JSONL 儲存。你可以直接用文字編輯器讀取，也能交給 Git 或其他 agent 處理。

## 目前能做什麼

- 用專案樹管理多個專案與子專案，支援搜尋、切換、解除匯入與磁碟刪除。
- 在關係圖自由拖放節點，預設不貼齊網格；需要時可從工具列開啟「貼齊格線」，拖曳中按住 Alt 就暫時不貼齊。畫布可平移，Alt＋滾輪會以游標位置為中心縮放，滾輪維持一般捲動。
- 從空白處右鍵新增節點或邏輯閘；節點 ID 由系統自動分配。
- 用 MUST、AND、OR、XOR、NAND、NOR 組合依賴。條件未接完整時會標成錯誤。
- 調整關係線的實線／虛線樣式，並用 `Alt` + 左鍵加入轉折點。
- 分開管理預期規劃與實際狀態。兩者都能拖曳改日期，實際異動會寫入歷史。
- 切換直式或橫式時間軸，拖曳節點標題改變顯示順序。
- 指派節點負責人；未指派時顯示「尚未指派」。
- 設定優先度、標籤、卡片尺寸、底色、文字色與外框色；菱形與六角形這類裁切形狀會自行描邊，每一邊都看得到。可批次切換狀態或負責人，也可以一次刪除整組選取（一次撤銷即全部復原）。
- 專案樹上 `Ctrl` / `⌘` + 點擊多選、`Shift` + 點擊選取範圍（節點與專案都適用），右鍵直接對整組操作。
- 依狀態、負責人、標籤與優先度篩選，並保存常用檢視；時間軸可排序但不改動關係圖位置。
- 在畫布建立可拖曳、縮放的群組區域與註解，並使用縮圖、全部置中或選取置中快速導覽。
- 以逾期、依賴違反、受阻節點與阻塞源提示排程問題，並估算關鍵路徑。
- 編輯 Markdown、預覽內容、查看語法提示，並為節點建立子頁面或彈出獨立視窗。
- 子頁可選檔案格式：Markdown、純文字、HTML 或 Word（`.docx`）；也能把現有檔案匯入成子頁，或把一頁轉成別的格式。
- 插入表格，游標移進表格後可直接增刪、搬移欄列、設定對齊並重新對齊欄寬。
- 把文件另存成 Word、純文字、網頁或 PDF，範圍可選這一頁、整個節點或整個專案。
- 匯入 Markdown 節點，或用 `.veproj` 與通用 JSON Canvas 搬移資料。
- 自動保存草稿、保留檔案歷史、偵測外部修改衝突；刪除節點或子頁面時先移入統一垃圾桶。
- 透過 WebSocket 接收磁碟變更，不必手動重新整理頁面。

## 快速開始

需要：

- Go 1.24.4
- Node.js 與 npm

先建立前端，再啟動 Go 服務：

```powershell
cd web
npm ci
npm run build

cd ..
go run ./cmd/nodevas serve -project ./examples -port 5666
```

開啟 <http://127.0.0.1:5666>。

`-project` 指向工作區，不是單一專案。工作區下每個含有 `graph.yaml` 的資料夾都會顯示為專案。

`serve` 也會讀取工作區根目錄的選用設定檔 `nodevas.yaml`；命令列旗標會覆蓋設定檔，設定檔會覆蓋內建預設值。也可以用
`-config path/to/server.yaml` 指定其他檔案，或用 `NODEVAS_CONFIG` 指定設定檔路徑。

可參考根目錄的 [`nodevas.yaml.example`](./nodevas.yaml.example)：

```yaml
listen: 0.0.0.0
port: 5666
hostname: nodevas.example.com
behind_proxy: true
trusted_proxy: 127.0.0.1/32
max_active_users: 20

smtp:
  host: smtp.example.com
  port: 587
  user: nodevas@example.com
  from: Nodevas <no-reply@example.com>
  security: starttls

logging:
  level: info
  format: json
```

SMTP 密碼不寫入 YAML，仍使用 `NODEVAS_SMTP_PASSWORD`。部署環境也能用 `NODEVAS_SERVE_*` 環境變數覆蓋 YAML；完整設定名稱對應集中在 `internal/config`。

### 建立執行檔

Windows：

```powershell
cd web
npm ci
npm run build

cd ..
go build -o nodevas.exe ./cmd/nodevas
.\nodevas.exe serve -project .\examples -port 5666
```

macOS / Linux：

```bash
cd web
npm ci
npm run build

cd ..
go build -o nodevas ./cmd/nodevas
./nodevas serve -project ./examples -port 5666
```

Go 會把 `web/dist` 嵌入執行檔，因此前端必須先完成建置。

### macOS DMG

macOS 桌面版使用 Universal App，同時支援 Apple Silicon 與 Intel Mac。GitHub tag `testflight-v1.0` 會產生顯示版本 `testflight v1.0` 的 DMG、更新用 ZIP 與更新描述檔；後續 testflight tag 會沿用相同自動更新頻道。

建置、簽章與 GitHub Release 設定請見 [macOS 發行說明](./packaging/macos/README.md)。

### 對外提供服務

預設 `-listen 127.0.0.1` 只接受本機連線，此時 server 一律拒絕 `Host` 標頭不是
`localhost`／`127.0.0.1`／`::1` 的請求。這道檢查擋的是 DNS rebinding：惡意網頁把自己的網域
解析到 127.0.0.1，就能讓 Origin 與 Host 互相吻合而通過同源判斷。基於同樣理由，本機來源也必須
與實際監聽的 port 相同，`http://localhost:3000` 這種其他本機服務不再被視為自己人。

監聽非 loopback 位址時必須先建立帳號（`nodevas user add`），並且需要 TLS，或明確以
`-allow-plaintext` 承擔風險。反向代理後方的部署請補上兩個旗標：

```bash
./nodevas serve -project ./examples -port 5666 \
  -listen 0.0.0.0 -hostname nodevas.example.com -behind-proxy
```

- `-hostname` 逗號分隔，列出這台 server 對外真正使用的網域名稱。未提供時，wildcard 監聽會接受任何
  `Host`，以免既有部署失效，但也就失去這層保護。
- `-behind-proxy` 才會信任 `X-Forwarded-Proto`。預設不信任，因為任何 client 都能偽造這個標頭，
  藉此讓 session cookie 被標成 `Secure` 而遭瀏覽器丟棄，或繞過「設定 OAuth 憑證需要 HTTPS」的檢查。
  **在 TLS 終結於代理的部署忘記加這個旗標，Google OAuth 的 redirect URI 會變成 `http://` 而被拒絕。**

### 桌面 App 連接工作區

桌面 App 會先啟動本機 server，再顯示系統資料夾選擇器。可選本機資料夾，或已由 Google Drive、OneDrive 等工具鏡像／掛載到本機的雲端資料夾。連接後，workspace 會記住；之後可從 App 選單的「連接工作區…」重新切換，不需要命令列參數。

雲端資料夾必須先在作業系統中可讀寫。Google Drive 也可以不掛載磁碟，直接從「加入工作區」進入 OAuth 資料夾瀏覽器；Nodevas 會把選定的 `.veproj` 快照匯入本機，編輯仍走本機檔案系統。

若要直接連接 Google Drive，請在 App 的 Google Drive 設定中輸入 OAuth Client ID 與
Client Secret。憑證會加密存放在本機 App secrets，不會寫入 workspace、專案或 `.veproj`；
Server 部署也必須在管理介面輸入並儲存加密憑證；不再讀取 OAuth client 的環境變數 fallback。
封裝 App 請建立 Google OAuth「Desktop app」client，Nodevas 會使用啟動時產生的
`127.0.0.1:<port>` loopback callback；若是固定網址部署，再將實際 HTTPS callback 登記到 Web client。
在「加入工作區」選「使用 Google Drive OAuth」後完成授權；Nodevas
會開啟 Drive 資料夾選擇器，列出 Nodevas `.veproj` 快照，將選定快照匯入本機 workspace，之後可手動
或依排程備份回同一個 Drive 資料夾。授權使用 `drive.metadata.readonly`（瀏覽資料夾 metadata）與
`drive.file`（建立 Nodevas 自己的備份），不把 Drive 假裝成本機即時檔案系統。

桌面 App 的 packaged release 不會內建任何特定部署的 URL，預設仍啟動內嵌的本機 Nodevas。若要
連接 OCI 或其他遠端部署，設定遠端 HTTPS URL：

```bash
NODEVAS_SERVER_URL="$(terraform -chdir=deploy/oci/terraform output -raw app_url)" \
  open -a "Nodevas"

# 或在 macOS App 的啟動參數指定
open -a "Nodevas" --args --server-url "$(terraform -chdir=deploy/oci/terraform output -raw app_url)"
```

遠端模式不會啟動本機 binary，也不會顯示本機資料夾選擇器；登入、工作區與檔案都由 OCI
伺服器管理。非 loopback 的 URL 必須使用 HTTPS。`NODEVAS_SERVER_URL` 或
`--server-url` 會覆蓋 packaged release 的預設 URL。

## 開發模式

後端：

```powershell
go run ./cmd/nodevas serve -project ./examples -port 5666
```

另一個終端啟動 Vite：

```powershell
cd web
npm run dev
```

開啟 <http://127.0.0.1:5173>。Vite 會把 `/api` 與 `/ws` 轉送到 5666。

## 操作方式

| 操作 | 結果 |
|---|---|
| 拖曳空白畫布 | 平移關係圖 |
| `Ctrl` / `Cmd` + 拖曳空白處 | 框選多個節點；拖曳任一已選節點可整組移動 |
| 專案樹上 `Ctrl` / `Cmd` + 點擊 | 多選節點或專案，不開啟 |
| 專案樹上 `Shift` + 點擊 | 從上次點擊處選到這裡；加 `Ctrl` 則是再加一段範圍 |
| 滑鼠滾輪 | 捲動頁面 |
| `Alt` + 滾輪 | 以游標位置縮放 |
| 空白處右鍵 | 新增節點或邏輯閘 |
| 節點上 `Alt` + 左鍵拖曳 | 建立連線 |
| 關係線上 `Alt` + 左鍵 | 新增轉折點 |
| 關係線右鍵 | 切換線條樣式 |
| `Delete` / `Backspace` | 刪除目前選取的節點、線或閘門 |
| `Enter` | 開啟選取節點 |
| `Escape` | 取消選取或關閉目前操作 |
| `Ctrl` / `Cmd` + `C` / `X` | 複製／剪下選取的節點，暫存到節點剪貼簿 |
| `Ctrl` / `Cmd` + `V` | 把剪貼簿的節點貼進目前開啟的專案 |
| `Ctrl` / `Cmd` + `S` | 儲存目前文件 |
| `Ctrl` / `Cmd` + `Z` | 還原上一個圖面操作 |
| `Ctrl` / `Cmd` + `K` | 開啟全工作區搜尋與指令面板 |

時間軸卡片不會套用關係圖的 `Delete` 快捷鍵；在時間軸按下 `Delete` 不會刪除關係圖節點。

### 把節點搬到其他專案

選取節點後，關係圖上方的批次工具列有「複製到專案…」與「移動到專案…」；也可以按
`Ctrl`/`Cmd` + `C`（或 `X`）暫存，切換到別的專案再按 `Ctrl`/`Cmd` + `V` 貼上。剪下
只是暫存，真正的搬移發生在貼上的那一刻，沒貼上就什麼都不會變。

一起帶過去的有：節點文件、子頁面、附件、畫布位置與樣式、預計里程碑、生命週期紀錄，
以及這些內容依賴的專案層級定義（負責人、自訂狀態、旗標）。節點 ID 由目標專案重新配
發，所以同名不會覆蓋既有節點；文件內的附件連結會一併改寫。

有些東西無法跟著走，會在完成後列出來：

- 前置條件指向沒有一起帶走的節點——留著會變成斷掉的參照，因此整條清除。
- 只有部分節點在選取範圍內的關係線與邏輯閘。
- 目標專案已有同名（但不同人）成員 ID 時的指派。

「移動」會先複製到目標專案，成功後才把來源節點移到來源專案的垃圾桶，隨時可以還原。
如果來源還有節點依賴被搬走的節點，整個動作會在寫入前就中止。

## 專案格式

一個工作區可以放多個專案：

```text
workspace/
├── project-a/
│   ├── graph.yaml
│   ├── nodes/
│   │   ├── node-0001.md
│   │   ├── node-0001.pages/
│   │   │   ├── pages.json
│   │   │   └── notes.md
│   │   └── node-0002.md
│   ├── run/
│   │   ├── state.json
│   │   └── journal.jsonl
│   └── .vised/
│       ├── drafts/
│       ├── history/
│       └── trash/
└── project-b/
    └── graph.yaml
```

`graph.yaml` 是圖結構的主要來源，保存節點、關係、位置、邏輯閘、使用者與預期規劃。

`nodes/<id>.md` 保存節點文件。透過介面新增節點時，伺服器會分配 `node-0001` 形式的 ID；匯入既有 Markdown 時則保留可用的原始 ID。

`run/journal.jsonl` 是實際狀態異動紀錄。狀態可直接切換，每次修改都會留下時間、操作者與註解。`run/state.json` 是從紀錄整理出的目前狀態。

`.vised` 保存草稿、歷史版本與垃圾桶內容，屬於本機回復資料，預設不加入 Git。

## 預期規劃與實際紀錄

這兩種資料刻意分開：

- 預期規劃寫在 `graph.yaml`，用來安排預計開始、進行、完成日期，也能加入自訂里程碑。
- 實際狀態寫入 `run/journal.jsonl`，拖曳改期同樣會新增一筆稽核紀錄。

`requires` 只描述關係與驗證條件，不會鎖住狀態選單。使用者仍可直接修改目前狀態。

## 匯入與匯出

`.veproj` 是 ZIP 格式的可攜專案封裝，包含圖、節點文件、子頁面與執行紀錄。單一專案的 manifest 目前為第 2 版，匯入器仍接受第 1 版並套用相容遷移。另一位使用者可直接從側欄匯入；一般 `.zip` 也能使用相同流程。

匯出資料夾、工作區根，或帶有子專案的專案時，會改用第 3 版的**多專案封裝**（manifest 的 `kind` 為 `bundle`）：封裝內以目錄結構原樣鏡射該子樹，每個子專案各有自己的 `graph.yaml` 與 `nodes/`，manifest 的 `projects` 與 `folders` 記錄整棵樹。匯入時會一次還原整個子樹，並開啟其中最上層的專案。右鍵點側欄任一專案或資料夾即可選「匯出封裝」，匯出選單另有「整個工作區」。

若只想加入文件，可使用「匯入 MD」。系統會讀取 frontmatter，沒有 ID 時則依檔名產生，再由伺服器處理衝突。

若要和 Obsidian、JSON Canvas 相容工具交換圖面，可匯入或匯出 `.canvas`。標準節點、座標、尺寸與關係線會保留；Nodevas 專屬欄位會放在可忽略的相容性 `vised` 擴充資料中。

### 文件另存其他格式

編輯器工具列的匯出鍵可把文件存成 Word（`.docx`）、純文字（`.txt`）、獨立網頁（`.html`）或 Markdown（`.md`），PDF 則透過瀏覽器列印對話框的「另存為 PDF」產生。範圍可選目前這一頁、整個節點（主頁加所有子頁）或整個專案（所有節點合成一份，節點標題自動降階成章節）。

轉檔在伺服器端進行，未儲存的編輯內容會一併送出，所以匯出的永遠是畫面上看到的版本。標題、清單、待辦、引用、程式碼區塊、表格與超連結都會轉成對應的原生格式；`.docx` 與 `.html` 還會把節點附件圖片直接嵌入檔案，離開這台機器也看得到。

側欄「專案檔案」的匯入與匯出各收成一個選單：匯入可選專案封裝、Markdown 檔案或 JSON Canvas；匯出除了封裝與 Canvas，也能把整個專案輸出成上述任一種文件格式。

### 子頁的檔案格式

建立子頁時可以選它在磁碟上是什麼檔案：`.md`、`.txt`、`.html` 或 `.docx`。子頁清單 `nodes/<id>.pages/pages.json` 會記下每頁的格式，沒有這個欄位的舊專案一律當成 Markdown，不需要遷移。

- **Markdown**：原本的行為，即時排版、大綱、表格工具列都在。
- **純文字**：所見即檔案，不套用任何標記語法。
- **HTML**：直接編輯 HTML 原始碼，預覽會渲染它（經過同一套 DOMPurify 清理）。
- **Word**：開啟時把 `.docx` 轉成 Markdown 來編輯，存檔時再產生 `.docx`。文件裡的內嵌圖片會抽進節點附件資料夾，存回去時重新嵌入。

`.docx` 的來回轉換只保留這個編輯器表達得出的結構——標題、清單、待辦、表格與對齊、程式碼、引用、粗斜刪、連結、圖片。Word 端的進階排版（欄位、頁首頁尾、追蹤修訂、圖表）會在下次存檔時流失，介面上會先提醒。

已經存在的子頁可以在子頁工具列切換格式：舊檔進版本歷史，新檔用新的副檔名寫入。上傳現有的 `.md` / `.txt` / `.html` / `.docx` 也能直接變成子頁，內容維持原樣直到第一次存檔。

## 儲存與回復

所有正式寫入都使用暫存檔加原子替換，避免程序中斷時留下半份文件。覆寫前會建立歷史快照；外部編輯器同時改檔時，API 會回傳 `409 Conflict`，讓使用者選擇磁碟版或自己的版本。

刪除節點預設是軟刪除。文件先移到 `.vised/trash`，可從側欄復原。刪除專案則分成兩種：

- 解除匯入：只從專案樹隱藏，磁碟檔案保留。
- 刪除磁碟檔案：移除整個專案資料夾，無法從介面復原。

### 設定與憑證放在哪裡

工作區層級的設定寫在 `<工作區>/.vised/`，不在任何一個專案資料夾裡：

| 檔案 | 內容 |
|---|---|
| `.vised/notify.json` | 通知設定；SMTP 密碼不在此檔，另以 AES-GCM 加密存於本機 App secrets |
| `.vised/history/` | 檔案版本快照 |
| `.vised/trash/` | 軟刪除的節點與子頁 |
| `.vised/drafts/` | 未正常結束時保留的編輯草稿 |

`.veproj` 專案封裝只帶 `graph.yaml`、節點文件、子頁與執行紀錄，**不會**帶走 `.vised/`，所以分享封裝不會外洩郵件密碼。備份整個工作區資料夾則會包含它——那份備份要當作機密看待。

## 技術組成

- 後端：Go、`net/http`、fsnotify、WebSocket、YAML
- 前端：React、TypeScript、Vite、Zustand
- 文件：CodeMirror 6、marked、DOMPurify
- 資料：YAML、Markdown、JSON、JSONL

本機預設後端只監聽 `127.0.0.1`；對外部署必須設定帳號、TLS（或反向代理）與 hostname，
不要直接把 5666 port 暴露到公網。

## 目錄

```text
cmd/nodevas/       CLI 入口與本機伺服器
internal/engine/ 狀態、條件 DSL、驗證
internal/server/ REST API、WebSocket、檔案與專案管理
web/src/         React 介面
examples/        本機工作區（不進版本庫，首次啟動時建立）
```

更完整的資料模型與持久性說明：

- [DESIGN.md](./DESIGN.md)
- [PERSISTENCE.md](./PERSISTENCE.md)

## License

Nodevas is licensed under the [GNU General Public License v3.0](./LICENSE).

## 目前限制

這是單機、單人優先的工具。它會處理外部檔案衝突，但沒有 CRDT 或即時多人共同編輯；雲端備份是快照同步，不是即時協作層。Git 仍是專案內容的協作層。

專案尚未加入 `LICENSE`。公開發布前，請先決定允許他人使用、修改與散布的條款。

### 同步版本與關閉行為

Nodevas 會保存最後成功同步的快照 ID 與 SHA-256。啟動時比對本機與遠端版本：本機較新可立即上傳，雲端較新或雙方都改過則只提示，不自動覆蓋。開啟排程備份時，關閉 Server 前會在限時內嘗試完成最後一次同步。
