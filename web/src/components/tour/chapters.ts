/**
 * Onboarding tour chapters [zh-TW].
 *
 * The tour is no longer one long linear walk: each chapter is a short 2-4
 * step spotlight sequence that plays the first time the user actually enters
 * that area (pointerdown on the region, or the drawer opening). Steps whose
 * target is missing or has zero size are skipped at runtime, so chapters can
 * safely mention UI that only exists in some layouts.
 */

export interface TourStep {
  id: string;
  /** CSS selector of the element to spotlight. Omit for a centered card. */
  target?: string;
  title: string;
  body: string;
  placement?: "top" | "bottom" | "left" | "right" | "center";
}

export type TourTrigger =
  | "manual"
  | { kind: "pointerdown"; selector: string }
  | { kind: "drawer-open" };

export interface TourChapter {
  id: string;
  trigger: TourTrigger;
  steps: TourStep[];
}

export const TOUR_CHAPTERS: TourChapter[] = [
  {
    // Played right after「開始導覽」; never auto-triggers contextually.
    id: "shell",
    trigger: "manual",
    steps: [
      {
        id: "shell-explorer",
        target: "#project-explorer",
        title: "專案總管",
        body: "在這裡切換工作區、建立專案,也能匯入或匯出資料。",
        placement: "right",
      },
      {
        id: "shell-command",
        target: ".command-trigger",
        title: "指令面板",
        body: "按 Ctrl/⌘ K 開啟,所有操作都搜得到。",
        placement: "bottom",
      },
      {
        id: "shell-replay",
        target: '[data-tour="tour-replay"]',
        title: "隨時重看",
        body: "右上角這顆按鈕隨時可以重播導覽,旁邊依序是設定、通知、備份與主題。接下來點進各區塊,會有對應的簡短教學。",
        placement: "bottom",
      },
    ],
  },
  {
    id: "graph",
    trigger: { kind: "pointerdown", selector: ".lane-wrap.graph" },
    steps: [
      {
        id: "graph-create",
        target: ".board-primary-action",
        title: "建立節點",
        body: "點這顆「新增節點」,或在畫布空白處按右鍵。",
        placement: "bottom",
      },
      {
        id: "graph-card",
        target: ".lane-wrap.graph",
        title: "節點卡片",
        body: "點一下開啟編輯,按住拖曳移動位置;選取後按 Enter 也能開啟。",
        placement: "bottom",
      },
      {
        id: "graph-link",
        target: ".lane-wrap.graph",
        title: "建立依賴",
        body: "按住 Alt 從一張卡片拖到另一張,放開即連線;卡片右鍵選單也能加入前置節點。",
        placement: "bottom",
      },
      {
        id: "graph-batch",
        target: ".lane-wrap.graph",
        title: "批次操作",
        body: "Ctrl/⌘＋拖曳可框選多個節點,Del 刪除、Ctrl/⌘ C/X/V 複製剪貼。空白處按右鍵,還能加入 AND/OR 邏輯閘。",
        placement: "bottom",
      },
    ],
  },
  {
    id: "timeline",
    trigger: { kind: "pointerdown", selector: ".lane-wrap.timeline" },
    steps: [
      {
        id: "timeline-cells",
        target: ".lane-wrap.timeline",
        title: "時間軸",
        body: "每一列是一個日期。在儲存格上按右鍵,設定開始、死線或自訂里程碑。",
        placement: "top",
      },
      {
        id: "timeline-drag",
        target: ".lane-wrap.timeline",
        title: "直接改期",
        body: "預期與實際的卡片都能直接拖曳到別的日期。",
        placement: "top",
      },
      {
        id: "timeline-orientation",
        target: ".timeline-orientation",
        title: "檢視切換",
        body: "這裡切換直式或橫式;格子大小也能在工具列調整。",
        placement: "bottom",
      },
    ],
  },
  {
    id: "drawer",
    trigger: { kind: "drawer-open" },
    steps: [
      {
        id: "drawer-panel",
        target: ".drawer",
        title: "節點編輯面板",
        body: "上方是文件分頁,可以釘選,也能拖曳調整面板寬度。",
        placement: "left",
      },
      {
        id: "drawer-views",
        target: ".node-view-tabs",
        title: "四種檢視",
        body: "內容(Markdown 編輯)、關係圖、時間軸、外觀(卡片形狀與顏色)。",
        placement: "left",
      },
      {
        id: "drawer-save",
        target: ".drawer",
        title: "狀態與儲存",
        body: "狀態與筆記在上方的生命週期區塊調整,Ctrl/⌘ S 儲存全部變更。",
        placement: "left",
      },
    ],
  },
  {
    id: "explorer",
    trigger: { kind: "pointerdown", selector: "#project-explorer" },
    steps: [
      {
        id: "explorer-tree",
        target: "#project-explorer",
        title: "專案樹",
        body: "點專案即可切換;「新專案」與子專案都從上方的按鈕建立。",
        placement: "right",
      },
      {
        id: "explorer-transfer",
        target: "#project-explorer",
        title: "匯入與匯出",
        body: "專案封裝、Markdown 都能匯入;儲存、另存新檔與匯出也都在這裡。",
        placement: "right",
      },
    ],
  },
];

export function chapterById(id: string): TourChapter | undefined {
  return TOUR_CHAPTERS.find((chapter) => chapter.id === id);
}
