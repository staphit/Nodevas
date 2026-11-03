import { openNodeDetail } from "../components/drawer/openNodeDetail";
import { useExplorer } from "./explorerStore";

/**
 * Opens a node's panel from the Explorer. The panel lives on the board,
 * which the Explorer's main view replaces, so the board comes back first.
 */
export function openNodeOnBoard(nodeId: string): void {
  useExplorer.getState().close();
  openNodeDetail(nodeId);
}
