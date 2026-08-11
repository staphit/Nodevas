# `domain/` — source of truth 與 command 層 [A-01 / A-04]

前端不再讓元件自行決定「這筆資料該寫去哪」。每個持久化概念都在
`registry.ts` 有一列，命名在 `glossary.ts`，寫入方式在 `commands.ts`。

## 1. 為什麼要分

`graph.yaml` 存**預期**，`run/journal.jsonl` 存**實際**。兩者在舊 UI 都被叫做
「狀態」，造成使用者無法分辨自己改的是計畫還是紀錄。命名規則：

| 概念 | Wire 型別 | Domain 名稱 | UI 文案 |
|---|---|---|---|
| 實際生命週期 | `Status` | `LifecycleStatus` | 實際狀態 |
| 實際事件 | `HistoryEvent` | `LifecycleEvent` | 實際紀錄 |
| 預期里程碑 | `PlanMilestone` | `PlannedMilestone` | 預期里程碑 |
| 里程碑類型 | `PlanStatus` | `MilestoneType` | 里程碑類型 |

JSON 欄位名一律不動；alias 只改 TypeScript 這一側的可讀性。

## 2. Registry 欄位

`registry.ts` 每個 domain 都必須填：

- `sourceOfTruth` / `diskPath`：唯一真相與檔案位置。
- `api`：允許碰它的 endpoint。
- `owner`：唯一的編輯入口（UI 畫面）。
- `readers`：允許讀的畫面。
- `writers`：允許寫的 store 進入點；不在名單上的寫入即為 bug。
- `savePolicy`：`commit-autosave` / `manual-save` / `explicit-apply` /
  `instant-local` / `none`。
- `undo`：`command` / `editor` / `compensating` / `reset` / `none`。
- `derived`：衍生快取（例如 `run/state.json`），永遠不直接編輯。

## 3. 寫入路徑

```
UI  →  store command  →  applyGraphCommand(draft, command)   (純函式，可測試)
                      →  op-able?  POST /api/graph/ops（無 baseRev，逐節點）
                                   否則 PUT /api/graph（帶 baseRev，樂觀鎖）
                      →  operation state: pending → saved | error | conflict
```

`graphOps.ts` 判斷哪些指令 op-able（畫布拖曳、節點縮放、節點欄位、時間軸排序、刪除關係）：
這些走 `/api/graph/ops`，不帶 baseRev，所以兩人改同一張圖的不同部分不會互相 409，
且套用後採用伺服器合併後的 graph。其餘指令仍走整檔 `PUT /api/graph`。

Journal domain 不走 graph command：

```
UI  →  setLifecycleStatus / moveActualEvent  →  POST /api/nodes/{id}/status
                                             →  POST /api/events/move
```

journal 只 append，undo 一律用 compensating event，不刪除歷史。

## 3.1 誰可以直接呼叫 `api`

`writers` 是規則,`web/scripts/check-api-imports.mjs` 是它的守門員。
`components/` 底下每個直接 `import { api }` 的檔案都記在
`web/scripts/api-import-baseline.txt`,CI 只允許這份名單變短。

新元件要寫資料就走 slice command。真的沒有對應概念(登入、備份設定這類),
才用 `npm run check:api-imports -- --write` 把它加進基準線,並在 PR 說明為什麼。
測試不受限:它 import 是為了 mock,不是寫入路徑。

## 4. 新增一個持久化概念的順序

1. `registry.ts` 補一列。
2. `glossary.ts` 補命名（如果是新概念）。
3. `commands.ts` 補一個 command + 純函式 reducer。
4. `store.ts` 對外只暴露 typed command，不再新增 `saveGraph` 呼叫端。
5. 補測試：reducer 純函式測試 + store 的 conflict / rollback 測試。
