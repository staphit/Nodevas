import { create } from "zustand";

import { findPageHoldingMatch } from "../../state/pageHoldingMatch";
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

/** A request for the panel to open one subpage of one node, held the same way. */
type PageRequest = { nodeId: string; pageId: string; nonce: number };

export const useNodeDetailView = create<{
  request: DetailViewRequest | null;
  requestView: (nodeId: string, view: NodeDetailView) => void;
  clearRequest: () => void;
  pageRequest: PageRequest | null;
  requestPage: (nodeId: string, pageId: string) => void;
  clearPageRequest: () => void;
}>((set) => ({
  request: null,
  requestView: (nodeId, view) =>
    set((state) => ({
      request: { nodeId, view, nonce: (state.request?.nonce ?? 0) + 1 },
    })),
  clearRequest: () => set({ request: null }),
  pageRequest: null,
  requestPage: (nodeId, pageId) =>
    set((state) => ({
      pageRequest: { nodeId, pageId, nonce: (state.pageRequest?.nonce ?? 0) + 1 },
    })),
  clearPageRequest: () => set({ pageRequest: null }),
}));

// A request belongs to the project it was made in: another project's node of
// the same id must not open on that view later.
useApp.subscribe((state, previous) => {
  if (state.activeProject !== previous.activeProject) {
    useNodeDetailView.getState().clearRequest();
    useNodeDetailView.getState().clearPageRequest();
  }
});

/**
 * Opens the subpage that holds a search hit, when the node's main page does
 * not. Search names no page, so a hit only a subpage holds used to land on
 * the main page, where the match is nowhere on screen. Called once the node's
 * tab is open: a hit on the title or the main page stays put. A failed read
 * leaves the main page, which is where the jump lands anyway.
 */
export async function openPageHoldingMatch(nodeId: string, title: string, query: string): Promise<void> {
  const needle = query.trim().toLowerCase();
  if (!needle || title.toLowerCase().includes(needle)) return;
  const tab = useApp.getState().tabs.find((entry) => entry.id === nodeId);
  if (tab?.content.toLowerCase().includes(needle)) return;
  const pageId = await findPageHoldingMatch(nodeId, needle).catch(() => null);
  if (pageId) useNodeDetailView.getState().requestPage(nodeId, pageId);
}

/**
 * Opens a node's detail panel on one of its views — the board's
 * "Add dependency…" lands on Dependencies, a run notice on Activity. Without a
 * view it is the plain open, which shows the document. If the node does not
 * open, the request goes with it, so it cannot fire on a later open.
 */
export function openNodeDetail(nodeId: string, view: NodeDetailView = "content"): void {
  useNodeDetailView.getState().requestView(nodeId, view);
  const nonce = useNodeDetailView.getState().request?.nonce;
  void useApp
    .getState()
    .openTab(nodeId)
    .catch((error: unknown) => {
      // Only this request: a newer one may have been made meanwhile.
      if (useNodeDetailView.getState().request?.nonce === nonce) useNodeDetailView.getState().clearRequest();
      reportError(error);
    });
}
