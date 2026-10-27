import type { ExplorerView } from "./explorerStore";

export interface DiffViewProps {
  view: Extract<ExplorerView, { kind: "diff" }>;
}

/** A run's, a workspace commit's or a repo's patch. */
export default function DiffView(_props: DiffViewProps) {
  // TODO(green): implement.
  return null;
}
