import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useReducer,
  useRef,
  useState,
} from "react";
import { connectWS } from "./api";
import { decodePresence } from "./collab/presence";
import { liveDocumentSink } from "./collab/useLiveDocument";
import { reportError, useApp, useLastOperation, usePreference } from "./store";
import { Sidebar, SidebarBackdrop } from "./components/Sidebar";
import { GraphPane } from "./components/GraphPane";
import { TimelinePane } from "./components/TimelinePane";
import { ConfirmDialogHost } from "./components/ConfirmDialog";
import { ProjectPickerHost } from "./components/ProjectPickerDialog";
import { CommandPalette } from "./components/CommandPalette";
import { OnboardingTour } from "./components/tour/OnboardingTour";
import { NotifySettingsModal } from "./components/NotifySettingsModal";
import { BackupModal } from "./components/BackupModal";
import { RemoteSyncBanner } from "./components/backup/RemoteSyncBanner";
import {
  degradedDocumentsReducer,
  DocumentPersistenceBanner,
  documentPersistenceAction,
} from "./components/DocumentPersistenceBanner";
import { AuthGate, SignOutButton, useSignOut, useViewer, VisitorBadge } from "./components/SignIn";
import { ProjectSettings } from "./components/ProjectSettings";
import { OperationStatus, ResizeHandle } from "./components/InteractionPrimitives";
import { useTouchContextMenu } from "./components/touch";
import { PresenceAvatars } from "./components/PresenceAvatars";
import { TopbarOverflow, useNarrowViewport } from "./components/TopbarOverflow";
import { LANGUAGE_OPTIONS, useI18n } from "./i18n";
import {
  IconBell,
  IconCloud,
  IconGear,
  IconHelp,
  IconMoon,
  IconSignOut,
  IconSun,
  IconTree,
} from "./icons";

const Drawer = lazy(() =>
  import("./components/Drawer").then((module) => ({ default: module.Drawer })),
);
const PopoutEditor = lazy(() =>
  import("./components/PopoutEditor").then((module) => ({
    default: module.PopoutEditor,
  })),
);

const EXPLORER_MIN_W = 200;
const EXPLORER_MAX_W = 520;
const EXPLORER_DEFAULT_W = 304;

function MainApp() {
  const { language, setLanguage, t } = useI18n();
  const loadAll = useApp((s) => s.loadAll);
  const refreshState = useApp((s) => s.refreshState);
  const refreshTrash = useApp((s) => s.refreshTrash);
  const refreshProjects = useApp((s) => s.refreshProjects);
  const switchProject = useApp((s) => s.switchProject);
  const notifyDiskChange = useApp((s) => s.notifyDiskChange);
  const saveTab = useApp((s) => s.saveTab);
  const activeTab = useApp((s) => s.activeTab);
  const undoLast = useApp((s) => s.undoLast);
  const redoLast = useApp((s) => s.redoLast);
  const theme = useApp((s) => s.theme);
  const toggleTheme = useApp((s) => s.toggleTheme);
  const viewer = useViewer();
  const signOut = useSignOut();
  const graph = useApp((s) => s.graph);
  const projects = useApp((s) => s.projects);
  const activeProject = useApp((s) => s.activeProject);
  const paneOpen = useApp((s) => s.paneOpen);
  const togglePane = useApp((s) => s.togglePane);
  const error = useApp((s) => s.error);
  const clearError = useApp((s) => s.clearError);
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  const explorerCollapsed = usePreference("explorerCollapsed");
  const workspace = useApp((s) => s.workspace);
  const workspaceLabel = useApp(
    (s) => s.workspaces.find((entry) => entry.active)?.label ?? "",
  );
  const lastOperation = useLastOperation();
  const [degradedDocuments, dispatchDocumentPersistence] = useReducer(
    degradedDocumentsReducer,
    new Set<string>(),
  );
  // One state for all four, rather than a boolean each.
  //
  // Booleans let two of these be true at once, and the one that shows is
  // whichever React renders last — so opening the backup dialog while the Drive
  // workspace picker was still up did open it, underneath, and read as a button
  // that did nothing. Making them one value makes that unrepresentable instead
  // of relying on every opener remembering to close its neighbours.
  const [modal, setModal] = useState<
    "" | "notify" | "backup" | "drive-workspace" | "settings"
  >("");
  const closeModal = useCallback(() => setModal(""), []);
  // Live width while dragging; the preference is written on commit so a drag
  // does not hammer localStorage. Adopting the preference again keeps a reset
  // (or another window's change) visible without a reload.
  const preferredExplorerWidth = usePreference("explorerWidth");
  const [explorerWidth, setExplorerWidth] = useState(preferredExplorerWidth);
  useEffect(() => setExplorerWidth(preferredExplorerWidth), [preferredExplorerWidth]);

  const toggleExplorer = () => {
    updateUIPreference("explorerCollapsed", !explorerCollapsed);
  };

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  useEffect(() => {
    document.documentElement.lang = language === "zh-TW" ? "zh-Hant-TW" : "en";
  }, [language]);

  // Ctrl/⌘+Z steps back, Shift+Z (and Ctrl+Y, which Windows users reach for)
  // steps forward again. Text fields keep their own history, so anything
  // editable is left alone in both directions.
  useEffect(() => {
    const onUndo = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
      const key = event.key.toLowerCase();
      const redo = (key === "z" && event.shiftKey) || (key === "y" && !event.shiftKey);
      const undo = key === "z" && !event.shiftKey;
      if (!redo && !undo) return;
      const target = event.target as HTMLElement | null;
      if (
        target?.isContentEditable ||
        target?.closest("input, textarea, select, [contenteditable='true'], .cm-editor")
      ) {
        return;
      }
      event.preventDefault();
      void (redo ? redoLast() : undoLast()).catch(reportError);
    };
    window.addEventListener("keydown", onUndo);
    return () => window.removeEventListener("keydown", onUndo);
  }, [redoLast, undoLast]);

  // Anything that mentions Project Settings can open it, instead of telling
  // the reader to hunt for the gear in the top bar. The Drive import event is
  // how the sidebar's 連接工作區 flow reaches the cloud dialog now that the
  // dialog is owned here: importing and backing up are two tabs of one thing,
  // and a second mounted copy of it is exactly the two-dialogs-at-once problem
  // the single modal state exists to prevent.
  useEffect(() => {
    const onOpenSettings = () => setModal("settings");
    const onOpenNotify = () => setModal("notify");
    const onOpenDriveImport = () => setModal("drive-workspace");
    window.addEventListener("nodevas-project-settings", onOpenSettings);
    window.addEventListener("nodevas-notify-settings", onOpenNotify);
    window.addEventListener("nodevas-drive-import", onOpenDriveImport);
    return () => {
      window.removeEventListener("nodevas-project-settings", onOpenSettings);
      window.removeEventListener("nodevas-notify-settings", onOpenNotify);
      window.removeEventListener("nodevas-drive-import", onOpenDriveImport);
    };
  }, []);

  // OAuth returns through the local server. Re-open the originating Drive
  // flow so the user can continue without hunting for a second menu.
  useEffect(() => {
    const url = new URL(window.location.href);
    if (url.searchParams.get("drive") !== "connected") return;
    if (url.searchParams.get("source") === "workspace") {
      setModal("drive-workspace");
    } else {
      setModal("backup");
    }
    url.searchParams.delete("drive");
    url.searchParams.delete("source");
    const query = url.searchParams.toString();
    window.history.replaceState(
      {},
      "",
      `${url.pathname}${query ? `?${query}` : ""}${url.hash}`,
    );
  }, []);

  useEffect(() => {
    const onSave = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return;
      if (
        !(event.ctrlKey || event.metaKey) ||
        event.altKey ||
        event.shiftKey ||
        event.key.toLowerCase() !== "s"
      ) {
        return;
      }
      event.preventDefault();
      if (!activeTab) return;
      void saveTab(activeTab).catch(reportError);
    };
    window.addEventListener("keydown", onSave);
    return () => window.removeEventListener("keydown", onSave);
  }, [activeTab, saveTab]);

  // Long press stands in for right click on a touch screen, for every menu in
  // the app at once.
  useTouchContextMenu();

  useEffect(() => {
    void loadAll().catch(reportError);
    void refreshTrash().catch(reportError);
    void refreshProjects().catch(reportError);
    const live = connectWS((ev) => {
      const persistenceAction = documentPersistenceAction(ev);
      if (persistenceAction) {
        dispatchDocumentPersistence(persistenceAction);
        return;
      }
      switch (ev.type) {
        case "graph-changed":
          void loadAll().catch(reportError);
          void refreshTrash().catch(reportError);
          break;
        case "graph-ops": {
          // Somebody moved one card. Applying their ops keeps this window
          // where it is; a reload would take the scroll position, the
          // selection and any open menu with it. Anything this client cannot
          // apply falls back to the reload it used to do for every change.
          if (ev.from && ev.from === useApp.getState().selfPeerID) break;
          if (!useApp.getState().applyPeerGraphOps(ev.ops ?? [])) {
            void loadAll().catch(reportError);
          }
          break;
        }
        case "graph-drag":
          if (ev.from && ev.from !== useApp.getState().selfPeerID) {
            useApp.getState().setGhost(ev.from, ev.drag ?? null);
          }
          break;
        // Live document frames belong to whichever window has that document
        // open; the session itself owns what to do with them.
        case "doc-state":
          liveDocumentSink(ev.id ?? "")?.state(ev.leader ?? false, ev.seed ?? false);
          break;
        case "doc-update":
          if (ev.payload) liveDocumentSink(ev.id ?? "")?.update(ev.payload);
          break;
        case "doc-leader":
          liveDocumentSink(ev.id ?? "")?.leader(ev.leader ?? false);
          break;
        case "doc-flushed":
          liveDocumentSink(ev.id ?? "")?.flushed(ev.rev ?? "", ev.from ?? "");
          break;
        case "doc-compact":
          liveDocumentSink(ev.id ?? "")?.compact(ev.token ?? "");
          break;
        case "doc-snapshot-start":
          liveDocumentSink(ev.id ?? "")?.snapshotStart(
            ev.token ?? "",
            ev.size ?? 0,
            ev.chunks ?? 0,
          );
          break;
        case "doc-snapshot-chunk":
          liveDocumentSink(ev.id ?? "")?.snapshotChunk(
            ev.token ?? "",
            ev.seq ?? -1,
            ev.payload ?? "",
          );
          break;
        case "doc-snapshot-commit":
          liveDocumentSink(ev.id ?? "")?.snapshotCommit(ev.token ?? "");
          break;
        case "doc-snapshot-ready":
          liveDocumentSink(ev.id ?? "")?.snapshotReady(ev.token ?? "");
          break;
        case "doc-snapshot-accepted":
          liveDocumentSink(ev.id ?? "")?.snapshotAccepted(ev.token ?? "");
          break;
        case "doc-snapshot-rejected":
          liveDocumentSink(ev.id ?? "")?.snapshotRejected(ev.token ?? "");
          break;
        case "doc-reopen":
          liveDocumentSink(ev.id ?? "")?.reopen();
          break;
        case "awareness":
          if (ev.from && ev.from !== useApp.getState().selfPeerID) {
            useApp.getState().setPresence(ev.from, decodePresence(ev.payload));
          }
          break;
        case "node-changed": {
          if (!ev.id) break;
          // A document being edited live cannot be reloaded over the top: there
          // is no way to replace a shared text with a string without throwing
          // away what everybody else is typing. The session merges the file's
          // change instead, and only falls back to this path when it decides
          // the change was a replacement rather than an edit.
          const session = liveDocumentSink(ev.id);
          if (session) {
            session.externalChange();
            break;
          }
          void notifyDiskChange(ev.id).catch(reportError);
          break;
        }
        case "state-changed":
          void refreshState().catch(reportError);
          break;
        case "folders-changed":
          // Only the file layout moved; the graph is untouched, so there is
          // nothing else to reload.
          void useApp.getState().refreshNodeFolders().catch(reportError);
          break;
        case "project-changed":
          void refreshProjects().catch(reportError);
          void loadAll().catch(reportError);
          void refreshTrash().catch(reportError);
          break;
        case "hello":
          // A hello starts a fresh socket generation. Durability warnings are
          // scoped to the lost connection; the server will re-announce any
          // document that is still degraded after the client rejoins.
          dispatchDocumentPersistence({ type: "reset" });
          useApp.getState().setSelfPeerID(ev.selfId ?? "");
          break;
        case "presence":
          useApp.getState().setPeers(ev.peers ?? []);
          break;
        case "locks":
          useApp.getState().setLocks(ev.locks ?? []);
          break;
        case "lock-denied":
          useApp.getState().setLockDenied({
            nodeId: ev.id ?? "",
            actor: ev.actor?.name ?? "",
          });
          break;
      }
    });
    useApp.getState().setLive(live);
    return () => {
      useApp.getState().setLive(null);
      live.close();
    };
  }, [loadAll, refreshState, refreshTrash, refreshProjects, notifyDiskChange]);

  // The socket only carries the project this window shows, so a project switch
  // has to move the client to the other room.
  const live = useApp((s) => s.live);
  useEffect(() => {
    // Document ids are only unique inside a project room. Never carry a
    // warning into another project, where a matching id names other content.
    dispatchDocumentPersistence({ type: "reset" });
    if (live && activeProject) live.subscribe(activeProject);
  }, [live, activeProject]);

  // Presence follows the open document: which node, and whether it is being
  // edited rather than just read.
  // The presence cluster keeps fewer faces on a phone-width bar; same
  // breakpoint the overflow menu uses, so the bar collapses as one thing.
  const narrowTopbar = useNarrowViewport();
  const editingTab = useApp((s) => (s.activeTab ? s.tabs.find((tab) => tab.id === s.activeTab) : null));
  useEffect(() => {
    if (!live) return;
    live.presence(activeTab ?? "", Boolean(editingTab?.dirty));
  }, [live, activeTab, editingTab?.dirty]);

  return (
    <div className="app">
      <header className="topbar">
        <button
          type="button"
          className="icon-btn explorer-toggle"
          onClick={toggleExplorer}
          title={explorerCollapsed ? t("topbar.explorer.expand") : t("topbar.explorer.collapse")}
          aria-label={explorerCollapsed ? t("topbar.explorer.expand") : t("topbar.explorer.collapse")}
          aria-expanded={!explorerCollapsed}
          aria-controls="project-explorer"
        >
          <IconTree size={15} />
          <span aria-hidden>{explorerCollapsed ? "»" : "«"}</span>
        </button>
        <span className="brand-mark">
          <IconTree size={17} />
        </span>
        <h1>Nodevas</h1>

        {/* Breadcrumb, not a bare dropdown: the workspace is named, and the
            control says what switching it does [B-03]. */}
        <nav className="file-ops project-breadcrumb" aria-label={t("topbar.currentLocation")}>
          <span className="crumb-workspace" title={workspace}>
            {workspaceLabel || t("topbar.workspace")}
          </span>
          <span className="crumb-separator" aria-hidden>
            ／
          </span>
          <label className="crumb-label" htmlFor="project-quick-switch">
            {t("topbar.project")}
          </label>
          <select
            id="project-quick-switch"
            className="project-select"
            value={activeProject}
            title={t("topbar.switchProject")}
            onChange={(e) => {
              if (e.target.value) void switchProject(e.target.value).catch(reportError);
            }}
          >
            {projects
              .filter((p) => !p.isFolder)
              .map((p) => (
                <option key={p.name} value={p.name}>
                  {`${"　".repeat(p.depth)}${p.label} (${p.nodes})`}
                </option>
              ))}
          </select>
        </nav>

        {graph?.type && <span className="project-type">{graph.type}</span>}
        {lastOperation && (
          <OperationStatus
            status={lastOperation.status}
            message={
              lastOperation.status === "error" || lastOperation.status === "conflict"
                ? lastOperation.message
                : undefined
            }
          />
        )}
        <PresenceAvatars narrow={narrowTopbar} />
        {/* Renders nothing unless the session is a visitor's. */}
        <VisitorBadge />
        <div className="topbar-actions">
          <button
            className="command-trigger"
            type="button"
            onClick={() => window.dispatchEvent(new Event("nodevas-command-palette"))}
            title={`${t("topbar.searchOrCommand")} (Ctrl/⌘ K)`}
          >
            {t("topbar.searchOrCommand")} <kbd>Ctrl/⌘ K</kbd>
          </button>
          <label className="language-control">
            <span className="sr-only">{t("language.label")}</span>
            <select
              className="language-select"
              value={language}
              aria-label={t("language.label")}
              onChange={(event) => setLanguage(event.target.value as typeof language)}
            >
              {LANGUAGE_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {t(`language.option.${option}`)}
                </option>
              ))}
            </select>
          </label>
          <button
            className="icon-btn"
            data-tour="tour-replay"
            onClick={() => window.dispatchEvent(new CustomEvent("nodevas-tour-start"))}
            title={t("topbar.replayTour")}
            aria-label={t("topbar.replayTour")}
          >
            <IconHelp size={16} />
          </button>
          <button
            className="icon-btn"
            data-tour="settings"
            onClick={() => setModal("settings")}
            title={t("topbar.projectSettingsTitle")}
            aria-label={t("topbar.projectSettings")}
          >
            <IconGear size={16} />
          </button>
          <button
            className="icon-btn"
            data-tour="notify"
            onClick={() => setModal("notify")}
            title={t("topbar.deadlineSettings")}
            aria-label={t("topbar.deadlineSettings")}
          >
            <IconBell size={16} />
          </button>
          <button
            className="icon-btn"
            data-tour="backup"
            onClick={() => setModal("backup")}
            title={t("topbar.cloudBackupTitle")}
            aria-label={t("topbar.cloudBackup")}
          >
            <IconCloud size={16} />
          </button>
          <button
            className="icon-btn"
            data-tour="theme"
            onClick={toggleTheme}
            title={theme === "dark" ? t("topbar.lightMode") : t("topbar.darkMode")}
          >
            {theme === "dark" ? <IconSun size={16} /> : <IconMoon size={16} />}
          </button>
          {/* Renders nothing on a loopback server, where there is no session. */}
          <SignOutButton />
          {/* Same five actions, one button wide, for the phone row. The
              buttons above are hidden by CSS there rather than unmounted, so
              nothing about the desktop bar changes. */}
          <TopbarOverflow
            items={[
              {
                id: "tour-replay",
                label: t("topbar.replayTour"),
                icon: <IconHelp size={16} />,
                onSelect: () => window.dispatchEvent(new CustomEvent("nodevas-tour-start")),
              },
              {
                id: "settings",
                label: t("topbar.projectSettings"),
                icon: <IconGear size={16} />,
                onSelect: () => setModal("settings"),
              },
              {
                id: "notify",
                label: t("topbar.deadlineSettings"),
                icon: <IconBell size={16} />,
                onSelect: () => setModal("notify"),
              },
              {
                id: "backup",
                label: t("topbar.cloudBackup"),
                icon: <IconCloud size={16} />,
                onSelect: () => setModal("backup"),
              },
              {
                id: "theme",
                label: theme === "dark" ? t("topbar.lightMode") : t("topbar.darkMode"),
                icon: theme === "dark" ? <IconSun size={16} /> : <IconMoon size={16} />,
                onSelect: toggleTheme,
              },
              ...(viewer
                ? [
                    {
                      id: "sign-out",
                      label: t("topbar.signOut", { name: viewer.name || viewer.role }),
                      icon: <IconSignOut size={16} />,
                      onSelect: signOut,
                    },
                  ]
                : []),
            ]}
          />
        </div>
      </header>
      <DocumentPersistenceBanner documents={degradedDocuments} />
      <RemoteSyncBanner onOpenBackup={() => setModal("backup")} />
      {modal === "notify" && <NotifySettingsModal onClose={closeModal} />}
      {/* Two names, one dialog: "drive-workspace" is the same BackupModal
        * opened on its import tab, kept as a distinct state so the OAuth
        * return and the sidebar land the user on the tab they came from. */}
      {modal === "backup" && <BackupModal onClose={closeModal} />}
      {modal === "drive-workspace" && (
        <BackupModal onClose={closeModal} initialTab="import" />
      )}
      {modal === "settings" && <ProjectSettings onClose={closeModal} />}
      {error && (
        <div className="global-error" role="alert">
          <span>{error}</span>
          <button type="button" onClick={clearError} aria-label={t("topbar.closeError")}>
            ×
          </button>
        </div>
      )}
      <div className="main">
        {/* Phone-width only (the component renders nothing when wide): dims
            the board the explorer overlay covers and gives a tap anywhere
            outside the panel the obvious meaning — close it. */}
        <SidebarBackdrop
          open={!explorerCollapsed}
          onClose={() => updateUIPreference("explorerCollapsed", true)}
        />
        <div
          id="project-explorer"
          className={`sidebar-shell${explorerCollapsed ? " collapsed" : ""}`}
          aria-hidden={explorerCollapsed}
          style={
            explorerCollapsed
              ? undefined
              : { width: explorerWidth, flexBasis: explorerWidth }
          }
        >
          {!explorerCollapsed && <Sidebar />}
        </div>
        {!explorerCollapsed && (
          <ResizeHandle
            className="explorer-resize"
            orientation="vertical"
            value={explorerWidth}
            min={EXPLORER_MIN_W}
            max={EXPLORER_MAX_W}
            step={12}
            largeStep={32}
            label={t("topbar.resizeExplorer")}
            title={t("topbar.resizeExplorerTitle")}
            valueText={(value) => `${Math.round(value)} ${t("topbar.pixels")}`}
            onChange={setExplorerWidth}
            onCommit={(value) => updateUIPreference("explorerWidth", value)}
            onReset={() => {
              setExplorerWidth(EXPLORER_DEFAULT_W);
              updateUIPreference("explorerWidth", EXPLORER_DEFAULT_W);
            }}
          />
        )}
        <ResizableStage paneOpen={paneOpen} togglePane={togglePane} />
      </div>
      <ConfirmDialogHost />
      <ProjectPickerHost />
      <CommandPalette />
      <OnboardingTour />
    </div>
  );
}

const DEFAULT_SPLIT = 50;

function ResizableStage({
  paneOpen,
  togglePane,
}: {
  paneOpen: { graph: boolean; timeline: boolean };
  togglePane: (pane: "graph" | "timeline") => void;
}) {
  const { t } = useI18n();
  const stageRef = useRef<HTMLElement>(null);
  const preferredSplit = usePreference("paneSplit");
  const [split, setSplit] = useState(preferredSplit);
  const splitRef = useRef(split);
  useEffect(() => {
    splitRef.current = preferredSplit;
    setSplit(preferredSplit);
  }, [preferredSplit]);
  const [stageHeight, setStageHeight] = useState(0);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [activePane, setActivePane] = useState<"graph" | "timeline" | null>(null);
  const bothOpen = paneOpen.graph && paneOpen.timeline;
  const activeTab = useApp((state) => state.activeTab);

  useEffect(
    () => () => {
      document.body.classList.remove("pane-resizing");
    },
    [],
  );

  // The split is a percentage but the pointer moves in pixels: without the
  // stage height the handle would jump the full range in ~70px of travel.
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    setStageHeight(stage.getBoundingClientRect().height);
    const observer = new ResizeObserver(([entry]) => {
      setStageHeight(entry.contentRect.height);
    });
    observer.observe(stage);
    return () => observer.disconnect();
  }, []);

  const applySplit = (next: number, persist = false) => {
    const clamped = Math.max(15, Math.min(85, next));
    splitRef.current = clamped;
    setSplit(clamped);
    if (persist) useApp.getState().updateUIPreference("paneSplit", clamped);
  };

  return (
    <main ref={stageRef} className={`stage split${bothOpen ? " has-open-split" : ""}`}>
      <GraphPane
        collapsed={!paneOpen.graph}
        onToggle={() => togglePane("graph")}
        paneStyle={bothOpen ? { flex: `0 0 ${split}%` } : undefined}
        selectedNodeId={selectedNodeId}
        onSelectedNodeChange={setSelectedNodeId}
        keyboardActive={activePane === "graph"}
        onActivate={() => setActivePane("graph")}
      />
      <ResizeHandle
        className={`pane-resizer${bothOpen ? "" : " disabled"}`}
        orientation="horizontal"
        value={split}
        min={15}
        max={85}
        step={3}
        largeStep={10}
        scale={Math.max(stageHeight / 100, 0.01)}
        disabled={!bothOpen}
        label={t("stage.resize")}
        title={t("stage.resizeTitle")}
        valueText={(value) =>
          `${t("stage.upper", { value: String(Math.round(value)) })}, ${t("stage.lower", {
            value: String(Math.round(100 - value)),
          })}`
        }
        onChange={(value) => applySplit(value)}
        onCommit={(value) => applySplit(value, true)}
        onReset={() => applySplit(DEFAULT_SPLIT, true)}
        onResizeStateChange={(resizing) =>
          document.body.classList.toggle("pane-resizing", resizing)
        }
      />
      <TimelinePane
        collapsed={!paneOpen.timeline}
        onToggle={() => togglePane("timeline")}
        selectedNodeId={selectedNodeId}
        onSelectedNodeChange={setSelectedNodeId}
        keyboardActive={activePane === "timeline"}
        onActivate={() => setActivePane("timeline")}
      />
      {activeTab !== null && (
        <Suspense fallback={<div className="drawer drawer-loading">{t("stage.loadingEditor")}</div>}>
          <Drawer />
        </Suspense>
      )}
    </main>
  );
}

export default function App() {
  const { language, t } = useI18n();
  useEffect(() => {
    document.documentElement.lang = language === "zh-TW" ? "zh-Hant-TW" : "en";
  }, [language]);

  return window.location.pathname === "/popout" ? (
    <Suspense fallback={<div className="popout-editor-loading">{t("stage.loadingPopout")}</div>}>
      <PopoutEditor />
    </Suspense>
  ) : (
    <AuthGate>
      <MainApp />
    </AuthGate>
  );
}
