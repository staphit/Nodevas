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
import { translate } from "../../i18n";
import { useApp } from "../../store";
import type { NodeClipboard } from "../../state/types";
import type { Graph } from "../../types";
import { confirmAction } from "../ConfirmDialog";
import { pickProject } from "../ProjectPickerDialog";

const modeLabel = (mode: NodeTransferMode) =>
  translate(mode === "copy" ? "batch.copy" : "batch.move");

/** Node titles for the messages, falling back to the id. */
export function nodeLabels(graph: Graph | null, ids: string[]): string[] {
  return ids.map(
    (id) => graph?.nodes?.find((node) => node.id === id)?.title || id,
  );
}

function describeSelection(labels: string[]): string {
  if (labels.length === 1) {
    return translate("batch.selectionOne", undefined, { label: labels[0] });
  }
  return translate("batch.selectionMany", undefined, {
    label: labels[0],
    count: labels.length,
  });
}

/** Shows what a completed transfer could not bring along. */
async function reportWarnings(result: NodeTransferResult): Promise<void> {
  if (!result.warnings?.length) return;
  await confirmAction({
    title: translate("batch.transferWarningsTitle"),
    description: result.warnings.join("\n"),
    confirmLabel: translate("common.confirm"),
    cancelLabel: translate("common.close"),
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
    title: translate("batch.transferTitle", undefined, { mode: modeLabel(mode) }),
    description: translate("batch.transferDescription", undefined, {
      selection: describeSelection(labels),
      mode: modeLabel(mode),
    }),
    confirmLabel: modeLabel(mode),
    exclude: [activeProject],
  });
  if (!target) return null;
  if (mode === "cut") {
    const confirmed = await confirmAction({
      title: translate("batch.moveTitle", undefined, { count: ids.length }),
      description: translate("batch.moveDescription", undefined, {
        selection: describeSelection(labels),
        target,
      }),
      confirmLabel: translate("batch.move"),
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
    throw new Error(translate("batch.sameProjectError"));
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
