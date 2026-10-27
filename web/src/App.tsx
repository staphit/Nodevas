import {
  lazy,
  Suspense,
  useEffect,
  useReducer,
  useRef,
  useState,
} from "react";
import { connectWS } from "./api";
import { decodePresence } from "./collab/presence";
import { liveDocumentSink } from "./collab/useLiveDocument";
import { reportError, useApp, usePreference } from "./store";
import { Sidebar, SidebarBackdrop } from "./components/Sidebar";
import { GraphPane } from "./components/GraphPane";
import { TimelinePane } from "./components/TimelinePane";
import { ConfirmDialogHost } from "./components/ConfirmDialog";
import { ProjectPickerHost } from "./components/ProjectPickerDialog";
import { CommandPalette } from "./components/CommandPalette";
import { OnboardingTour } from "./components/tour/OnboardingTour";
import { StartScreen } from "./components/StartScreen";
import { useWorkstationAutoEnable } from "./runners/workstation";
import { CopilotRoot } from "./copilot/CopilotRoot";
import { notifyAccessChanged, ProjectAccessBanner } from "./members/ProjectAccessBanner";
import { followupArrived as copilotFollowupArrived, runnersChanged as copilotRunnersChanged } from "./copilot/session";
import { AgentNetworkRoot } from "./agents/AgentNetworkRoot";
import { useAgents } from "./agents/agentStore";
import { useUsage } from "./usage/usageStore";
import { useAutopilot } from "./autopilot/autopilotStore";
import { useRunners } from "./runners/runnerStore";
import { useSessions } from "./runs/sessionStore";
import { RunsRoot } from "./runs/RunsRoot";
import { openSettings } from "./settings/openSettings";
import { useStation } from "./station/stationStore";
import { useRuns } from "./runs/runStore";
import { useRunToasts } from "./runs/runNotify";
import { RemoteSyncBanner } from "./components/backup/RemoteSyncBanner";
import {
  degradedDocumentsReducer,
  DocumentPersistenceBanner,
  documentPersistenceAction,
} from "./components/DocumentPersistenceBanner";
import { AuthGate } from "./components/SignIn";
import { ResizeHandle } from "./components/InteractionPrimitives";
import { useTouchContextMenu } from "./components/touch";
import { TopBar } from "./components/topbar/TopBar";
import { useI18n } from "./i18n";

const Drawer = lazy(() =>
  import("./components/Drawer").then((module) => ({ default: module.Drawer })),
);
const StationView = lazy(() => import("./station/StationView"));
const SettingsView = lazy(() => import("./settings/SettingsView"));
const PopoutEditor = lazy(() =>
  import("./components/PopoutEditor").then((module) => ({
    default: module.PopoutEditor,
  })),
);

const EXPLORER_MIN_W = 200;
const EXPLORER_MAX_W = 520;
const EXPLORER_DEFAULT_W = 304;

function MainApp() {
  const { language, t } = useI18n();
  const loadAll = useApp((s) => s.loadAll);
  const refreshState = useApp((s) => s.refreshState);
  const refreshTrash = useApp((s) => s.refreshTrash);
  const refreshProjects = useApp((s) => s.refreshProjects);
  const notifyDiskChange = useApp((s) => s.notifyDiskChange);
  const saveTab = useApp((s) => s.saveTab);
  const activeTab = useApp((s) => s.activeTab);
  const undoLast = useApp((s) => s.undoLast);
  const redoLast = useApp((s) => s.redoLast);
  const theme = useApp((s) => s.theme);
  const activeProject = useApp((s) => s.activeProject);
  const paneOpen = useApp((s) => s.paneOpen);
  const togglePane = useApp((s) => s.togglePane);
  const error = useApp((s) => s.error);
  const clearError = useApp((s) => s.clearError);
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  const explorerCollapsed = usePreference("explorerCollapsed");
  const projectMode = usePreference("projectMode");
  // Agents seen in this workspace turn the workstation on.
  useWorkstationAutoEnable();
  const [degradedDocuments, dispatchDocumentPersistence] = useReducer(
    degradedDocumentsReducer,
    new Set<string>(),
  );
  // Live width while dragging; the preference is written on commit so a drag
  // does not hammer localStorage. Adopting the preference again keeps a reset
  // (or another window's change) visible without a reload.
  const preferredExplorerWidth = usePreference("explorerWidth");
  const [explorerWidth, setExplorerWidth] = useState(preferredExplorerWidth);
  useEffect(() => setExplorerWidth(preferredExplorerWidth), [preferredExplorerWidth]);

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

  // Anything that mentions a setting can open it, instead of telling the
  // reader to hunt for it: each event opens its part of the Settings view.
  useEffect(() => {
    const onOpenSettings = () => openSettings("project");
    const onOpenNotify = () => openSettings("notifications");
    const onOpenRunners = () => openSettings("runners");
    const onOpenUsage = () => openSettings("usage");
    const onOpenBudget = () => openSettings("budget");
    window.addEventListener("nodevas-project-settings", onOpenSettings);
    window.addEventListener("nodevas-notify-settings", onOpenNotify);
    window.addEventListener("nodevas-open-runners", onOpenRunners);
    window.addEventListener("nodevas-open-usage", onOpenUsage);
    window.addEventListener("nodevas-open-budget", onOpenBudget);
    return () => {
      window.removeEventListener("nodevas-project-settings", onOpenSettings);
      window.removeEventListener("nodevas-notify-settings", onOpenNotify);
      window.removeEventListener("nodevas-open-runners", onOpenRunners);
      window.removeEventListener("nodevas-open-usage", onOpenUsage);
      window.removeEventListener("nodevas-open-budget", onOpenBudget);
    };
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
          void useAgents.getState().refreshAll();
          break;
        case "agents-changed":
        case "agent-messages-changed":
          void useAgents.getState().refreshAll();
          break;
        case "agent-notify":
          // The id is a delivered message sent with notify=true: toast it,
          // and raise a system notification when this browser allows one.
          void useAgents.getState().handleNotify(ev.id ?? "");
          break;
        case "copilot-followup":
          // The copilot came back by itself (a plan run ended): shown live
          // when its conversation is open, else as unread.
          void copilotFollowupArrived(ev.id ?? "");
          break;
        case "agent-ask":
          // The id is a new question: toast it to whoever may answer, with a
          // system notification when this browser allows one.
          void useAgents.getState().handleAsk(ev.id ?? "");
          break;
        case "runners-changed":
          // Only refetched once the Runners dialog has loaded them; a board
          // that never opened it has nothing to keep current.
          void useRunners.getState().refreshIfLoaded();
          // The copilot's model calls may run on this person's runner.
          copilotRunnersChanged();
          break;
        case "usage-changed":
          // A session reported its tokens: an open Usage dialog refetches.
          useUsage.getState().onUsageEvent();
          break;
        case "runs-changed":
          // The id is a node: its run list is refetched if something shows it.
          useRuns.getState().onRunsChanged(ev.id);
          // A finished run's usage arrives only as runs-changed.
          useUsage.getState().onUsageEvent();
          // An open Station refetches the project's runs, coalesced.
          useStation.getState().onRunsChanged();
          // A task's run moved: active autopilot runs held refetch.
          useAutopilot.getState().onRunsChanged();
          break;
        case "autopilot-changed":
          // The id is an autopilot run: refetched, coalesced; one not held
          // yet is how its tasks get their card badges.
          useAutopilot.getState().onChanged(ev.id, ev.goalNodeId);
          break;
        case "run-events":
          // The id is a run: an open run view fetches what came after its
          // last event.
          useRuns.getState().onRunEvents(ev.id);
          break;
        case "session-changes":
          // The id is a node: its external sessions are refetched if shown.
          useSessions.getState().onChanged(ev.id);
          break;
        case "run-notify":
          // The id is a run that finished, whose CI turned red or whose
          // merge ended: toast it, and raise a system notification when the
          // page is hidden and this browser allows one.
          void useRunToasts.getState().handleRunNotify(ev.id ?? "");
          break;
        case "folders-changed":
          // Only the file layout moved; the graph is untouched, so there is
          // nothing else to reload.
          void useApp.getState().refreshNodeFolders().catch(reportError);
          break;
        case "access-denied":
          // The server moved this connection out of a project it may no
          // longer watch (a membership change): say so instead of going quiet.
          notifyAccessChanged();
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
          // Run pushes sent while the socket was down are gone; refetch
          // whatever run lists and live runs are on screen.
          useRuns.getState().resync();
          useSessions.getState().resync();
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
  const editingTab = useApp((s) => (s.activeTab ? s.tabs.find((tab) => tab.id === s.activeTab) : null));
  useEffect(() => {
    if (!live) return;
    live.presence(activeTab ?? "", Boolean(editingTab?.dirty));
  }, [live, activeTab, editingTab?.dirty]);

  return (
    <div className={`app mode-${projectMode}`}>
      <TopBar />
      <DocumentPersistenceBanner documents={degradedDocuments} />
      <ProjectAccessBanner />
      <RemoteSyncBanner onOpenBackup={() => openSettings("backup")} />
      {error && (
        <div className="global-error" role="alert">
          <span>{error}</span>
          <button type="button" onClick={clearError} aria-label={t("topbar.closeError")}>
            ×
          </button>
        </div>
      )}
      <div className="main">
        {projectMode === "settings" ? (
          <Suspense fallback={<div className="station-loading">{t("station.loading")}</div>}>
            <SettingsView />
          </Suspense>
        ) : projectMode === "station" ? (
          <Suspense fallback={<div className="station-loading">{t("station.loading")}</div>}>
            <StationView
              drawer={
                activeTab !== null && (
                  <Suspense fallback={<div className="drawer drawer-loading">{t("stage.loadingEditor")}</div>}>
                    <Drawer />
                  </Suspense>
                )
              }
            />
          </Suspense>
        ) : (
          <>
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
          </>
        )}
      </div>
      <ConfirmDialogHost />
      <ProjectPickerHost />
      <CommandPalette />
      <StartScreen />
      <OnboardingTour />
      <CopilotRoot />
      <AgentNetworkRoot />
      <RunsRoot />
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
