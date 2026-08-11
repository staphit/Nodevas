/**
 * Moving nodes between projects [B-06].
 *
 * Two gestures share this module so they cannot drift apart:
 *
 * - "複製到專案…／移動到專案…" picks the destination up front.
 * - Ctrl/⌘+C / X then V stashes a selection and pastes it into whichever
 *   project is open when you press V.
 *
 * Node ids only mean something inside their own project, so the server issues
 * new ones and answers with the mapping. Anything it could not bring along
 * comes back as a warning: the transfer happened, and the user is told what
 * was left out instead of discovering it later.
 */

import type { NodeTransferMode, NodeTransferResult } from "../../api";
import { useApp } from "../../store";
import type { NodeClipboard } from "../../state/types";
import type { Graph } from "../../types";
import { confirmAction } from "../ConfirmDialog";
import { pickProject } from "../ProjectPickerDialog";

const MODE_LABEL: Record<NodeTransferMode, string> = {
  copy: "複製",
  cut: "移動",
};

/** Node titles for the messages, falling back to the id. */
export function nodeLabels(graph: Graph | null, ids: string[]): string[] {
  return ids.map(
    (id) => graph?.nodes?.find((node) => node.id === id)?.title || id,
  );
}

function describeSelection(labels: string[]): string {
  if (labels.length === 1) return `「${labels[0]}」`;
  return `「${labels[0]}」等 ${labels.length} 個節點`;
}

/** Shows what a completed transfer could not bring along. */
async function reportWarnings(result: NodeTransferResult): Promise<void> {
  if (!result.warnings?.length) return;
  await confirmAction({
    title: "已完成，但有內容未一併帶過",
    description: result.warnings.join("\n"),
    confirmLabel: "知道了",
    cancelLabel: "關閉",
  });
}

/**
 * Asks for a destination project and performs the transfer. Returns null when
 * the user backs out — callers must not treat that as a failure.
 */
export async function sendNodesToProject(
  ids: string[],
  labels: string[],
  mode: NodeTransferMode,
): Promise<NodeTransferResult | null> {
  const { activeProject, transferNodes } = useApp.getState();
  if (ids.length === 0) return null;
  const target = await pickProject({
    title: `${MODE_LABEL[mode]}到其他專案`,
    description: `將 ${describeSelection(labels)} ${MODE_LABEL[mode]}到選定的專案。`,
    confirmLabel: MODE_LABEL[mode],
    exclude: [activeProject],
  });
  if (!target) return null;
  if (mode === "cut") {
    const confirmed = await confirmAction({
      title: `移動 ${ids.length} 個節點`,
      description:
        `${describeSelection(labels)} 會複製到「${target}」，` +
        "並從目前專案移到垃圾桶（可從專案總管還原）。",
      confirmLabel: "移動",
    });
    if (!confirmed) return null;
  }
  const result = await transferNodes({ ids, target, mode, source: activeProject });
  await reportWarnings(result);
  return result;
}

/** Remembers a selection until it is pasted or replaced. */
export function stashNodes(
  ids: string[],
  labels: string[],
  mode: NodeTransferMode,
): NodeClipboard | null {
  if (ids.length === 0) return null;
  const { activeProject, setNodeClipboard } = useApp.getState();
  const clipboard: NodeClipboard = {
    project: activeProject,
    ids: [...ids],
    labels,
    mode,
  };
  setNodeClipboard(clipboard);
  return clipboard;
}

/**
 * Pastes the stash into the project currently open. A cut clears the stash
 * afterwards — its originals no longer exist, so a second paste would fail.
 */
export async function pasteNodeClipboard(): Promise<NodeTransferResult | null> {
  const { nodeClipboard, activeProject, transferNodes, setNodeClipboard } =
    useApp.getState();
  if (!nodeClipboard || nodeClipboard.ids.length === 0) return null;
  if (nodeClipboard.project === activeProject) {
    // The nodes are already here. Copying a node within one project is
    // "建立副本"; this gesture is for crossing projects.
    throw new Error("剪貼簿的節點就在目前專案；請切換到其他專案再貼上，或用「建立副本」");
  }
  const result = await transferNodes({
    ids: nodeClipboard.ids,
    target: activeProject,
    mode: nodeClipboard.mode,
    source: nodeClipboard.project,
  });
  if (nodeClipboard.mode === "cut") setNodeClipboard(null);
  await reportWarnings(result);
  return result;
}
