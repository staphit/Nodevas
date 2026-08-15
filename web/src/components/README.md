# `components/` — 扁平檔與功能資料夾的分界

這個資料夾裡有兩種東西：直接掛在 `App.tsx` 上的全域單例、跨功能共用的原語，
以及被**單一**父元件擁有的功能內部檔案。前兩種放在最上層（扁平），
第三種一定收進功能資料夾。

## 1. 為什麼要分

`LaneView.tsx` 一個元件底下就有 55 個檔案（`canvas/` + `lane/` + `timeline/`），
`Drawer.tsx` 底下有 21 個（`drawer/` + `inspector/`）。全部攤平在 `components/`
會有兩個後果：

1. 看不出誰擁有誰。`useCardResize.ts` 只有 `LaneView` 會用，但攤平之後它跟
   `BackupModal.tsx` 長得一樣重要，改它的人不知道爆炸半徑只有一個元件。
2. 反過來也一樣糟。真正全域的東西（`ConfirmDialog`、`InteractionPrimitives`）
   淹在一百個檔名裡，新來的人不會發現已經有共用元件，就再寫一個。

所以規則不是「檔案太多就開資料夾」，而是**擁有者是誰**：

```
App.tsx  ──掛載──▶  扁平：全域單例 / modal
         ──使用──▶  扁平：共用原語（InteractionPrimitives、touch）
                            ▲
                            │ 誰都可以用
LaneView.tsx ──擁有──▶ canvas/ lane/ timeline/  ─┐
Drawer.tsx   ──擁有──▶ drawer/ inspector/       ─┤ 只有擁有者可以用
Sidebar.tsx  ──擁有──▶ explorer/                 ─┘
```

## 2. 資料夾與擁有者

| 資料夾 | 擁有者 | 內容 |
|---|---|---|
| `canvas/` | `LaneView.tsx` | 畫布幾何（`geometry.ts`）、拖曳／縮放／框選／平移 hooks、`GraphCanvas`、`GraphMinimap`、`LogicGate`、`DependencyLine` |
| `lane/` | `LaneView.tsx` | 看板版面與視窗（`boardGeometry`、`useBoardViewport`）、邊指令、右鍵選單層 `LaneContextMenuLayer`、`GraphAnalysisPanel` |
| `timeline/` | `LaneView.tsx` | 時間軸格線與卡片、日期與尺寸計算、里程碑拖曳 |
| `menus/` | `lane/LaneContextMenuLayer.tsx` | 六個右鍵選單元件：節點、關係、圖、邏輯閘、計畫、新增節點 |
| `drawer/` | `Drawer.tsx` | 編輯器擴充與工具列、表格編輯、附件、autosave、子頁與匯出 |
| `inspector/` | `Drawer.tsx` | 抽屜右側的節點面板：中繼資料表單、關係、實際紀錄、生命週期、外觀、大綱 |
| `explorer/` | `Sidebar.tsx` | 專案／節點樹、右鍵選單、專案建立／改名／搬移／另存、工作區樹、匯入備份對話框、排序與篩選 hooks |
| `tour/` | `App.tsx` | `OnboardingTour` 與它的章節資料 `chapters.ts` |

三點要注意，它們是規則的真實樣貌，不是例外：

- **`menus/` 的擁有者不是扁平檔**，是 `lane/LaneContextMenuLayer.tsx`。
  往上追還是 `LaneView`，但直接匯入者只有那一個檔案，所以它仍然是單一擁有者。
- **`inspector/` 的擁有者是 `Drawer.tsx`，不是 `LaneView` 也不是 `Sidebar`。**
  唯一的外部匯入是 `menus/NodeCreateMenu.tsx` 借用 `AppearanceControls`；
  這已經是第二個讀者，如果再多一個，`AppearanceControls` 就該往上搬（見 §3）。
- **`tour/` 的擁有者是 `App.tsx` 本身。**`OnboardingTour` 是全域單例，
  照 §3 本來該扁平，但它有自己的章節資料，所以單例加內部檔一起收成一個資料夾。

`canvas/geometry.ts` 被 `lane/` 與 `timeline/` 大量共用。這不違規：三個資料夾
同屬 `LaneView` 這一棵樹，共用只發生在擁有者內部。

## 3. 新檔案該放哪

依序問：

1. **`App.tsx` 會直接掛它嗎？**（modal、host、橫幅、命令面板）→ 扁平。
   現況的這一類：`ConfirmDialog`（`ConfirmDialogHost`）、`ProjectPickerDialog`
   （`ProjectPickerHost`）、`CommandPalette`、`NotifySettingsModal`、`BackupModal`、
   `ProjectSettings`、`RemoteSyncBanner`、`TopbarOverflow`、`SignIn`、`Sidebar`、
   `GraphPane`／`TimelinePane`、`Drawer`／`PopoutEditor`（lazy）。
2. **兩個以上功能都要用嗎？** → 扁平，或併進既有原語。
   `InteractionPrimitives.tsx` 放無狀態互動元件（`ResizeHandle`、`OperationStatus`、
   `InlineNotice`、`EmptyState`），`touch.ts` 放觸控／指標能力
   （`useTouchContextMenu` 讓長按等同右鍵，全 app 生效；`useTouchCapable`、
   `useCoarsePointer`）。`NodeLinkPicker.tsx` 是同一類：`Drawer`、`drawer/`、
   `inspector/` 三邊都要，所以它在扁平層。
3. **只有一個父元件會用嗎？** → 進那個父元件的資料夾。
   一個資料夾**只能有一個擁有者**。若第二個功能開始匯入其中的檔案，不要加第二個
   擁有者：把那個檔案往上搬到扁平層，或抽成共用原語。
4. **門檻**：某個扁平元件的私有檔案累積到 3～4 個，就替它開資料夾，
   父元件留在扁平層。反過來，資料夾只剩一個檔案就攤回去。

尚未跨過門檻、因此仍在扁平層的私有檔：`BoardToolbar.tsx`、`GraphBatchToolbar.tsx`、
`GraphToolsPanel.tsx`（三者只有 `LaneView` 用）、`DriveCredentialsPanel.tsx`
（只有 `BackupModal` 用）。它們不是共用原語，只是還不值得一個資料夾。

## 4. 測試

測試與受測檔**同層**，`*.test.ts` / `*.test.tsx` 緊鄰原始檔，不另開 `__tests__/`。
整個 `src/` 都是這個慣例（`state/graphSlice.test.ts`、`domain/commands.test.ts`…），
`components/` 沒有例外：`canvas/useCardDrag.test.tsx` 在 `canvas/`，
`explorer/sortProjects.test.ts` 在 `explorer/`，`LaneView.test.tsx` 在扁平層。

一個檔案可以有多份測試，用中綴分主題：`Drawer.sheet.test.tsx` 測的是 `Drawer.tsx`
的 sheet 行為。`drawer/`、`lane/`、`timeline/` 目前沒有自己的測試，覆蓋來自
`LaneView.test.tsx` 與 `Drawer.sheet.test.tsx`。
