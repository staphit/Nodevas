import type { ExplorerTab } from "./explorerStore";

export interface ExplorerRailProps {
  tab: ExplorerTab;
  onTab: (tab: ExplorerTab) => void;
}

/** The four tabs: Nodes, Files, Git, Agent. */
export default function ExplorerRail(_props: ExplorerRailProps) {
  // TODO(green): implement.
  return null;
}
