/**
 * Browser-side UI tools (COPILOT.md, "Client tools").
 *
 * Each one goes through an existing `useApp` action — the same entry point the
 * UI's own controls use — so the copilot can do nothing a click could not.
 *
 * Declared to the server: only the tools with a clean store equivalent.
 * - `ui_focus_nodes`: the selection and the canvas viewport are LaneView-local
 *   state; the store only offers `revealNode` for a single node, which cannot
 *   select several nodes or fit the canvas to them.
 * - `ui_set_filter`: the board filter lives in `useGraphViews` component state,
 *   with no store action to set it.
 */

import type { CopilotClientTool, CopilotView } from "../api";
import { useApp } from "../store";
import type { AppState } from "../state/types";

export const SUPPORTED_CLIENT_TOOLS: CopilotClientTool[] = [
  "ui_open_node",
  "ui_set_view",
  "ui_switch_project",
];

export interface ClientToolResult {
  output: string;
  isError: boolean;
  /** The browser moved to another project: the conversation ends with the turn. */
  switchedProject?: boolean;
}

type StoreAccess = () => Pick<
  AppState,
  | "graph"
  | "projects"
  | "activeProject"
  | "paneOpen"
  | "openTab"
  | "revealNode"
  | "togglePane"
  | "switchProject"
>;

const ok = (output: string): ClientToolResult => ({ output, isError: false });
const fail = (output: string): ClientToolResult => ({ output, isError: true });

function asRecord(input: unknown): Record<string, unknown> {
  if (typeof input === "string") {
    try {
      return asRecord(JSON.parse(input));
    } catch {
      return {};
    }
  }
  return input && typeof input === "object" ? (input as Record<string, unknown>) : {};
}

function stringField(input: Record<string, unknown>, key: string): string {
  const value = input[key];
  return typeof value === "string" ? value.trim() : "";
}

export async function runClientTool(
  name: string,
  rawInput: unknown,
  store: StoreAccess = () => useApp.getState(),
): Promise<ClientToolResult> {
  const input = asRecord(rawInput);
  try {
    switch (name) {
      case "ui_open_node": {
        const id = stringField(input, "id");
        if (!id) return fail("missing id");
        const state = store();
        const node = state.graph?.nodes?.find((candidate) => candidate.id === id);
        if (!node) return fail(`node ${id} not found in project ${state.activeProject}`);
        await state.openTab(id);
        // The graph pane answers a reveal by selecting the node and scrolling to it.
        state.revealNode(id);
        return ok(`opened ${id}${node.title ? ` (${node.title})` : ""}`);
      }
      case "ui_set_view": {
        const view = stringField(input, "view") as CopilotView;
        // The board is the graph pane's canvas; there is no third pane.
        const target = view === "board" ? "graph" : view;
        if (target !== "graph" && target !== "timeline") return fail(`unknown view ${view}`);
        const other = target === "graph" ? "timeline" : "graph";
        const state = store();
        if (!state.paneOpen[target]) state.togglePane(target);
        if (store().paneOpen[other]) store().togglePane(other);
        return ok(`showing ${view}`);
      }
      case "ui_switch_project": {
        const project = stringField(input, "name");
        if (!project) return fail("missing name");
        const state = store();
        if (project === state.activeProject) return ok(`already on ${project}`);
        const entry = state.projects.find((candidate) => candidate.name === project && !candidate.isFolder);
        if (!entry) return fail(`project ${project} not found`);
        await state.switchProject(project);
        return { ...ok(`switched to ${project}; this conversation ends here`), switchedProject: true };
      }
      default:
        return fail(`unsupported client tool ${name}`);
    }
  } catch (error) {
    return fail(error instanceof Error ? error.message : String(error));
  }
}
