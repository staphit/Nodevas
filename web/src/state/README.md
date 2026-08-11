# `state/` — store slices、operation state、undo policy

`store.ts` 只做組裝與 selector。實作在這裡。

## 1. Slice 對應

| Slice | 負責 | 對應 domain |
|---|---|---|
| `operationsSlice` | pending／saved／error／conflict，依 scope 分開 | 全部 |
| `preferencesSlice` | 本機偏好；`theme`、`paneOpen` 為衍生值 | `ui-preference` |
| `workspaceSlice` | workspace、專案清單、切換、trash 清單 | — |
| `documentSlice` | 分頁、dirty、draft、conflict、手動存檔 | `document` |
| `runSlice` | 實際狀態、journal append | `lifecycle` |
| `graphSlice` | `graph.yaml` 全部寫入；`saveGraph` 與 typed command | `node-metadata`、`plan-milestone`、`workflow`、`project-layout` |
| `remoteSlice` | 備份設定、Drive 憑證與連線、備份清單、同步狀態、截止提醒設定 | — |
| `undoSlice` | 還原與重做；`canUndo`、`undoLabel`、`canRedo`、`redoLabel` | — |

`internals.ts` 放共用管線：graph 寫入佇列（同一 `baseRev` 不併發）、每份文件的存檔佇列、
以及 load／state generation（切專案後丟棄舊回應）。

`coalesce.ts` 管讀取合併：一次改動會同時來自 mutation 自己的 refresh、WebSocket 回聲、
以及檔案 watcher，過去每一個都各發一次 GET。同一資源在 30ms 視窗內的觸發合成一次請求，
所有呼叫者共用結果；已經在路上的請求不會拿來回答它出發之後才發生的觸發 ——
那種觸發會等它結束後再發一次。這是合併與批次，不是快取：完成的每一次請求都是當下的伺服器狀態。

## 2. Operation state

所有 mutation 走同一組詞彙：

```
idle → pending → saved
               → error     （已回滾，可重試）
               → conflict  （磁碟已變動，需要使用者決定）
```

scope 由 `operationScope.*` 產生（`node:<id>`、`plan:<id>`、`document:<id>`、
`lifecycle:<id>`、`workflow`、`canvas`、`workspace`、`run`）。
UI 用 `useOperation(scope)` 讀，不要自己管 busy／error boolean。
conflict 一定要使用者處理，不可自動清掉。

## 3. Undo policy [A-06]

| 類型 | 還原方式 | 是否進 undo stack |
|---|---|---|
| Graph command（節點、計畫、workflow、版面） | 還原前一版 graph 並重新 PUT | 是，帶 command label |
| 新增／刪除節點 | 刪除新節點／從 trash 還原 | 是 |
| 文件內容 | CodeMirror 自己的 undo | 否 |
| 實際狀態 | append 補償事件（回到前一個狀態） | 只有在前一個狀態本身是明確事件時 |
| 實際時間 | append 補償 move 事件 | 是（保留原時間） |
| 本機偏好 | Settings 的「重設」 | 否 |

原則：

1. journal 永不刪除。撤銷實際狀態＝再 append 一筆補償事件，note 標明是撤銷。
2. 衍生狀態（沒有事件的 `ready`／`locked`）不可還原 —— 補一筆事件會憑空造歷史，
   所以這種變更不進 stack。
3. 不宣稱可 undo 的操作不進 stack；`canUndo` 因此永遠不是樂觀值。
4. 還原失敗會把 entry 放回 stack，下一次 Ctrl/⌘+Z 重試同一步，不會跳過。
5. 切換專案／workspace 會清空 stack：舊專案的 snapshot 不可套用到新專案。
6. 補償寫入本身用 `recordUndo: false`，否則會形成 undo 迴圈。

## 4. Redo policy

Redo 不是另一套實作：套用一筆 entry 會回傳「撤銷這次套用」的 entry，放到另一個 stack。
撤銷 create 得到 delete，撤銷 delete 得到 create，補償事件的反面是再補償一次。

1. 撤銷成功 → 反向 entry 進 redo stack；重做成功 → 反向 entry 回 undo stack。
2. 使用者的新編輯（任何 `pushUndo`）清空 redo stack —— 那些 entry 描述的是已經分岔掉的未來。
   還原機制自己在兩個 stack 之間搬 entry 時用 `pushUndo(entry, { keepRedo: true })`。
3. 套用成功但算不出反向 entry（例如衍生狀態沒有事件可回去）→ 清空 redo stack，
   不留下會重做錯步驟的殘骸。
4. 失敗的重做把 entry 放回 redo stack，和 undo 的重試規則一樣。
5. `clearUndo()`（切換專案）兩個 stack 一起清。
6. 快捷鍵：`Ctrl/⌘+Shift+Z` 與 `Ctrl/⌘+Y`。
