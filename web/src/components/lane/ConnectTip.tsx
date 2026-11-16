/**
 * The board's one passive hint on how to make a node wait for another.
 *
 * Two new nodes used to be met by a Problems badge ("not connected to
 * anything") and by nothing that said how to connect them. While the graph on
 * show has two or more nodes that carry a status and no dependency at all,
 * this one line says it: drag the dot on a card's edge onto the card that
 * should wait. A line over the board's corner, not a dialog and not a tour
 * step — it is there to be read in passing, or ignored.
 *
 * It goes for good once this person has made a dependency, by whichever route
 * (noteConnectionMade), or has closed it: a tip that came back with every new
 * project would be one more thing to dismiss.
 */

import { nodeTypeOf } from "../../domain/graph/nodeTypes";
import { useI18n } from "../../i18n";
import { IconClose } from "../../icons";
import { useApp, usePreference } from "../../store";
import type { Graph } from "../../types";
import { useCoarsePointer } from "../touch";

/**
 * Whether the graph is one the tip has something to say about: at least two
 * nodes that can wait for each other — a Note has no status and never waits —
 * and nothing connected yet.
 */
export function wantsConnectTip(graph: Graph | null): boolean {
  if (!graph || (graph.edges?.length ?? 0) > 0) return false;
  let waiting = 0;
  for (const node of graph.nodes ?? []) {
    if (nodeTypeOf(graph, node).completion === "none") continue;
    waiting += 1;
    if (waiting >= 2) return true;
  }
  return false;
}

/**
 * Called wherever this person makes a dependency: they know how now, so the
 * tip is done with. Recorded at the act rather than read off the graph, where
 * an example project opened to look around would have dismissed it unread.
 */
export function noteConnectionMade(): void {
  const { preferences, updateUIPreference } = useApp.getState();
  if (!preferences.graphConnectTipDismissed) {
    updateUIPreference("graphConnectTipDismissed", true);
  }
}

/** `graph` is the graph as the board reads it, implied wires included. */
export function ConnectTip({ graph }: { graph: Graph | null }) {
  const { t } = useI18n();
  const dismissed = usePreference("graphConnectTipDismissed");
  const updateUIPreference = useApp((state) => state.updateUIPreference);
  // The dot shows under a mouse pointer; a finger has to select the card first.
  const coarsePointer = useCoarsePointer();
  if (dismissed || !wantsConnectTip(graph)) return null;
  return (
    <div className="board-connect-tip" role="note">
      <span>{coarsePointer ? t("canvas.connectTipTouch") : t("canvas.connectTip")}</span>
      <button
        type="button"
        aria-label={t("canvas.connectTipDismiss")}
        title={t("canvas.connectTipDismiss")}
        onClick={() => updateUIPreference("graphConnectTipDismissed", true)}
      >
        <IconClose size={12} />
      </button>
    </div>
  );
}
