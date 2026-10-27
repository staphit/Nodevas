import type { ExplorerView } from "./explorerStore";

export interface FileViewProps {
  view: Extract<ExplorerView, { kind: "file" }>;
}

/** One file, read-only. */
export default function FileView(_props: FileViewProps) {
  // TODO(green): implement.
  return null;
}
