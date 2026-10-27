import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import {
  IconBell,
  IconClock,
  IconCompass,
  IconDoc,
  IconFolderOpen,
  IconGear,
  IconHelp,
  IconMoon,
  IconPlus,
  IconRedo,
  IconRefresh,
  IconSave,
  IconSaveAll,
  IconSidebar,
  IconSpark,
  IconSun,
  IconTree,
  IconUndo,
  IconWarning,
} from "../icons";
import { reportError, useApp, usePreference } from "../store";
import { useI18n } from "../i18n";
import { nodeTypeLabel, useNodeTypes } from "../nodeTypeTheme";
import { useWorkstationInUse, useWorkstationVisible } from "../runners/workstation";
import { packName, usePacks } from "../game/packStore";
import {
  setProjectMode,
  setStageSkin,
  setStationPack,
  STAGE_SKIN_LABEL,
  useStageSkinChoices,
  useStationView,
} from "../game/stageSkin";
import { openSettings, type SettingsSection } from "../settings/openSettings";
import { useControl } from "../station/controlStore";
import { confirmAction } from "./ConfirmDialog";
import { OPEN_START_SCREEN_EVENT } from "./StartScreen";
import { useTeamServer, useViewer } from "./SignIn";
import { COMMAND_PALETTE_EVENT, replayTour, type CommandPaletteView } from "./topbar/actions";

/**
 * Station's emergency stop, from the palette: confirmed, then the same
 * pause-all Station sends, leaving running runs running (Station's own dialog
 * can cancel those too). Station opens to show what it did.
 */
async function pauseAllAgents(t: (key: string, values?: Record<string, string | number>) => string) {
  const confirmed = await confirmAction({
    title: t("station.control.pauseAllTitle"),
    description: `${t("station.control.pauseAllIntro")} ${t("topbar.cmd.pauseAllKeepsRunning")}`,
    confirmLabel: t("station.control.pauseAll"),
    tone: "danger",
  });
  if (!confirmed) return;
  setProjectMode("station");
  await useControl.getState().pauseAll(false);
}

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
  const { language, t } = useI18n();
  const switchProject = useApp((state) => state.switchProject);
  const openTab = useApp((state) => state.openTab);
  const createNode = useApp((state) => state.createNode);
  const nodeTypes = useNodeTypes();
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
  const snapToGrid = usePreference("graphSnapToGrid");
  const snapGuides = usePreference("graphSnapGuides");
  const minimap = usePreference("graphMinimap");
  const viewer = useViewer();
  const canCreateProjects = !viewer || viewer.role === "admin";
  const workstation = useWorkstationVisible();
  const workstationInUse = useWorkstationInUse();
  const stationView = useStationView();
  const projectMode = usePreference("projectMode");
  const skinChoices = useStageSkinChoices();
  const packs = usePacks((s) => s.packs);
  const stationPack = usePreference("stationPack");
  const projects = useApp((state) => state.projects);
  const workspaces = useApp((state) => state.workspaces);
  const switchWorkspace = useApp((state) => state.switchWorkspace);
  const teamServer = useTeamServer();
  // The parts of Settings without a command of their own above, offered as
  // Settings offers them (SettingsView's PARTS): the Agents ones only with the
  // workstation shown, the server ones only to who may see them.
  const settingsParts = useMemo<SettingsSection[]>(() => {
    const parts: SettingsSection[] = ["appearance", "backup", "copilot", "workstation"];
    if (teamServer && viewer) parts.push("account");
    if (teamServer && viewer?.role === "admin") parts.push("server", "audit");
    return parts;
  }, [teamServer, viewer]);
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<"list" | "help">("list");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // Settings' "Help and shortcuts" opens straight on the help view.
    const onOpen = (event: Event) => {
      const detail = (event as CustomEvent<{ view?: CommandPaletteView } | null>).detail;
      if (detail?.view === "help") setView("help");
      setOpen(true);
    };
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
    window.addEventListener(COMMAND_PALETTE_EVENT, onOpen);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener(COMMAND_PALETTE_EVENT, onOpen);
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
      // One per type, so "new review" or a custom type's name finds it.
      ...nodeTypes.map<Command>((type) => {
        const label = nodeTypeLabel(type, t);
        return {
          key: `new-node-${type.id}`,
          title: t("palette.newNodeOfType", { type: label }),
          detail: t("palette.createIn", { project: activeProject || t("topbar.project") }),
          keywords: `node create add type ${type.id} ${type.label} ${label} 建立 類型`,
          icon: <IconPlus size={15} />,
          run: async () => {
            await createNode({ title: t("palette.untitledNode"), kind: type.id });
          },
        };
      }),
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
        icon: <IconClock size={15} />,
        run: () => togglePane("timeline"),
      },
      {
        key: "toggle-explorer",
        title: explorerCollapsed ? t("palette.expandExplorer") : t("palette.collapseExplorer"),
        detail: t("palette.toggleExplorer"),
        keywords: "sidebar explorer 側邊欄 專案總管",
        icon: <IconSidebar size={15} />,
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
      ...(projectMode !== "settings"
        ? [
            {
              key: "open-settings",
              title: t("topbar.cmd.openSettings"),
              detail: t("mode.settings.detail"),
              keywords: "settings preferences options 設定 偏好 選項",
              icon: <IconGear size={15} />,
              run: () => setProjectMode("settings"),
            },
          ]
        : []),
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
      // The graph header keeps these behind its ⋯ menu; they are settings,
      // so they can be flipped from here without finding the board first.
      {
        key: "toggle-snap-grid",
        title: t("board.snapGrid"),
        detail: t("board.snapGridTitle"),
        keywords: "snap grid 格線 吸附",
        hint: snapToGrid ? "✓" : undefined,
        run: () => updateUIPreference("graphSnapToGrid", !snapToGrid),
      },
      {
        key: "toggle-snap-guides",
        title: t("board.alignGuides"),
        detail: t("board.alignGuidesTitle"),
        keywords: "align guides snap 對齊 輔助線",
        hint: snapGuides ? "✓" : undefined,
        run: () => updateUIPreference("graphSnapGuides", !snapGuides),
      },
      {
        key: "toggle-minimap",
        title: t("board.minimap"),
        detail: t("board.minimapTitle"),
        keywords: "minimap overview 縮圖 小地圖",
        hint: minimap ? "✓" : undefined,
        run: () => updateUIPreference("graphMinimap", !minimap),
      },
      ...(canCreateProjects
        ? [
      {
        key: "open-example",
        title: t("palette.openExample"),
        detail: t("palette.openExampleDetail"),
        keywords: "example demo sample template 範例 示範 開始",
        icon: <IconCompass size={15} />,
        run: () => void window.dispatchEvent(new Event(OPEN_START_SCREEN_EVENT)),
      },
          ]
        : []),
    ];
    // Board and Station, and Station's views; what is showing is not offered
    // again.
    if (projectMode !== "info") {
      list.push({
        key: "project-mode-info",
        title: t("topbar.cmd.openBoard"),
        detail: t("mode.info.detail"),
        keywords: "info board graph timeline cards 資訊 看板 圖 時間軸 卡片",
        icon: <IconTree size={15} />,
        run: () => setProjectMode("info"),
      });
    }
    if (projectMode !== "station") {
      list.push({
        key: "open-station",
        title: t("topbar.cmd.openStation"),
        detail: t("mode.station.detail"),
        keywords: "station agents waiting runs today 工作站 等你 執行",
        icon: <IconSpark size={15} />,
        run: () => setProjectMode("station"),
      });
    }
    for (const skin of skinChoices) {
      if (projectMode === "station" && !stationPack && skin === stationView) continue;
      list.push({
        key: `stage-skin-${skin}`,
        title: t("game.menu.openInStation", { name: t(STAGE_SKIN_LABEL[skin]) }),
        detail: t(`game.palette.${skin}`),
        keywords: "station agents view skin game war room boss dungeon 工作站 檢視 遊戲 戰情室 地城 頭目",
        icon: <IconTree size={15} />,
        run: () => setStageSkin(skin),
      });
    }
    for (const pack of packs) {
      if (projectMode === "station" && stationPack === pack.id) continue;
      list.push({
        key: `view-pack-${pack.id}`,
        title: t("game.menu.openInStation", { name: packName(pack, language) }),
        detail: t("packs.paletteDetail"),
        keywords: `station pack view skin ${pack.id} 工作站 主題包`,
        icon: <IconTree size={15} />,
        run: () => setStationPack(pack),
      });
    }
    // The top bar's Runners and Usage icons went into its runner pill; these
    // reach their pages, and the agent controls with them, by name.
    if (workstation) {
      list.push(
        {
          key: "runners",
          title: t("topbar.runners"),
          detail: t("palette.runnersDetail"),
          keywords: "runner agent machine 執行器 agent",
          run: () => openSettings("runners"),
        },
        {
          key: "add-runner",
          title: t("topbar.cmd.addRunner"),
          detail: t("topbar.cmd.addRunnerDetail"),
          keywords: "add new pair runner machine device 新增 配對 執行器 電腦",
          icon: <IconPlus size={15} />,
          run: () => openSettings("runners", { add: true }),
        },
        {
          key: "usage",
          title: t("topbar.usage"),
          detail: t("palette.usageDetail"),
          keywords: "usage tokens cost spend today 用量 費用 花費 今天",
          run: () => openSettings("usage"),
        },
        {
          key: "budget",
          title: t("topbar.cmd.budget"),
          detail: t("topbar.cmd.budgetDetail"),
          keywords: "budget daily limit cost spend 預算 每日 上限 花費",
          run: () => openSettings("budget"),
        },
        {
          key: "routing",
          title: t("topbar.cmd.routing"),
          detail: t("topbar.cmd.routingDetail"),
          keywords: "routing rules route agent model 路由 規則 分派",
          run: () => openSettings("routing"),
        },
        {
          key: "pause-all",
          title: t("topbar.cmd.pauseAll"),
          detail: t("topbar.cmd.pauseAllDetail"),
          keywords: "pause stop emergency halt agents autopilot runs 暫停 停止 緊急 全部",
          icon: <IconWarning size={15} />,
          run: () => pauseAllAgents(t),
        },
      );
    }
    // Every other part of Settings, by its own name.
    for (const part of settingsParts) {
      list.push({
        key: `settings-${part}`,
        title: t("topbar.cmd.settingsPart", { part: t(`settingsPage.part.${part}`) }),
        detail: t("topbar.cmd.settingsPartDetail"),
        keywords: `settings ${part} 設定`,
        icon: <IconGear size={15} />,
        run: () => openSettings(part),
      });
    }
    // Off until asked for or in use; once in use it cannot be hidden, since
    // seeing a runner or a run turns it straight back on.
    if (!workstation || !workstationInUse) {
      list.push({
        key: "toggle-workstation",
        title: workstation ? t("workstation.disable") : t("workstation.enable"),
        detail: t("workstation.hint"),
        keywords: "agent workstation runner runs autopilot 工作站 執行器 自動駕駛",
        run: () => updateUIPreference("agentWorkstation", !workstation),
      });
    }
    if (activeTab) {
      list.push(
        {
          key: "save-tab",
          title: t("palette.saveTab"),
          detail: t("palette.saveTabDetail"),
          keywords: "save 儲存",
          hint: `${MOD} S`,
          icon: <IconSave size={15} />,
          run: async () => {
            await saveTab(activeTab);
          },
        },
        {
          key: "save-all",
          title: t("palette.saveAll"),
          detail: t("palette.saveAllDetail"),
          keywords: "save all 全部 儲存",
          icon: <IconSaveAll size={15} />,
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
        icon: <IconUndo size={15} />,
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
        icon: <IconRedo size={15} />,
        run: async () => {
          await redoLast();
        },
      },
      {
        key: "reload",
        title: t("palette.reload"),
        detail: t("palette.reloadDetail"),
        keywords: "reload refresh 重新整理 同步",
        icon: <IconRefresh size={15} />,
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
        icon: <IconHelp size={15} />,
        run: () => setView("help"),
      },
      {
        key: "tour",
        title: t("palette.tour"),
        detail: t("palette.tourDetail"),
        keywords: "tour onboarding guide 導覽 教學 新手 引導",
        icon: <IconCompass size={15} />,
        run: replayTour,
      },
    );
    // Last, since there can be many: the other projects and workspaces, which
    // the top bar's project menu also lists.
    for (const project of projects) {
      if (project.isFolder || project.name === activeProject) continue;
      list.push({
        key: `switch-project-${project.name}`,
        title: t("topbar.cmd.switchProject", { name: project.label || project.name }),
        detail: t("topbar.nodeCount", { count: project.nodes }),
        keywords: `project switch open ${project.name} 專案 切換`,
        icon: <IconTree size={15} />,
        run: async () => {
          await switchProject(project.name);
        },
      });
    }
    for (const entry of workspaces) {
      if (entry.active) continue;
      list.push({
        key: `switch-workspace-${entry.path}`,
        title: t("topbar.cmd.switchWorkspace", { name: entry.label || entry.path }),
        detail: entry.path,
        keywords: "workspace switch open folder 工作區 切換",
        icon: <IconFolderOpen size={15} />,
        run: async () => {
          await switchWorkspace(entry.path);
        },
      });
    }
    return list;
  }, [
    activeProject,
    activeTab,
    canCreateProjects,
    createNode,
    nodeTypes,
    explorerCollapsed,
    projects,
    workspaces,
    switchProject,
    switchWorkspace,
    settingsParts,
    loadAll,
    minimap,
    paneOpen.graph,
    paneOpen.timeline,
    refreshProjects,
    redoLast,
    refreshTrash,
    saveAllTabs,
    saveTab,
    snapGuides,
    snapToGrid,
    skinChoices,
    stationView,
    projectMode,
    packs,
    stationPack,
    language,
    theme,
    toggleTheme,
    togglePane,
    undoLast,
    updateUIPreference,
    workstation,
    workstationInUse,
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
