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
import { useI18n } from "../i18n";

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
    title: "palette.help.global",
    rows: [
      { keys: [`${MOD} K`], text: "palette.help.open" },
      { keys: [`${MOD} S`], text: "palette.help.save" },
      { keys: [`${MOD} Z`], text: "palette.help.undo" },
      { keys: [`${MOD} ⇧ Z`, `${MOD} Y`], text: "palette.help.redo" },
      { keys: ["Esc"], text: "palette.help.close" },
    ],
  },
  {
    title: "palette.help.palette",
    rows: [
      { keys: ["↑", "↓"], text: "palette.help.move" },
      { keys: ["Enter"], text: "palette.help.run" },
      { keys: ["?"], text: "palette.help.help" },
      { keys: ["palette.key.twoChars"], text: "palette.help.search" },
    ],
  },
  {
    title: "palette.help.graph",
    rows: [
      { keys: [`${MOD} palette.key.drag`], text: "palette.help.selectNodes" },
      { keys: [`${MOD} palette.key.click`], text: "palette.help.selectItems" },
      { keys: ["palette.key.divider"], text: "palette.help.resize" },
    ],
  },
];

const USAGE_STEPS = [
  "palette.usage.explorer",
  "palette.usage.editor",
  "palette.usage.views",
  "palette.usage.export",
];

export function CommandPalette() {
  const { t } = useI18n();
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
        title: t("palette.newNode"),
        detail: t("palette.createIn", { project: activeProject || t("topbar.project") }),
        keywords: "node create add 建立",
        icon: <IconPlus size={15} />,
        run: async () => {
          await createNode({ title: t("palette.untitledNode"), kind: "task" });
        },
      },
      {
        key: "toggle-graph",
        title: paneOpen.graph ? t("palette.hideGraph") : t("palette.showGraph"),
        detail: t("palette.toggleGraph"),
        keywords: "graph 關係圖 圖表 切換",
        icon: <IconTree size={15} />,
        run: () => togglePane("graph"),
      },
      {
        key: "toggle-timeline",
        title: paneOpen.timeline ? t("palette.hideTimeline") : t("palette.showTimeline"),
        detail: t("palette.toggleTimeline"),
        keywords: "timeline 時間軸 里程碑 切換",
        icon: <IconLayout size={15} />,
        run: () => togglePane("timeline"),
      },
      {
        key: "toggle-explorer",
        title: explorerCollapsed ? t("palette.expandExplorer") : t("palette.collapseExplorer"),
        detail: t("palette.toggleExplorer"),
        keywords: "sidebar explorer 側邊欄 專案總管",
        icon: <IconLayout size={15} />,
        run: () => updateUIPreference("explorerCollapsed", !explorerCollapsed),
      },
      {
        key: "toggle-theme",
        title: theme === "dark" ? t("topbar.lightMode") : t("topbar.darkMode"),
        detail: t("palette.themeDetail"),
        keywords: "theme dark light 主題 深色 淺色",
        icon: theme === "dark" ? <IconSun size={15} /> : <IconMoon size={15} />,
        run: () => toggleTheme(),
      },
      {
        key: "project-settings",
        title: t("palette.settings"),
        detail: t("palette.settingsDetail"),
        keywords: "settings 設定 狀態 成員",
        icon: <IconGear size={15} />,
        run: () => window.dispatchEvent(new Event("nodevas-project-settings")),
      },
      {
        key: "notify-settings",
        title: t("palette.notify"),
        detail: t("palette.notifyDetail"),
        keywords: "notify mail 提醒 通知 郵件",
        icon: <IconBell size={15} />,
        run: () => window.dispatchEvent(new Event("nodevas-notify-settings")),
      },
    ];
    if (activeTab) {
      list.push(
        {
          key: "save-tab",
          title: t("palette.saveTab"),
          detail: t("palette.saveTabDetail"),
          keywords: "save 儲存",
          hint: `${MOD} S`,
          icon: <IconDoc size={15} />,
          run: async () => {
            await saveTab(activeTab);
          },
        },
        {
          key: "save-all",
          title: t("palette.saveAll"),
          detail: t("palette.saveAllDetail"),
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
        title: t("palette.undo"),
        detail: t("palette.undoDetail"),
        keywords: "undo 復原 還原",
        hint: `${MOD} Z`,
        icon: <IconRestore size={15} />,
        run: async () => {
          await undoLast();
        },
      },
      {
        key: "redo",
        title: t("palette.redo"),
        detail: t("palette.redoDetail"),
        keywords: "redo 重做 重新",
        hint: `${MOD} ⇧ Z`,
        icon: <IconRestore size={15} />,
        run: async () => {
          await redoLast();
        },
      },
      {
        key: "reload",
        title: t("palette.reload"),
        detail: t("palette.reloadDetail"),
        keywords: "reload refresh 重新整理 同步",
        icon: <IconRestore size={15} />,
        run: async () => {
          await Promise.all([loadAll(), refreshProjects(), refreshTrash()]);
        },
      },
      {
        key: "help",
        title: t("palette.helpTitle"),
        detail: t("palette.helpDetail"),
        keywords: "help shortcut keys 說明 幫助 快捷鍵 教學",
        hint: "?",
        icon: <IconDoc size={15} />,
        run: () => setView("help"),
      },
      {
        key: "tour",
        title: t("palette.tour"),
        detail: t("palette.tourDetail"),
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
    t,
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
      group: normalized ? t("palette.command") : t("palette.commands"),
    })),
    ...results.map((result, index) => ({
      key: `${result.project}:${result.nodeId ?? ""}:${index}`,
      kind: "result" as const,
      group: t("palette.searchResults"),
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
        aria-label={t("topbar.searchOrCommand")}
        onPointerDown={(event) => event.stopPropagation()}
      >
        {view === "help" ? (
          <>
            <div className="command-palette-help-head">
              <b>{t("palette.helpTitle")}</b>
              <button type="button" onClick={() => setView("list")}>
                {t("palette.returnToCommands")}
              </button>
            </div>
            <div className="command-palette-help">
              {HELP_SECTIONS.map((section) => (
                <section key={section.title}>
                  <h3>{t(section.title)}</h3>
                  <dl>
                    {section.rows.map((row) => (
                      <div key={row.text}>
                        <dt>
                          {row.keys.map((key) => (
                            <kbd key={key}>
                              {key.replace(/palette\.[\w.-]+/g, (token) => t(token))}
                            </kbd>
                          ))}
                        </dt>
                        <dd>{t(row.text)}</dd>
                      </div>
                    ))}
                  </dl>
                </section>
              ))}
              <section>
                <h3>{t("palette.basicWorkflow")}</h3>
                <ol>
                  {USAGE_STEPS.map((step) => (
                    <li key={step}>{t(step)}</li>
                  ))}
                </ol>
              </section>
            </div>
            <footer>
              <span>
                <kbd>Esc</kbd> {t("palette.close")}
              </span>
              <span>
                <kbd>Ctrl/⌘ K</kbd> {t("palette.toggle")}
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
                placeholder={t("palette.searchPlaceholder")}
                aria-label={t("palette.search")}
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
              {loading && <div className="command-palette-empty">{t("palette.searching")}</div>}
              {!loading && normalized.length >= 2 && items.length === 0 && (
                <div className="command-palette-empty">{t("palette.noResults")}</div>
              )}
            </div>
            <footer>
              <span>
                <kbd>↑</kbd>
                <kbd>↓</kbd> {t("palette.select")}
              </span>
              <span>
                <kbd>Enter</kbd> {t("palette.open")}
              </span>
              <span>
                <kbd>?</kbd> {t("palette.helpShortcut")}
              </span>
              <span>
                <kbd>Ctrl/⌘ K</kbd> {t("palette.toggle")}
              </span>
            </footer>
          </>
        )}
      </section>
    </div>
  );
}
