import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import {
  IconBell,
  IconDoc,
  IconGear,
  IconLayout,
  IconMoon,
  IconPlus,
  IconRestore,
  IconSun,
  IconTree,
} from "../icons";
import { reportError, useApp, usePreference } from "../store";

type SearchResult = Awaited<ReturnType<typeof api.search>>["results"][number];

type Command = {
  key: string;
  title: string;
  detail: string;
  /** Extra words the query may match; the title alone is often not enough. */
  keywords?: string;
  hint?: string;
  icon?: React.ReactNode;
  run: () => Promise<void> | void;
};

type PaletteItem =
  | ({ kind: "action"; group: string } & Command)
  | {
      key: string;
      kind: "result";
      group: string;
      resultKind: SearchResult["kind"];
      project: string;
      nodeId?: string;
      title: string;
      snippet?: string;
    };

/** Ctrl on Windows/Linux, ⌘ on macOS — the palette says which one it means. */
const MOD =
  typeof navigator !== "undefined" && /mac|iphone|ipad/i.test(navigator.platform || "")
    ? "⌘"
    : "Ctrl";

const HELP_SECTIONS: { title: string; rows: { keys: string[]; text: string }[] }[] = [
  {
    title: "全域",
    rows: [
      { keys: [`${MOD} K`], text: "開啟或關閉這個面板" },
      { keys: [`${MOD} S`], text: "儲存目前分頁" },
      { keys: [`${MOD} Z`], text: "復原上一步（游標不在編輯器內時）" },
      { keys: [`${MOD} ⇧ Z`, `${MOD} Y`], text: "重做剛剛復原的那一步" },
      { keys: ["Esc"], text: "關閉面板、對話框或選單" },
    ],
  },
  {
    title: "這個面板",
    rows: [
      { keys: ["↑", "↓"], text: "移動選取" },
      { keys: ["Enter"], text: "執行命令或開啟結果" },
      { keys: ["?"], text: "空白輸入時按下即開啟操作說明" },
      { keys: ["2 字以上"], text: "同時比對命令與全工作區內容" },
    ],
  },
  {
    title: "關係圖與專案總管",
    rows: [
      { keys: [`${MOD} 拖曳`], text: "在關係圖上框選多個節點" },
      { keys: [`${MOD} 點擊`], text: "在專案總管加選或取消單一項目" },
      { keys: ["拖曳分隔線"], text: "調整寬高；雙擊還原預設比例" },
    ],
  },
];

const USAGE_STEPS = [
  "左側專案總管管理工作區、專案與節點；右鍵可重新命名、移動、匯出封裝。",
  "節點內容是 Markdown，雙擊節點開啟編輯抽屜，可彈出成獨立視窗。",
  "關係圖畫依賴，時間軸排里程碑與實際事件；兩者都可從這裡收合。",
  "匯出支援 Markdown、PDF、單一專案 .veproj，以及整個工作區的多專案封裝。",
];

export function CommandPalette() {
  const switchProject = useApp((state) => state.switchProject);
  const openTab = useApp((state) => state.openTab);
  const createNode = useApp((state) => state.createNode);
  const togglePane = useApp((state) => state.togglePane);
  const toggleTheme = useApp((state) => state.toggleTheme);
  const updateUIPreference = useApp((state) => state.updateUIPreference);
  const saveTab = useApp((state) => state.saveTab);
  const saveAllTabs = useApp((state) => state.saveAllTabs);
  const undoLast = useApp((state) => state.undoLast);
  const redoLast = useApp((state) => state.redoLast);
  const loadAll = useApp((state) => state.loadAll);
  const refreshProjects = useApp((state) => state.refreshProjects);
  const refreshTrash = useApp((state) => state.refreshTrash);
  const activeProject = useApp((state) => state.activeProject);
  const activeTab = useApp((state) => state.activeTab);
  const theme = useApp((state) => state.theme);
  const paneOpen = useApp((state) => state.paneOpen);
  const explorerCollapsed = usePreference("explorerCollapsed");
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<"list" | "help">("list");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const onOpen = () => setOpen(true);
    const onKey = (event: KeyboardEvent) => {
      if (
        (event.ctrlKey || event.metaKey) &&
        !event.altKey &&
        event.key.toLowerCase() === "k"
      ) {
        event.preventDefault();
        setOpen((value) => !value);
      }
    };
    window.addEventListener("nodevas-command-palette", onOpen);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("nodevas-command-palette", onOpen);
      window.removeEventListener("keydown", onKey);
    };
  }, []);

  useEffect(() => {
    if (!open) {
      setQuery("");
      setResults([]);
      setSelected(0);
      setView("list");
      return;
    }
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [open]);

  // The help view has no focused input, so Escape needs a listener of its own.
  useEffect(() => {
    if (!open || view !== "help") return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, view]);

  useEffect(() => {
    const normalized = query.trim();
    if (normalized.length < 2) {
      setResults([]);
      setLoading(false);
      return;
    }
    let cancelled = false;
    const timer = window.setTimeout(() => {
      setLoading(true);
      void api
        .search(normalized)
        .then((response) => {
          if (!cancelled) setResults(response.results);
        })
        .catch((error) => {
          if (!cancelled) reportError(error);
        })
        .finally(() => {
          if (!cancelled) setLoading(false);
        });
    }, 180);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [query]);

  const commands = useMemo<Command[]>(() => {
    const list: Command[] = [
      {
        key: "new-node",
        title: "新增節點",
        detail: `建立於 ${activeProject || "目前專案"}`,
        keywords: "node create add 建立",
        icon: <IconPlus size={15} />,
        run: async () => {
          await createNode({ title: "未命名節點", kind: "task" });
        },
      },
      {
        key: "toggle-graph",
        title: paneOpen.graph ? "收合關係圖" : "顯示關係圖",
        detail: "切換上方的關係圖區塊",
        keywords: "graph 關係圖 圖表 切換",
        icon: <IconTree size={15} />,
        run: () => togglePane("graph"),
      },
      {
        key: "toggle-timeline",
        title: paneOpen.timeline ? "收合時間軸" : "顯示時間軸",
        detail: "切換下方的時間軸區塊",
        keywords: "timeline 時間軸 里程碑 切換",
        icon: <IconLayout size={15} />,
        run: () => togglePane("timeline"),
      },
      {
        key: "toggle-explorer",
        title: explorerCollapsed ? "展開專案總管" : "摺疊專案總管",
        detail: "切換左側側邊欄",
        keywords: "sidebar explorer 側邊欄 專案總管",
        icon: <IconLayout size={15} />,
        run: () => updateUIPreference("explorerCollapsed", !explorerCollapsed),
      },
      {
        key: "toggle-theme",
        title: theme === "dark" ? "切換淺色模式" : "切換深色模式",
        detail: "外觀主題",
        keywords: "theme dark light 主題 深色 淺色",
        icon: theme === "dark" ? <IconSun size={15} /> : <IconMoon size={15} />,
        run: () => toggleTheme(),
      },
      {
        key: "project-settings",
        title: "專案設定",
        detail: "工作流程狀態、里程碑類型、成員、外觀",
        keywords: "settings 設定 狀態 成員",
        icon: <IconGear size={15} />,
        run: () => window.dispatchEvent(new Event("nodevas-project-settings")),
      },
      {
        key: "notify-settings",
        title: "截止提醒設定",
        detail: "郵件通知與提醒時間",
        keywords: "notify mail 提醒 通知 郵件",
        icon: <IconBell size={15} />,
        run: () => window.dispatchEvent(new Event("nodevas-notify-settings")),
      },
    ];
    if (activeTab) {
      list.push(
        {
          key: "save-tab",
          title: "儲存目前分頁",
          detail: "寫回 Markdown 檔案",
          keywords: "save 儲存",
          hint: `${MOD} S`,
          icon: <IconDoc size={15} />,
          run: async () => {
            await saveTab(activeTab);
          },
        },
        {
          key: "save-all",
          title: "儲存所有分頁",
          detail: "一次寫回全部未存檔的分頁",
          keywords: "save all 全部 儲存",
          icon: <IconDoc size={15} />,
          run: async () => {
            await saveAllTabs();
          },
        },
      );
    }
    list.push(
      {
        key: "undo",
        title: "復原上一步",
        detail: "還原最近一次結構變更",
        keywords: "undo 復原 還原",
        hint: `${MOD} Z`,
        icon: <IconRestore size={15} />,
        run: async () => {
          await undoLast();
        },
      },
      {
        key: "redo",
        title: "重做上一步",
        detail: "再次套用剛剛復原的結構變更",
        keywords: "redo 重做 重新",
        hint: `${MOD} ⇧ Z`,
        icon: <IconRestore size={15} />,
        run: async () => {
          await redoLast();
        },
      },
      {
        key: "reload",
        title: "重新載入資料",
        detail: "重新讀取專案、節點與垃圾桶",
        keywords: "reload refresh 重新整理 同步",
        icon: <IconRestore size={15} />,
        run: async () => {
          await Promise.all([loadAll(), refreshProjects(), refreshTrash()]);
        },
      },
      {
        key: "help",
        title: "快捷鍵與操作說明",
        detail: "所有快速鍵與基本流程",
        keywords: "help shortcut keys 說明 幫助 快捷鍵 教學",
        hint: "?",
        icon: <IconDoc size={15} />,
        run: () => setView("help"),
      },
      {
        key: "tour",
        title: "重新播放導覽",
        detail: "重新選擇並播放各區塊導覽",
        keywords: "tour onboarding guide 導覽 教學 新手 引導",
        icon: <IconDoc size={15} />,
        run: () => {
          window.dispatchEvent(new CustomEvent("nodevas-tour-start"));
        },
      },
    );
    return list;
  }, [
    activeProject,
    activeTab,
    createNode,
    explorerCollapsed,
    loadAll,
    paneOpen.graph,
    paneOpen.timeline,
    refreshProjects,
    redoLast,
    refreshTrash,
    saveAllTabs,
    saveTab,
    theme,
    toggleTheme,
    togglePane,
    undoLast,
    updateUIPreference,
  ]);

  const normalized = query.trim().toLowerCase();
  // Commands stay reachable while typing: a query filters them instead of
  // replacing them with search results, which used to hide every command
  // after the second character.
  const matchedCommands = normalized
    ? commands.filter((command) =>
        `${command.title} ${command.detail} ${command.keywords ?? ""}`
          .toLowerCase()
          .includes(normalized),
      )
    : commands;
  const items: PaletteItem[] = [
    ...matchedCommands.map((command) => ({
      ...command,
      kind: "action" as const,
      group: normalized ? "命令" : "常用命令",
    })),
    ...results.map((result, index) => ({
      key: `${result.project}:${result.nodeId ?? ""}:${index}`,
      kind: "result" as const,
      group: "搜尋結果",
      resultKind: result.kind,
      project: result.project,
      nodeId: result.nodeId,
      title: result.title,
      snippet: result.snippet,
    })),
  ];

  useEffect(() => setSelected(0), [query, results.length]);

  const choose = async (item: PaletteItem | undefined) => {
    if (!item) return;
    if (item.kind === "action") {
      // The help view replaces the list in place; closing first would make the
      // palette blink shut and reopen.
      if (item.key !== "help") setOpen(false);
      await item.run();
      return;
    }
    setOpen(false);
    if (item.project !== activeProject) await switchProject(item.project);
    if (item.nodeId) await openTab(item.nodeId);
  };

  if (!open) return null;
  return (
    <div className="command-palette-backdrop" onPointerDown={() => setOpen(false)}>
      <section
        className="command-palette"
        role="dialog"
        aria-modal="true"
        aria-label="全域搜尋與命令"
        onPointerDown={(event) => event.stopPropagation()}
      >
        {view === "help" ? (
          <>
            <div className="command-palette-help-head">
              <b>快捷鍵與操作說明</b>
              <button type="button" onClick={() => setView("list")}>
                返回命令
              </button>
            </div>
            <div className="command-palette-help">
              {HELP_SECTIONS.map((section) => (
                <section key={section.title}>
                  <h3>{section.title}</h3>
                  <dl>
                    {section.rows.map((row) => (
                      <div key={row.text}>
                        <dt>
                          {row.keys.map((key) => (
                            <kbd key={key}>{key}</kbd>
                          ))}
                        </dt>
                        <dd>{row.text}</dd>
                      </div>
                    ))}
                  </dl>
                </section>
              ))}
              <section>
                <h3>基本流程</h3>
                <ol>
                  {USAGE_STEPS.map((step) => (
                    <li key={step}>{step}</li>
                  ))}
                </ol>
              </section>
            </div>
            <footer>
              <span>
                <kbd>Esc</kbd> 關閉
              </span>
              <span>
                <kbd>Ctrl/⌘ K</kbd> 切換
              </span>
            </footer>
          </>
        ) : (
          <>
            <div className="command-palette-input">
              <span aria-hidden>⌕</span>
              <input
                ref={inputRef}
                value={query}
                placeholder="搜尋所有專案、節點、Markdown；或執行命令（? 看說明）"
                aria-label="搜尋"
                onChange={(event) => setQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Escape") {
                    event.preventDefault();
                    setOpen(false);
                  }
                  if (event.key === "?" && query.length === 0) {
                    event.preventDefault();
                    setView("help");
                  }
                  if (event.key === "ArrowDown") {
                    event.preventDefault();
                    setSelected((value) => Math.min(items.length - 1, value + 1));
                  }
                  if (event.key === "ArrowUp") {
                    event.preventDefault();
                    setSelected((value) => Math.max(0, value - 1));
                  }
                  if (event.key === "Enter") {
                    event.preventDefault();
                    void choose(items[selected]).catch(reportError);
                  }
                }}
              />
              <kbd>Esc</kbd>
            </div>
            <div className="command-palette-results" role="listbox">
              {items.map((item, index) => (
                <div key={item.key}>
                  {(index === 0 || items[index - 1].group !== item.group) && (
                    <div className="command-palette-group" role="presentation">
                      {item.group}
                    </div>
                  )}
                  <button
                    type="button"
                    role="option"
                    aria-selected={index === selected}
                    className={index === selected ? "selected" : ""}
                    onPointerMove={() => setSelected(index)}
                    onClick={() => void choose(item).catch(reportError)}
                  >
                    <span className="command-palette-icon">
                      {item.kind === "action" ? (
                        (item.icon ?? <IconTree size={15} />)
                      ) : item.nodeId ? (
                        <IconDoc size={15} />
                      ) : (
                        <IconTree size={15} />
                      )}
                    </span>
                    <span>
                      <b>{item.title || ("nodeId" in item ? item.nodeId : "")}</b>
                      <small>
                        {item.kind === "action"
                          ? item.detail
                          : `${item.project}${item.snippet ? ` · ${item.snippet}` : ""}`}
                      </small>
                    </span>
                    {item.kind === "action" && item.hint && <kbd>{item.hint}</kbd>}
                  </button>
                </div>
              ))}
              {loading && <div className="command-palette-empty">搜尋中…</div>}
              {!loading && normalized.length >= 2 && items.length === 0 && (
                <div className="command-palette-empty">找不到符合的命令或內容</div>
              )}
            </div>
            <footer>
              <span>
                <kbd>↑</kbd>
                <kbd>↓</kbd> 選擇
              </span>
              <span>
                <kbd>Enter</kbd> 開啟
              </span>
              <span>
                <kbd>?</kbd> 說明
              </span>
              <span>
                <kbd>Ctrl/⌘ K</kbd> 切換
              </span>
            </footer>
          </>
        )}
      </section>
    </div>
  );
}
