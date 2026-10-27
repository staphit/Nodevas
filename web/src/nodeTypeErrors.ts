/**
 * The hub's node-type refusals, in words. The hub answers a dispatch, an
 * Autopilot start or a status write that the node's type does not allow with
 * one of these codes (internal/store/permission.go); the dialogs that send
 * those requests read them here so the reason names the type. A graph save
 * that changes the project's office style as an agent is refused the same
 * way (OFFICE_STYLE_AGENT_DENIED).
 */

import { nodeTypeById } from "./domain/graph/nodeTypes";
import { translate } from "./i18n";
import { useApp } from "./store";

export const NODE_TYPE_ERROR_CODES = [
  "NODE_TYPE_FORBIDS_AGENT",
  "NODE_TYPE_PEOPLE_ONLY",
  "NODE_TYPE_NO_STATUS",
  "UNKNOWN_NODE_TYPE",
  "OFFICE_STYLE_AGENT_DENIED",
] as const;

function errorCode(error: unknown): string {
  if (!error || typeof error !== "object" || !("code" in error)) return "";
  const code = (error as { code?: unknown }).code;
  return typeof code === "string" ? code : "";
}

/** The type's name for a node on the current board. */
function typeLabelOf(nodeId: string | undefined): string {
  const graph = useApp.getState().graph;
  const kind = nodeId ? graph?.nodes?.find((node) => node.id === nodeId)?.kind : undefined;
  const type = nodeTypeById(graph, kind);
  return type.builtin ? translate(`nodeType.${type.id}`) : type.label;
}

/**
 * A readable message for a node-type refusal, or null when the error is
 * something else and the caller's own wording applies.
 */
export function nodeTypeErrorMessage(error: unknown, nodeId?: string): string | null {
  const type = typeLabelOf(nodeId);
  switch (errorCode(error)) {
    case "NODE_TYPE_FORBIDS_AGENT":
      return translate("nodeType.error.notAgentWork", undefined, { type });
    case "NODE_TYPE_PEOPLE_ONLY":
      return translate("nodeType.error.peopleOnly", undefined, { type });
    case "NODE_TYPE_NO_STATUS":
      return translate("nodeType.error.noStatus", undefined, { type });
    case "UNKNOWN_NODE_TYPE":
      return translate("nodeType.error.unknown", undefined, { type });
    case "OFFICE_STYLE_AGENT_DENIED":
      return translate("office.style.agentDenied");
    default:
      return null;
  }
}

/** A graph save refused because an agent changed the office style, in
 * words; null for anything else (the save's own message applies). */
export function officeStyleErrorMessage(error: unknown): string | null {
  return errorCode(error) === "OFFICE_STYLE_AGENT_DENIED" ? translate("office.style.agentDenied") : null;
}
