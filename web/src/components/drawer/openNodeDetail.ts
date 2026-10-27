import { create } from "zustand";

import { reportError, useApp } from "../../store";

/** The three views of a node's detail panel. */
export type NodeDetailView = "content" | "dependencies" | "activity";

/**
 * A request for the panel to show one view of one node. Held here rather than
 * in the panel because the panel may not exist yet: the body that shows the
 * node consumes it when it mounts, or straight away when it already shows it.
 * The nonce makes asking twice for the same view count as a second request.
 */
type DetailViewRequest = { nodeId: string; view: NodeDetailView; nonce: number };

export const useNodeDetailView = create<{
  request: DetailViewRequest | null;
  requestView: (nodeId: string, view: NodeDetailView) => void;
  clearRequest: () => void;
}>((set) => ({
  request: null,
  requestView: (nodeId, view) =>
    set((state) => ({
      request: { nodeId, view, nonce: (state.request?.nonce ?? 0) + 1 },
    })),
  clearRequest: () => set({ request: null }),
}));

/**
 * Opens a node's detail panel on one of its views — the board's
 * "Add dependency…" lands on Dependencies, a run notice on Activity. Without a
 * view it is the plain open, which shows the document.
 */
export function openNodeDetail(nodeId: string, view: NodeDetailView = "content"): void {
  useNodeDetailView.getState().requestView(nodeId, view);
  void useApp.getState().openTab(nodeId).catch(reportError);
}
