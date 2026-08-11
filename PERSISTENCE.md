# 文件持久性設計 — Survey 與方案

> 議題:文件不應該是揮發性的。目前 v1 實作是「直接覆寫檔案」——
> 程序在寫入中途崩潰可能留下半寫檔案、存檔沒有歷史版本、刪除是永久的、
> UI 與外部編輯器同時改檔是 last-write-wins。本文先看業界怎麼做,再提出本專案的方案。

---

## 1. Survey:其他文件系統的做法

### VS Code(桌面編輯器)

| 機制 | 做法 |
|---|---|
| 原子存檔 | 寫入暫存檔 → rename 蓋回原檔(rename 在同一磁碟區是原子操作,不會出現半寫檔案) |
| Hot Exit | 未存檔的 buffer 持續備份到使用者資料夾;崩潰重開後還原 |
| 磁碟衝突 | file watcher 偵測「檔案在磁碟上被改了」;若 buffer 也髒 → 顯示 "Compare / Overwrite / Revert" 對話框,不默默覆蓋 |
| Local History | 每次存檔把舊版快照進 history 目錄;Timeline 面板可回滾任意版本 |

### Obsidian(markdown 知識庫,與我們最像)

| 機制 | 做法 |
|---|---|
| 純文字為本 | vault 就是資料夾,md 檔是唯一真相 —— 跟我們同哲學 |
| File Recovery | 內建外掛,每隔 N 分鐘把有變動的檔案快照到內部資料庫,可回溯數週 |
| 刪除 | 預設移到 vault 內 `.trash/` 資料夾,不是真刪 |
| 同步衝突 | Obsidian Sync 保留版本歷史,衝突時兩版都留 |

### Google Docs / Notion(雲端協作)

| 機制 | 做法 |
|---|---|
| 操作日誌 | 不存「整份文件」而是 append-only 的操作串(op log);文件 = 重放所有操作的結果 |
| 版本歷史 | 定期把 op log 壓成 named snapshot |
| 併發 | OT/CRDT 合併多人同時編輯 |

### Git 為底的系統(Logseq、各種 wiki)

- 每次存檔(或 debounce 後)自動 `git commit` → 免費獲得完整歷史、diff、回滾
- 缺點:.git 對非工程師不透明;大量小 commit 汙染歷史

### SQLite / 資料庫(WAL journaling)

- 永不直接改主檔:先 append 到 write-ahead log,fsync,之後才 checkpoint 進主檔
- 崩潰後重放 WAL 即可復原 —— 「先記日誌再動本體」是持久性的根本手段

### 共同模式(結論)

1. **原子寫入**:temp file + rename,永遠不留半寫檔案 — 所有系統的地板
2. **改前留舊版**:local history / snapshot / git commit — 存檔永遠可回滾
3. **刪除 = 搬移**:trash 資料夾,不是 `rm`
4. **衝突要偵測**:寫入前比對「我讀到的版本」和「磁碟現在的版本」,不一致就停下來問人
5. **未存檔內容也要活過崩潰**:draft/hot-exit 備份
6. **append-only 日誌**最耐崩潰:狀態類資料先 append 再壓縮

---

## 2. 本專案方案(v1 落地版)

專案資料夾內新增一個隱藏目錄,所有持久性機制都收在裡面,不汙染使用者的文件:

```
my-project/
├── graph.yaml
├── nodes/*.md
├── run/
│   ├── state.json          # 快取(可重算)
│   └── journal.jsonl       # ★ append-only 事件日誌(真相來源)
└── .vised/
    ├── history/            # ★ local history:改檔前的舊版快照
    │   ├── graph.yaml/20260729-103000.yaml
    │   └── nodes/intro.md/20260729-102500.md
    ├── trash/              # ★ 刪除的節點搬來這裡
    │   └── 20260729-104000-intro.md
    └── drafts/             # ★ 未存檔的 tab 內容(hot exit)
        └── intro.md
```

### 2.1 原子寫入(所有寫檔一律走這條)

```
writeAtomic(path, data):
    tmp = path + ".tmp-" + random
    write(tmp, data); fsync(tmp)
    rename(tmp, path)          # 原子:成功=新版,失敗=舊版,永無半寫
```

### 2.2 Local History(存檔即快照)

- 每次要覆寫 `graph.yaml` 或 `nodes/*.md` 前,先把**現有磁碟版本**複製到
  `.vised/history/<相對路徑>/<timestamp>.<ext>`
- 保留策略:每檔最多 N 版(預設 50)+ 總量上限;超過刪最舊
- 新 API:`GET /api/history/{path}` 列版本、`POST /api/history/{path}/restore` 回滾
- 「回滾」本身也是一次存檔(會再快照當前版),所以回滾永遠可以反悔

### 2.3 衝突偵測(樂觀鎖)

- `GET /api/nodes/{id}` 回傳 `content` + `rev`(內容 sha256 前 12 碼)
- `PUT` 必須帶 `baseRev`;server 比對磁碟當前 rev:
  - 一致 → 快照舊版 → 原子寫入 → 回新 rev
  - 不一致(外部編輯器改過了)→ **409 Conflict** + 磁碟版內容
- UI 收到 409:顯示「磁碟版 / 你的版本」對照,讓使用者選擇或手動合併 —— 絕不默默覆蓋
- `graph.yaml` 同樣機制

### 2.4 刪除 = 進垃圾桶

- `DELETE /api/nodes/{id}` → md 檔搬到 `.vised/trash/<timestamp>-<id>.md`,
  graph.yaml 照常移除(graph.yaml 本身有 history 可回滾)
- `GET /api/trash` / `POST /api/trash/restore`

### 2.5 實際狀態以 journal 為真相

- 每次狀態切換 **append 一行**到 `run/journal.jsonl`(append 天然耐崩潰):
  `{"t":"...","event":"status","node":"intro","from":"","to":"done","by":"agent-1"}`
- `state.json` 降級為快取:由 journal 重放產生,崩潰壞了就重建
- Timeline 檢視直接吃 journal —— 這正好也是使用者要的「每格留下紀錄」的資料源
- 預計開始/進行中/完成日期與註解存於 `graph.yaml → ui.plans`,不寫入 journal;規劃與實際紀錄分離
- 實際狀態由 server 驗證 `ready → started → in_progress → done`;不可倒退或略過階段

#### Checkpoint(journal 壓縮)

journal 不能無限長。超過軟門檻(8 MiB)時,下一次 append 之前先做一次 checkpoint:

```
run/
├── checkpoint.json     # ★ 壓縮後的完整狀態 + 它涵蓋的 journal 前綴(bytes + sha256)
├── journal.jsonl       # checkpoint 之後的事件
└── journal.jsonl.1     # ★ 前一段 journal(只保留一份,供鑑識/回退)
```

1. 重放全部事件 → `WriteAtomic(checkpoint.json)`(fsync 後 rename,落地才算數)
2. **落地之後**才把 journal 輪替成 `journal.jsonl.1`,開新檔繼續 append

順序就是重點:崩在兩步之間不會掉資料,只是多重放幾筆。載入時比對
`checkpoint.journalSha256` 與現有 journal 前綴——相符表示尚未輪替(只重放尾巴),
不符表示已輪替(整份都是新事件),因此不會重複計算。checkpoint 缺失或損毀時
退回 `journal.jsonl.1 + journal.jsonl` 全量重放,checkpoint 永遠不是單點故障。

### 2.6 Draft(hot exit)

- 前端:tab 內容髒了 → debounce 寫 `PUT /api/drafts/{id}`(存到 `.vised/drafts/`)
- 重開 UI:若 draft 比正式檔新 → 提示「還原未儲存的編輯?」
- 正式存檔成功 → 刪掉對應 draft

### 2.7 明確不做(v1)

- CRDT/OT 即時協作(單人本地工具,衝突偵測+人工合併夠用)
- 自動 git commit(留為選配指令 `nodevas checkpoint`,不預設)

---

## 3. Editor Script:一次編輯的完整腳本

逐步走一遍,驗證上面機制怎麼咬合。`★` = 本方案新增的行為。

```
場景 A:正常編輯
─────────────────
1. 使用者點畫布節點 intro
2. UI: GET /api/nodes/intro            → {content, rev: "a1b2c3"}
3. 使用者打字…
4. UI(debounce 2s): PUT /api/drafts/intro   ★ 未存檔內容落地
5. 使用者按 Ctrl+S
6. UI: PUT /api/nodes/intro {content, baseRev: "a1b2c3"}
7. Server: 磁碟 rev == a1b2c3 ✓
   a. copy nodes/intro.md → .vised/history/nodes/intro.md/20260729-1030.md  ★
   b. writeAtomic(nodes/intro.md, 新內容)                                   ★
   c. delete .vised/drafts/intro.md                                        ★
   d. 回 {ok, rev: "d4e5f6"}
8. UI 更新 tab 的 rev,顯示「已儲存」

場景 B:外部編輯器同時改了檔
─────────────────
1-4. 同上,使用者 tab 是髒的,手上 baseRev = "a1b2c3"
5. 使用者同時用 VS Code 改了 nodes/intro.md 並存檔
6. fsnotify → ws 廣播 node-changed:intro
7. UI:tab 是髒的 → 不自動重載,tab 標示「磁碟已變更」  ★
8. 使用者按 Ctrl+S → PUT baseRev: "a1b2c3"
9. Server: 磁碟 rev 已是 "x7y8z9" ✗ → 409 + 磁碟版內容        ★
10. UI 對照視窗:[磁碟版] [我的版本] → 使用者選擇/合併 → 重新 PUT(帶新 baseRev)
    ※ 兩版都沒有遺失:磁碟版在 history 有快照,使用者版在 drafts 有備份

場景 C:程序在存檔中途崩潰
─────────────────
1. writeAtomic 寫到一半斷電 → 只留下 .tmp-xxx 殘檔,原檔完好   ★
2. 重啟:server 清掃 *.tmp-*;UI 發現 drafts/intro.md 比正式檔新
   → 「還原未儲存的編輯?」                                      ★

場景 D:刪錯節點
─────────────────
1. DELETE /api/nodes/intro
2. Server: intro.md → .vised/trash/20260729-1040-intro.md      ★
           graph.yaml 先快照再原子改寫                          ★
3. 使用者後悔 → GET /api/trash → restore
   graph.yaml 從 history 回滾 or 重新加回節點,md 從 trash 搬回

場景 E:agent 切狀態(timeline 紀錄)
─────────────────
1. POST /api/nodes/build-api/status {status:"started", by:"agent-1", note:"開始處理"}
2. POST /api/nodes/build-api/status {status:"in_progress", by:"agent-1", note:"實作中"}
3. 完成後 POST /api/nodes/build-api/status {status:"done", by:"agent-1", note:"已驗收"}
4. Server:每次合法轉移 append journal.jsonl 一行(fsync)       ★
           重算 state.json(壞了也無所謂,journal 可重放)        ★
5. ws 廣播 → Timeline 檢視在今天格子顯示實際紀錄與註解
```

---

## 4. 實作切分

| 步驟 | 內容 | 動到的檔 |
|---|---|---|
| P1 | `writeAtomic` + tmp 清掃(全部寫入點換掉) | `internal/server/store.go` |
| P2 | local history + rev 樂觀鎖(409 流程) | store.go、server.go、web/src |
| P3 | trash + restore API | store.go、server.go、Sidebar |
| P4 | journal.jsonl + state.json 降級快取 | engine/state.go、store.go |
| P5 | drafts(hot exit)+ UI 衝突對照視窗 | server.go、web/src |
```
