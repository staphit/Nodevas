// When Agent chat's floating button shows on the board, and what its badge
// says. Pure, so the rules are tested on their own.

export interface AgentFabInput {
  /** The agent workstation is shown in this browser. */
  workstation: boolean;
  /** Station is open: Agent chat is its side card there, nothing floats. */
  station: boolean;
  /** The panel is open already. */
  open: boolean;
  /** Agents online in the project. */
  online: number;
  /** Messages the viewer has not read. */
  unseen: number;
  /** Questions agents asked the viewer. */
  pendingAsks: number;
  /** Messages held for the viewer's approval. */
  held: number;
}

/** The button shows only when there is something: an agent online, unread
 * messages, a question or a held message. Otherwise its way in is Station's
 * side card (and Ctrl+Shift+A). */
export function shouldShowAgentFab(input: AgentFabInput): boolean {
  return (
    input.workstation &&
    !input.station &&
    !input.open &&
    input.online + input.unseen + input.pendingAsks + input.held > 0
  );
}

export interface AgentFabBadge {
  tone: "waiting" | "unread";
  count: number;
}

/** What waits on the viewer comes first; unread messages otherwise. */
export function agentFabBadge(input: Pick<AgentFabInput, "unseen" | "pendingAsks" | "held">): AgentFabBadge | null {
  const waiting = input.pendingAsks + input.held;
  if (waiting > 0) return { tone: "waiting", count: waiting };
  if (input.unseen > 0) return { tone: "unread", count: input.unseen };
  return null;
}
