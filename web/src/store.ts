/**
 * Store facade.
 *
 * The implementation lives in `state/*Slice.ts` [A-05]; this file composes them
 * into the single `useApp` hook every component already imports, and exports the
 * selectors UI should prefer over reaching into state by hand.
 */

import { useMemo } from "react";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { RemoteCaret } from "./editor/remoteCarets";
import {
  readPreferences,
  subscribePreferences,
  type PreferenceKey,
  type PreferenceValue,
} from "./preferences";
import { createCollabSlice } from "./state/collabSlice";
import { createDocumentSlice } from "./state/documentSlice";
import { createFolderSlice } from "./state/folderSlice";
import { createGraphSlice } from "./state/graphSlice";
import { createOperationsSlice } from "./state/operationsSlice";
import {
  createPreferencesSlice,
  preferencesEqual,
  syncDerivedPreferences,
} from "./state/preferencesSlice";
import { createRemoteSlice } from "./state/remoteSlice";
import { createRunSlice } from "./state/runSlice";
import { createUndoSlice, undoMirror } from "./state/undoSlice";
import { createWorkspaceSlice } from "./state/workspaceSlice";
import { selectOperation, type OperationScope, type OperationState } from "./state/operations";
import { observeUndo } from "./state/undo";
import type { AppState } from "./state/types";
import type { Graph, GraphNode } from "./types";

export const useApp = create<AppState>((...args) => ({
  ...createOperationsSlice(...args),
  ...createPreferencesSlice(...args),
  ...createWorkspaceSlice(...args),
  ...createDocumentSlice(...args),
  ...createRunSlice(...args),
  ...createGraphSlice(...args),
  ...createCollabSlice(...args),
  ...createFolderSlice(...args),
  ...createRemoteSlice(...args),
  ...createUndoSlice(...args),
}));

// Undo depth lives outside the store (it holds graph snapshots); mirror just
// the summary the UI needs [A-05].
observeUndo(() => useApp.setState(undoMirror()));

// Preferences can also change in another tab or in the popout editor window.
// Adopt those writes instead of letting the two windows drift apart [A-03].
subscribePreferences(() => {
  const values = readPreferences();
  if (preferencesEqual(useApp.getState().preferences, values)) return;
  useApp.setState({ preferences: values });
  syncDerivedPreferences(useApp.setState, useApp.getState);
});

// ---------------------------------------------------------------------------
// Selectors
// ---------------------------------------------------------------------------

/** One local preference value [A-03]. */
export function usePreference<K extends PreferenceKey>(key: K): PreferenceValue<K> {
  return useApp((state) => state.preferences[key]);
}

/**
 * Current state of one mutation scope [A-02]. Returns the shared idle value
 * when the scope has not been touched, so components need no busy/error flags
 * of their own. `scope` comes from `operationScope.*`.
 */
export function useOperation(scope: OperationScope): OperationState {
  return useApp((state) => selectOperation(state.operations, scope));
}

/** Newest transition across every scope; for a single global status line. */
export function useLastOperation() {
  return useApp((state) => state.lastOperation);
}

/** True while the given scope is saving. */
export function useOperationPending(scope: OperationScope): boolean {
  return useApp((state) => selectOperation(state.operations, scope).status === "pending");
}

/**
 * Whether Ctrl/⌘+Z would do something [A-06]. False also means "do not show an
 * undo affordance" — an operation that cannot be reverted never enters the
 * stack, so this is never optimistic.
 */
export function useCanUndo(): boolean {
  return useApp((state) => state.canUndo);
}

/** What Ctrl/⌘+Z would revert, in UI wording. */
export function useUndoLabel(): string | null {
  return useApp((state) => state.undoLabel);
}

/** Whether Ctrl/⌘+Shift+Z would re-apply an undone step [A-06]. */
export function useCanRedo(): boolean {
  return useApp((state) => state.canRedo);
}

/** What Ctrl/⌘+Shift+Z would re-apply, in UI wording. */
export function useRedoLabel(): string | null {
  return useApp((state) => state.redoLabel);
}

/** The lock on a document, or null when nobody holds it [P2]. */
export function useNodeLock(nodeId: string | null) {
  return useApp((state) =>
    nodeId ? (state.locks.find((lock) => lock.nodeId === nodeId) ?? null) : null,
  );
}

/**
 * Who else has this document on screen [P2].
 *
 * The filter builds a fresh array on every read, so it goes through
 * `useShallow`: without it each store read looks like a change to
 * useSyncExternalStore and the subscriber re-renders forever.
 */
export function usePeersOnNode(nodeId: string | null) {
  return useApp(
    useShallow((state) =>
      nodeId
        ? state.peers.filter((peer) => peer.id !== state.selfPeerID && peer.nodeId === nodeId)
        : [],
    ),
  );
}

/**
 * The carets other people have in this document, ready for the editor.
 *
 * Peers on another node, or on a different subpage of this one, are left out:
 * their offsets mean nothing here, and drawing them would put somebody else's
 * cursor in the middle of your paragraph.
 */
export function useRemoteCarets(nodeId: string | null, pageId: string | null) {
  // Selected raw and assembled in a memo, not built inside the selector: a
  // selector that mints fresh objects every call never compares equal, and
  // useSyncExternalStore answers that by re-rendering forever.
  const peers = useApp(useShallow((state) => state.peers));
  const presences = useApp(useShallow((state) => state.presences));
  const selfPeerID = useApp((state) => state.selfPeerID);
  return useMemo(() => {
    if (!nodeId) return [];
    const carets: RemoteCaret[] = [];
    for (const peer of peers) {
      if (peer.id === selfPeerID) continue;
      const presence = presences[peer.id];
      if (!presence?.selection) continue;
      if (presence.nodeId !== nodeId) continue;
      if ((presence.pageId ?? null) !== pageId) continue;
      carets.push({
        peerId: peer.id,
        name: peer.actor?.name || "?",
        color: peer.color || "#888",
        anchor: presence.selection.anchor,
        head: presence.selection.head,
      });
    }
    return carets;
  }, [peers, presences, selfPeerID, nodeId, pageId]);
}

export function reportError(error: unknown): void {
  useApp.setState({
    error: error instanceof Error ? error.message : "發生未預期的錯誤",
  });
}

export function nodeById(graph: Graph | null, id: string): GraphNode | undefined {
  return graph?.nodes?.find((n) => n.id === id) ?? undefined;
}

// Expression helpers moved to the domain layer; re-exported so the components
// that already import them from the store keep working [A-04].
export { editableGateOperator, topLevelOp } from "./domain";

// ---------------------------------------------------------------------------
// Re-exports: components import these from the store they already depend on.
// ---------------------------------------------------------------------------

export { operationScope } from "./state/operations";
export { driveReady, hasEverBackedUp, remoteScope, sortBundles } from "./state/remoteSlice";
export type { RemoteConfigInput } from "./state/types";
export type {
  CommandResult,
  OperationScope,
  OperationState,
  OperationStatus,
} from "./state/operations";
export type {
  CanvasCommand,
  GraphCommand,
  NodeCommand,
  PlanCommand,
  WorkflowCommand,
} from "./domain";
export type { PreferenceKey, PreferenceScope, PreferenceValues } from "./preferences";
export type { AppState, Tab, Theme } from "./state/types";
