import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
} from "react";
import CodeMirror, { type ReactCodeMirrorRef } from "@uiw/react-codemirror";
import { EditorView } from "@codemirror/view";

import { api } from "../api";
import {
  nodeById,
  operationScope,
  reportError,
  useApp,
  useNodeLock,
  useOperation,
  usePeersOnNode,
  usePreference,
  useRemoteCarets,
} from "../store";
import { OperationStatus, ResizeHandle } from "./InteractionPrimitives";
import { useCanEdit } from "./SignIn";
import { DocumentOutline } from "./inspector/DocumentOutline";
import { NodeAppearance } from "./inspector/NodeAppearance";
import { NodeLinkPicker } from "./NodeLinkPicker";
import { useNarrowViewport } from "./TopbarOverflow";
import { LifecyclePanel } from "./inspector/LifecyclePanel";
import { NodeHistory } from "./inspector/NodeHistory";
import { NodeMetaForm } from "./inspector/NodeMetaForm";
import { NodeRelations } from "./inspector/NodeRelations";
import { NodeTimeline } from "./inspector/NodeTimeline";
import { DocumentPreview } from "./drawer/DocumentPreview";
import { DrawerTabs } from "./drawer/DrawerTabs";
import { EditorToolbar } from "./drawer/EditorToolbar";
import { SubpageControls } from "./drawer/SubpageControls";
import { TableToolbar } from "./drawer/TableToolbar";
import { useEditorExtensions, type EditorMode } from "./drawer/editorExtensions";
import { setRemoteCarets } from "../editor/remoteCarets";
import { useLiveDocument } from "../collab/useLiveDocument";
import { YSyncConfig, ySync, ySyncFacet } from "y-codemirror.next";
import { insertNodeLink, insertSnippet } from "./drawer/markdownCommands";
import { editsAsMarkdown, PAGE_FORMATS } from "./drawer/pageFormats";
import { useAttachments } from "./drawer/useAttachments";
import { useAutosave } from "./drawer/useAutosave";
import { useNodePages } from "./drawer/useNodePages";
import { useTableEditing } from "./drawer/useTableEditing";

/**
 * Right-hand slide-over panel. Hidden until a node is clicked; the file's
 * frontmatter never shows — the form above IS the frontmatter, the editor
 * holds only the markdown document body.
 */
const DRAWER_MIN_W = 380;
const DRAWER_MAX_W = 960;
const DRAWER_DEFAULT_W = 580;

/**
 * Phone layout (05-drawer.css): the panel is a bottom sheet. Its height is kept
 * in viewport units so a rotation cannot strand it at a size that no longer
 * fits, and it stops short of the full screen so the board — and the node being
 * edited — stays visible behind it.
 */
const SHEET_DEFAULT_VH = 76;
const SHEET_MIN_VH = 34;
const SHEET_MAX_VH = 94;
/** Dragged down past a third of its own height, the sheet is being put away. */
const SHEET_CLOSE_RATIO = 1 / 3;
/** Long enough for the settle transition to finish before the transform drops. */
const SHEET_SETTLE_MS = 220;

function clampVh(value: number): number {
  return Math.max(SHEET_MIN_VH, Math.min(SHEET_MAX_VH, value));
}

// Pointer capture keeps the gesture alive once the finger leaves the handle,
// but it is an enhancement: environments without it (jsdom, older engines)
// must still drag. Same shape as ResizeHandle's, which cannot export it.
function capturePointer(element: Element, pointerId: number): void {
  try {
    element.setPointerCapture?.(pointerId);
  } catch {
    /* capture unavailable: events still arrive while over the handle */
  }
}

function releaseCapture(element: Element, pointerId: number): void {
  try {
    if (element.hasPointerCapture?.(pointerId)) {
      element.releasePointerCapture?.(pointerId);
    }
  } catch {
    /* nothing to release */
  }
}

/**
 * The sheet's grab bar. Without one a sheet reads as an overlay that is stuck
 * there, so it is drawn even on the pointer that cannot drag it.
 *
 * Dragging down puts the sheet away, dragging up makes it taller. The gesture
 * is a shortcut, never the only way out: the close button and Escape both keep
 * working, which is why the bar itself stays out of the accessibility tree
 * instead of pretending to be a second control.
 */
export function DrawerSheetHandle({
  heightVh,
  onHeightChange,
  onOffsetChange,
  onDragStateChange,
  onDismiss,
}: {
  heightVh: number;
  onHeightChange: (value: number) => void;
  onOffsetChange: (offset: number) => void;
  onDragStateChange: (dragging: boolean) => void;
  onDismiss: () => void;
}) {
  const dragRef = useRef<{
    pointerId: number;
    startY: number;
    heightVh: number;
    offset: number;
  } | null>(null);

  const finishDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    releaseCapture(event.currentTarget, event.pointerId);
    onDragStateChange(false);
    const sheetPx = (heightVh / 100) * window.innerHeight;
    if (drag.offset > sheetPx * SHEET_CLOSE_RATIO) {
      onDismiss();
      return;
    }
    onOffsetChange(0);
  };

  return (
    <div
      className="drawer-sheet-handle"
      aria-hidden="true"
      onPointerDown={(event) => {
        if (event.pointerType === "mouse" && event.button !== 0) return;
        event.preventDefault();
        event.stopPropagation();
        capturePointer(event.currentTarget, event.pointerId);
        dragRef.current = {
          pointerId: event.pointerId,
          startY: event.clientY,
          heightVh,
          offset: 0,
        };
        onDragStateChange(true);
      }}
      onPointerMove={(event) => {
        const drag = dragRef.current;
        if (!drag || drag.pointerId !== event.pointerId) return;
        event.preventDefault();
        const delta = event.clientY - drag.startY;
        if (delta >= 0) {
          drag.offset = delta;
          onOffsetChange(delta);
          return;
        }
        // Upward is a resize, not a dismissal, so any pending offset is undone
        // before the sheet grows.
        drag.offset = 0;
        onOffsetChange(0);
        onHeightChange(
          clampVh(drag.heightVh + (-delta / window.innerHeight) * 100),
        );
      }}
      onPointerUp={finishDrag}
      onPointerCancel={finishDrag}
    >
      <span className="drawer-sheet-grip" />
    </div>
  );
}

export function Drawer() {
  const graph = useApp((s) => s.graph);
  const activeTab = useApp((s) => s.activeTab);
  const closeDrawer = useApp((s) => s.closeDrawer);
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  // The tab context menu answers Escape itself; while it is open the panel
  // must not take the same key as "close me".
  const tabMenuOpen = useRef(false);
  // Live width during a drag; committed to the preference on release, and
  // re-adopted when the stored value changes (reset, other window).
  const preferredWidth = usePreference("drawerWidth");
  const [width, setWidth] = useState(preferredWidth);
  useEffect(() => setWidth(preferredWidth), [preferredWidth]);
  const collapsed = usePreference("drawerCollapsed");
  // Bottom sheet geometry, read only by the phone rules in 05-drawer.css. The
  // offset is what the finger is holding; `settling` keeps the transform alive
  // long enough for the sheet to slide back rather than snap.
  const [sheetHeight, setSheetHeight] = useState(SHEET_DEFAULT_VH);
  const [sheetOffset, setSheetOffset] = useState(0);
  const [sheetDragging, setSheetDragging] = useState(false);
  const [sheetSettling, setSheetSettling] = useState(false);
  useEffect(() => {
    if (!sheetSettling) return;
    const timer = window.setTimeout(() => setSheetSettling(false), SHEET_SETTLE_MS);
    return () => window.clearTimeout(timer);
  }, [sheetSettling]);

  const toggleCollapsed = () => {
    updateUIPreference("drawerCollapsed", !collapsed);
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        if (tabMenuOpen.current) return;
        closeDrawer();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [closeDrawer]);

  if (activeTab === null) return null;

  if (collapsed) {
    return (
      <div className="drawer collapsed-rail" aria-label="節點編輯（已收合）">
        <button
          type="button"
          className="drawer-expand"
          title="展開資訊欄"
          aria-label="展開資訊欄"
          onClick={toggleCollapsed}
        >
          «
        </button>
        <span className="drawer-rail-title">
          {nodeById(graph, activeTab)?.title || activeTab}
        </span>
      </div>
    );
  }

  return (
    <div
      className={`drawer${sheetDragging || sheetSettling ? " sheet-shifted" : ""}`}
      role="dialog"
      aria-label="節點編輯"
      data-sheet-drag={sheetDragging ? "active" : undefined}
      style={
        {
          width,
          "--sheet-height": `${sheetHeight}vh`,
          "--sheet-offset": `${sheetOffset}px`,
        } as CSSProperties
      }
    >
      <DrawerSheetHandle
        heightVh={sheetHeight}
        onHeightChange={setSheetHeight}
        onOffsetChange={(offset) => {
          setSheetOffset(offset);
          if (offset === 0) setSheetSettling(true);
        }}
        onDragStateChange={setSheetDragging}
        onDismiss={closeDrawer}
      />
      <ResizeHandle
        className="drawer-resize"
        orientation="vertical"
        value={width}
        min={DRAWER_MIN_W}
        max={DRAWER_MAX_W}
        direction={-1}
        step={16}
        largeStep={48}
        label="調整資訊欄寬度"
        title="拖曳調整資訊欄寬度"
        valueText={(value) => `${Math.round(value)} 像素`}
        onChange={setWidth}
        onCommit={(value) => updateUIPreference("drawerWidth", value)}
        onReset={() => {
          setWidth(DRAWER_DEFAULT_W);
          updateUIPreference("drawerWidth", DRAWER_DEFAULT_W);
        }}
      />
      <DrawerTabs
        activeTab={activeTab}
        menuOpenRef={tabMenuOpen}
        onToggleCollapsed={toggleCollapsed}
        onClose={closeDrawer}
      />
      <TabBody key={activeTab} id={activeTab} />
    </div>
  );
}


/** split a node file into raw frontmatter block (kept verbatim) + body */
function splitContent(content: string): { fm: string; body: string } {
  const m = content.match(/^(---\r?\n[\s\S]*?\r?\n---\r?\n?)/);
  if (!m) return { fm: "", body: content };
  return { fm: m[1], body: content.slice(m[1].length).replace(/^\r?\n/, "") };
}

export function TabBody({
  id,
  initialPageID = "main",
  saveRequest = 0,
  onDirtyChange,
}: {
  id: string;
  initialPageID?: string;
  saveRequest?: number;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const tab = useApp((s) => s.tabs.find((t) => t.id === id));
  const setTabContent = useApp((s) => s.setTabContent);
  const adoptDocumentRev = useApp((s) => s.adoptDocumentRev);
  const saveDraft = useApp((s) => s.saveDraft);
  const applyDraft = useApp((s) => s.applyDraft);
  const resolveConflict = useApp((s) => s.resolveConflict);
  const openNodeLink = useApp((s) => s.openNodeLink);
  const theme = useApp((s) => s.theme);

  const cmRef = useRef<ReactCodeMirrorRef>(null);
  const syncingEditorRef = useRef(false);
  const getCurrentPageContent = useCallback(
    () => cmRef.current?.view?.state.doc.toString(),
    [],
  );
  const replaceCurrentPageContent = useCallback((content: string) => {
    const view = cmRef.current?.view;
    if (!view || view.state.doc.toString() === content) return;
    syncingEditorRef.current = true;
    try {
      view.dispatch({
        changes: {
          from: 0,
          to: view.state.doc.length,
          insert: content,
        },
      });
    } finally {
      syncingEditorRef.current = false;
    }
  }, []);

  // Soft lock [P2]: while someone else has this document open for editing,
  // this window is read-only by default. It is courtesy, not enforcement —
  // "強制編輯" takes it over, and the Rev check on save is what actually
  // prevents a silent overwrite.
  const lock = useNodeLock(id);
  const peers = usePeersOnNode(id);
  const requestLock = useApp((s) => s.requestLock);
  const releaseLock = useApp((s) => s.releaseLock);
  const heldByOther = Boolean(lock && !lock.mine);
  // A visitor session reads everything and changes nothing. The server refuses
  // the writes regardless; what this gates is the offer — a document that lets
  // you type and then fails to save reads as a broken server, not as a rule.
  const canEdit = useCanEdit();

  useEffect(() => {
    requestLock(id);
    return () => releaseLock(id);
  }, [id, requestLock, releaseLock]);

  const [drawerView, setDrawerView] = useState<
    "content" | "relations" | "timeline" | "appearance"
  >("content");
  // The panel is one column of panes with the document last, which a desktop
  // drawer has the height to show all at once and a phone sheet does not: on a
  // 390px screen 基本資料 and 實際狀態 together are taller than half the sheet,
  // and the document ends up below the fold of something that does not scroll.
  // So on a phone the metadata opens closed. It is the same reasoning as the
  // sheet itself — what you came for is the document; the metadata is what you
  // check afterwards, and one tap away is close enough for that. Kept in state
  // rather than left to the `open` attribute so a window resized across the
  // breakpoint mid-session lands on the right default for its new width.
  const narrow = useNarrowViewport();
  const [metaOpen, setMetaOpen] = useState(!narrow);
  useEffect(() => setMetaOpen(!narrow), [narrow]);
  const editorMode = usePreference("editorMode");
  const setEditorMode = (mode: EditorMode) =>
    useApp.getState().updateUIPreference("editorMode", mode);
  const preview = editorMode === "preview";
  const outlineOpen = usePreference("editorOutline");
  const toggleOutline = () =>
    useApp.getState().updateUIPreference("editorOutline", !outlineOpen);
  /** Caret line, so the outline can highlight the heading being edited. */
  const [caretLine, setCaretLine] = useState(1);
  const handledSaveRequest = useRef(saveRequest);
  const subpages = useNodePages({
    nodeId: id,
    initialPageID,
    editorMode,
    setEditorMode,
    getCurrentPageContent,
    replaceCurrentPageContent,
  });
  const {
    activePageID,
    activeFormat,
    pageBusy,
    pageDoc,
    setPageDoc,
    saveSubpage,
  } = subpages;
  const fontSize = usePreference("editorFontSize");
  const setFontSize = (next: number) =>
    useApp.getState().updateUIPreference("editorFontSize", next);
  const { fm, body } = useMemo(
    () => splitContent(tab?.content ?? ""),
    [tab?.content],
  );

  const onBodyChange = useCallback(
    (v: string) => setTabContent(id, fm ? `${fm}\n${v}` : v),
    [id, fm, setTabContent],
  );

  const editorBody = activePageID ? (pageDoc?.content ?? "") : body;
  const editorDirty = activePageID ? (pageDoc?.dirty ?? false) : (tab?.dirty ?? false);
  // Neither of these can be auto-saved: a conflict is a question waiting for an
  // answer, and a document somebody else has locked is not ours to write.
  const saveState = useOperation(operationScope.document(id));

  // ---- live editing [P2] --------------------------------------------------
  // A node's own document and each of its subpages are separate sessions, and
  // one only opens once somebody else is in the room: a person editing alone
  // has nobody to converge with and should not pay for loading a CRDT to find
  // that out. The server decides who seeds, so starting late is not a problem.
  //
  // Word and HTML pages are excluded: what is on screen for those is a
  // conversion of the file rather than the file, so two people converging on
  // the conversion would each write a different document back.
  const liveText = activePageID ? (pageDoc?.content ?? "") : body;
  const liveDoc = useLiveDocument({
    nodeId: id,
    pageId: activePageID ?? undefined,
    enabled:
      Boolean(tab?.loaded) &&
      peers.length > 0 &&
      (!activePageID || activeFormat === "md" || activeFormat === "txt"),
    // The editor's text rather than the file's: this window may have unsaved
    // edits, and seeding from disk would throw them away in front of the person
    // who typed them.
    initialText: useCallback(() => liveText, [liveText]),
    // Somebody else's leader wrote the file this session is for. Everyone in
    // the session holds the same converged text, so its revision is this
    // window's revision too, and taking it is what stops the `node-changed`
    // that follows from being read as an edit made behind this window's back.
    onFlushed: useCallback(
      (rev: string) => {
        if (!activePageID) {
          adoptDocumentRev(id, rev);
          return;
        }
        setPageDoc((current) =>
          current?.id === activePageID
            ? { ...current, rev, dirty: false, conflict: null }
            : current,
        );
      },
      [activePageID, adoptDocumentRev, id, setPageDoc],
    ),
    // What the file says now, for merging an edit made outside the session.
    // The node's own document arrives with its frontmatter, which the graph
    // owns and the shared text never contains.
    readFileText: useCallback(async () => {
      // The revision is taken as well as the text. Merging leaves this window
      // holding a document built on the file as it is now, and a leader that
      // kept the old revision would 409 on its next save against an edit it
      // has already taken in.
      if (activePageID) {
        const page = await api.getNodePage(id, activePageID);
        setPageDoc((current) =>
          current?.id === activePageID ? { ...current, rev: page.rev } : current,
        );
        return page.content;
      }
      const file = await api.getNode(id);
      adoptDocumentRev(id, file.rev, false);
      return splitContent(file.content).body;
    }, [activePageID, adoptDocumentRev, id, setPageDoc]),
    // Too much changed to be an edit. The session stands down and the ordinary
    // disk-conflict banner takes it from here, because choosing between a
    // restore and a half-typed paragraph is the user's call, not this window's.
    onExternalGiveUp: useCallback(() => {
      void useApp.getState().notifyDiskChange(id).catch(reportError);
    }, [id]),
  });
  // While a session is live the soft lock stops meaning read-only: converging
  // is what the CRDT is for. What is still single-writer is the file, and that
  // is the leader's job, so everyone else simply never saves.
  const saveBlocked =
    (liveDoc.ready ? !liveDoc.isLeader : heldByOther) ||
    Boolean(activePageID ? pageDoc?.conflict : tab?.conflict);

  // Telling the room which revision this window wrote is what lets the others
  // adopt it instead of reading the `node-changed` that follows as somebody
  // editing the file behind their back.
  const reportedRev = useRef("");
  useEffect(() => {
    const rev = (activePageID ? pageDoc?.rev : tab?.rev) ?? "";
    if (!liveDoc.ready || !liveDoc.isLeader || !rev || rev === reportedRev.current) return;
    reportedRev.current = rev;
    liveDoc.reportFlushed(rev);
  }, [activePageID, pageDoc?.rev, tab?.rev, liveDoc]);

  // ---- other people's carets [P2] ----------------------------------------
  const remoteCaretList = useRemoteCarets(id, activePageID ?? null);
  const reportPresenceState = useApp((s) => s.reportPresenceState);
  useEffect(() => {
    // Pushed in as an effect rather than rebuilt into the extension list: the
    // extensions are memoised per document, and remaking them on every
    // keystroke somebody else types would reset the editor's own state.
    cmRef.current?.view?.dispatch({ effects: setRemoteCarets.of(remoteCaretList) });
  }, [remoteCaretList]);
  // Where this browser's caret is. Sent on selection changes only; the store
  // throttles and merges it with whatever the board is reporting.
  const publishSelection = useCallback(
    (anchor: number, head: number) => {
      reportPresenceState({
        nodeId: id,
        pageId: activePageID ?? undefined,
        selection: { anchor, head },
      });
    },
    [reportPresenceState, id, activePageID],
  );
  useEffect(() => {
    // Leaving the document takes the caret with it, rather than leaving it
    // parked in a file this person is no longer looking at.
    return () => reportPresenceState({ selection: undefined });
  }, [reportPresenceState, id, activePageID]);

  const autosave = useAutosave({
    content: editorBody,
    dirty: editorDirty,
    blocked: saveBlocked,
    documentKey: activePageID ?? "main",
    save: useCallback(
      async (reason) => {
        if (activePageID) {
          const result = await saveSubpage();
          // Rethrown so the scheduler retries. A conflict carries no error and
          // is not rethrown: it is a question on screen, not a failed request.
          if (result.error) throw new Error(result.error);
          return;
        }
        // "manual" is the only reason a person chose, so it is the only one
        // that commits a staged lifecycle status [B-04]; the rest are the
        // editor tidying up behind them. `unload` asks for a request that can
        // outlive the page.
        await useApp.getState().saveTab(id, {
          auto: reason !== "manual",
          unload: reason === "close",
        });
      },
      [activePageID, id, saveSubpage],
    ),
  });

  // Hot exit, now only for the content auto-save is not allowed to write. While
  // a save can land, the file itself is the draft; mirroring it into
  // `.vised/drafts` as well would double every write for no extra safety.
  useEffect(() => {
    if (!editorDirty || !saveBlocked || activePageID) return;
    const timer = setTimeout(() => void saveDraft(id), 1500);
    return () => clearTimeout(timer);
  }, [activePageID, editorDirty, saveBlocked, tab?.content, id, saveDraft]);

  const popoutPage = useCallback(() => {
    const page = activePageID ?? "main";
    const url = `/popout?node=${encodeURIComponent(id)}&page=${encodeURIComponent(page)}`;
    window.open(
      url,
      `nodevas-${id}-${page}`,
      "popup,width=1100,height=820,resizable=yes,scrollbars=yes",
    );
  }, [activePageID, id]);

  // Ctrl/⌘ + S is now "save now" rather than "save at all". It is still worth
  // keeping: it is how someone forces the document out before doing something
  // they are not sure about, and — because the document saves itself on a timer
  // and a lifecycle status deliberately does not [B-04] — it is one of the two
  // ways to apply a staged status, the other being the panel's own button.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        e.stopPropagation();
        // Committed here rather than left to saveTab, which would skip it on a
        // document with nothing outstanding — and a status waiting to be
        // applied is the most likely reason to press this on a clean document.
        void useApp.getState().commitStagedLifecycle(id).catch(reportError);
        void autosave.flush("manual").catch(reportError);
      }
    },
    [autosave, id],
  );

  // ---- toolbar operations (CodeMirror dispatch) ----
  const view = () => cmRef.current?.view;

  const [linkPickerOpen, setLinkPickerOpen] = useState(false);

  const insertAtCaret = useCallback((snippet: string) => {
    insertSnippet(view(), snippet);
  }, []);

  const attachments = useAttachments({
    nodeId: id,
    format: activeFormat,
    insertSnippet: insertAtCaret,
  });

  const tables = useTableEditing(view);

  const markdownTools = editsAsMarkdown(activeFormat);

  const openNodeLinkTarget = useCallback(
    (target: { project: string; nodeId: string }) => {
      void openNodeLink(target).catch(reportError);
    },
    [openNodeLink],
  );

  const links = useApp((s) => s.graph?.nodes?.find((node) => node.id === id)?.links) ?? [];
  // What file the editor is actually writing to, shown above the toolbar so a
  // document is never anonymous.
  const activeProject = useApp((s) => s.activeProject);

  const extensions = useEditorExtensions({
    format: activeFormat,
    editorMode,
    links,
    currentProject: activeProject,
    onOpenNodeLink: openNodeLinkTarget,
  });

  // While a session is live the document belongs to the CRDT, not to React.
  // The editor is remounted on that boundary and handed the shared text, and
  // `value` is frozen at whatever was on screen when it happened: re-setting a
  // controlled editor from the outside on every remote keystroke moves the
  // caret and drops what was typed in between, which is exactly the interleaved
  // mess it is supposed to prevent.
  const liveEditing =
    Boolean(liveDoc.ytext) &&
    (!activePageID || activeFormat === "md" || activeFormat === "txt");
  const liveExtensions = useMemo(
    () =>
      liveEditing && liveDoc.ytext
        ? [ySyncFacet.of(new YSyncConfig(liveDoc.ytext, null)), ySync]
        : [],
    [liveDoc.ytext, liveEditing],
  );
  const editorExtensions = useMemo(
    () => [...extensions, ...liveExtensions],
    [extensions, liveExtensions],
  );
  // ySync only forwards deltas, so the editor has to open on whatever the
  // shared text already says. Captured once per session and then left alone:
  // a `value` prop that keeps changing is the controlled behaviour being
  // avoided here.
  const liveMountText = useRef<string | null>(null);
  if (!liveEditing) liveMountText.current = null;
  else if (liveMountText.current === null) liveMountText.current = liveDoc.ytext?.toString() ?? "";

  const syncEditorView = useCallback(
    (view: EditorView | null) => {
      if (pageBusy || liveEditing || !view) return;
      if (view.state.doc.toString() === editorBody) return;
      syncingEditorRef.current = true;
      try {
        view.dispatch({
          changes: {
            from: 0,
            to: view.state.doc.length,
            insert: editorBody,
          },
        });
      } finally {
        syncingEditorRef.current = false;
      }
    },
    [editorBody, liveEditing, pageBusy],
  );
  useEffect(() => {
    const timer = setTimeout(() =>
      syncEditorView(cmRef.current?.view ?? null),
      0,
    );
    return () => clearTimeout(timer);
  }, [activeFormat, editorBody, liveEditing, pageBusy, syncEditorView]);


  const documentPath = activePageID
    ? `nodes/${id}.pages/${activePageID}${
        PAGE_FORMATS.find(([format]) => format === activeFormat)?.[2] ?? ".md"
      }`
    : `nodes/${id}.md`;

  // After a history restore the file on disk is a different one: reload
  // whichever document the editor is showing.
  const reloadDocument = useCallback(async () => {
    if (!activePageID) {
      await useApp.getState().notifyDiskChange(id);
      return;
    }
    const response = await api.getNodePage(id, activePageID);
    setPageDoc({
      id: activePageID,
      content: response.content,
      rev: response.rev,
      format: response.format ?? "md",
      dirty: false,
      loading: false,
      conflict: null,
    });
  }, [activePageID, id]);

  const jumpToLine = useCallback((target: { line: number; from: number }) => {
    const view = cmRef.current?.view;
    if (!view) return;
    const position = Math.min(target.from, view.state.doc.length);
    view.dispatch({
      selection: { anchor: position },
      effects: EditorView.scrollIntoView(position, { y: "start" }),
    });
    view.focus();
  }, []);

  useEffect(() => {
    onDirtyChange?.(Boolean(editorDirty));
  }, [editorDirty, onDirtyChange]);
  // The popout's save button. Explicit, so it applies a staged status too.
  useEffect(() => {
    if (handledSaveRequest.current === saveRequest) return;
    handledSaveRequest.current = saveRequest;
    void useApp.getState().commitStagedLifecycle(id).catch(reportError);
    void autosave.flush("manual").catch(reportError);
  }, [autosave, id, saveRequest]);
  const onEditorChange = useCallback(
    (value: string) => {
      if (syncingEditorRef.current || pageBusy) return;
      if (!activePageID) {
        onBodyChange(value);
        return;
      }
      setPageDoc((current) =>
        current?.id === activePageID
          ? { ...current, content: value, dirty: true, conflict: null }
          : current,
      );
    },
    [activePageID, onBodyChange, pageBusy],
  );

  if (!tab) return null;
  if (!tab.loaded) return <div className="tab-body loading">載入中</div>;

  return (
    <div className="tab-body" onKeyDown={onKeyDown}>
      {tab.draftPending !== null && (
        <div className="banner banner-draft">
          <span>找到未儲存的草稿(上次未正常結束)</span>
           <button onClick={() => void applyDraft(id, true).catch(reportError)}>還原草稿</button>
           <button onClick={() => void applyDraft(id, false).catch(reportError)}>捨棄</button>
        </div>
      )}
      {tab.diskChanged && !tab.conflict && (
        <div className="banner banner-warn">
          <span>檔案在磁碟上被修改了;儲存時會進行版本比對</span>
        </div>
      )}
      {tab.conflict && (
        <div className="banner banner-conflict">
          <div className="banner-text">
            <b>版本衝突</b>：磁碟上已有較新版本。兩個版本都不會遺失，磁碟版有歷史快照，你的版本已存為草稿。
          </div>
          <div className="banner-actions">
             <button onClick={() => void resolveConflict(id, "disk").catch(reportError)}>載入磁碟版</button>
             <button onClick={() => void resolveConflict(id, "mine").catch(reportError)}>以我的版本覆蓋</button>
          </div>
          <details>
            <summary>檢視磁碟版</summary>
            <pre className="conflict-preview">{tab.conflict.diskContent}</pre>
          </details>
        </div>
      )}

      {/* One disabled fieldset over every metadata control, rather than a
        * canEdit prop threaded into each panel. The browser disables every
        * input, select and button underneath it, including ones added to
        * these panels later — which is the same deny-by-default reasoning the
        * server's middleware gate uses. The server refuses these writes
        * regardless; this only stops the UI offering what will be refused. */}
      <details
        className="drawer-meta-band"
        open={metaOpen}
        onToggle={(event) => setMetaOpen(event.currentTarget.open)}
      >
        {/* The summary is drawn only at phone width (05-drawer.css); on a
          * desktop drawer this band is always open and the two panels look
          * exactly as they did before it existed. */}
        <summary>基本資料與實際狀態</summary>
        <fieldset className="readonly-gate" disabled={!canEdit}>
          <NodeMetaForm id={id} />
          <LifecyclePanel id={id} />
        </fieldset>
      </details>

      <div className="node-view-tabs" role="tablist" aria-label="節點資訊">
        <button
          type="button"
          role="tab"
          aria-selected={drawerView === "content"}
          className={drawerView === "content" ? "active" : ""}
          onClick={() => setDrawerView("content")}
        >
          內容
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={drawerView === "relations"}
          className={drawerView === "relations" ? "active" : ""}
          onClick={() => setDrawerView("relations")}
        >
          關係圖
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={drawerView === "timeline"}
          className={drawerView === "timeline" ? "active" : ""}
          onClick={() => setDrawerView("timeline")}
        >
          時間軸
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={drawerView === "appearance"}
          className={drawerView === "appearance" ? "active" : ""}
          onClick={() => setDrawerView("appearance")}
        >
          外觀
        </button>
      </div>

      <fieldset className="readonly-gate" disabled={!canEdit}>
        {drawerView === "relations" && <NodeRelations id={id} />}
        {drawerView === "timeline" && <NodeTimeline id={id} />}
        {drawerView === "appearance" && <NodeAppearance id={id} />}
      </fieldset>

      <div className="node-content-view" hidden={drawerView !== "content"}>
        <NodeHistory
          id={id}
          path={documentPath}
          format={activeFormat}
          onRestored={reloadDocument}
        />

        {/* Not fieldset-gated: the page tabs inside are how a reader moves
          * between subpages. The component hides its own mutators instead. */}
        <SubpageControls pages={subpages} onPopout={popoutPage} canEdit={canEdit} />

        <nav className="editor-path" aria-label="檔案位置">
          {activeProject && (
            <>
              <span className="editor-path-project">{activeProject}</span>
              <span className="editor-path-sep" aria-hidden>
                /
              </span>
            </>
          )}
          <span className="editor-path-file mono" title={documentPath}>
            {documentPath}
          </span>
          {editorDirty && <span className="editor-path-dirty">編輯中</span>}
        </nav>

        {/* The formatting toolbar's whole job is inserting text, and the
          * editor below refuses insertions. font size, outline and the
          * preview toggle live in it too, which is a real loss for a reader —
          * accepted for now over teaching the toolbar which of its buttons
          * write. */}
        {canEdit && (
          <EditorToolbar
            markdownTools={markdownTools}
            view={view}
            onInsertNodeLink={() => setLinkPickerOpen(true)}
            attachments={attachments}
            tables={tables}
            fontSize={fontSize}
            setFontSize={setFontSize}
            outlineOpen={outlineOpen}
            toggleOutline={toggleOutline}
            editorMode={editorMode}
            setEditorMode={setEditorMode}
            nodeId={id}
            pageId={activePageID ?? "main"}
            content={editorBody}
          />
        )}

        {canEdit && markdownTools && !preview && <TableToolbar tables={tables} />}

        <div className={`editor-stage${outlineOpen ? " with-outline" : ""}`}>
        {outlineOpen && markdownTools && (
          <DocumentOutline
            source={editorBody}
            activeLine={caretLine}
            onJump={jumpToLine}
          />
        )}
        {heldByOther && lock && (
          <div className="editor-lock-banner">
            <span>
              {lock.actor.name || "另一位使用者"} 正在編輯這份文件，目前為唯讀。
            </span>
            <button type="button" onClick={() => requestLock(id, true)}>
              強制編輯
            </button>
          </div>
        )}
        {peers.length > 0 && (
          <div className="editor-presence">
            {peers.map((peer) => (
              <span key={peer.id} className="editor-presence-peer" title={peer.actor.name}>
                {(peer.actor.name || "?").slice(0, 2)}
              </span>
            ))}
          </div>
        )}
        {linkPickerOpen && (
          <NodeLinkPicker
            excludeNodeId={id}
            onClose={() => setLinkPickerOpen(false)}
            onPick={(target) => {
              insertNodeLink(view(), target, activeProject);
              setLinkPickerOpen(false);
            }}
          />
        )}
        {pageDoc?.loading ? (
          <div className="content-page-editor-loading">正在開啟子頁…</div>
        ) : preview ? (
          <DocumentPreview
            content={editorBody}
            format={activeFormat}
            fontSize={fontSize}
            onOpenNodeLink={openNodeLinkTarget}
          />
        ) : (
          <CodeMirror
            // Remounted when a live session starts or ends: the document
            // changes owner at that moment, and CodeMirror cannot be handed a
            // new one in place.
            key={`${activePageID ?? "main"}:${activeFormat}${liveEditing ? ":live" : ""}`}
            ref={cmRef}
            className="editor"
            style={{ fontSize }}
            value={liveEditing ? (liveMountText.current ?? "") : editorBody}
            height="100%"
            theme={theme}
            readOnly={(liveDoc.ready ? false : heldByOther) || !canEdit || pageBusy}
            extensions={editorExtensions}
            onCreateEditor={syncEditorView}
            onChange={onEditorChange}
            // Leaving the field is a person saying they are done with it, and
            // it is the one save event a laptop lid closing cannot beat.
            onBlur={() => void autosave.flush("blur").catch(reportError)}
            onUpdate={(update) => {
              if (!update.selectionSet && !update.docChanged) return;
              const { anchor, head } = update.state.selection.main;
              setCaretLine(update.state.doc.lineAt(head).number);
              tables.syncTable(update.state);
              publishSelection(anchor, head);
            }}
          />
        )}
        </div>
        {/*
          Telling somebody their work saves itself is worthless unless they can
          see that it did. The badge is the same OperationStatus every other
          mutation reports through, so 儲存中 / 已儲存 / 儲存失敗 read the same
          here as everywhere else, and a failure stays on screen — with the
          reason and a way to try again — for as long as it is unresolved.
        */}
        {!canEdit && (
          <div className="tab-footer">
            <span className="tab-footer-hint">唯讀：訪客無法編輯或將變更寫回伺服器</span>
          </div>
        )}
        {canEdit && (
        <div className="tab-footer">
          <OperationStatus
            status={saveState.status}
            message={
              saveState.status === "error" || saveState.status === "conflict"
                ? saveState.message
                : undefined
            }
          />
          {saveState.status === "error" && (
            <>
              <span className="tab-footer-retry">自動重試中</span>
              <button
                type="button"
                onClick={() => void autosave.flush("manual").catch(reportError)}
              >
                立即重試
              </button>
            </>
          )}
          <span className="tab-footer-hint">
            {editorDirty ? "編輯中，將自動儲存" : "自動儲存"}
            <kbd>Ctrl</kbd>/<kbd>⌘</kbd>+<kbd>S</kbd> 立即儲存
          </span>
        </div>
        )}
      </div>
    </div>
  );
}
