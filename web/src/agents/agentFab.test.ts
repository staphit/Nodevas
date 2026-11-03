import { describe, expect, it } from "vitest";

import { agentFabBadge, shouldShowAgentFab, type AgentFabInput } from "./agentFab";

const base: AgentFabInput = {
  workstation: true,
  station: false,
  open: false,
  online: 0,
  unseen: 0,
  pendingAsks: 0,
  held: 0,
};

describe("shouldShowAgentFab", () => {
  it("shows only when there is something to see", () => {
    expect(shouldShowAgentFab(base)).toBe(false);
    for (const key of ["online", "unseen", "pendingAsks", "held"] as const) {
      expect(shouldShowAgentFab({ ...base, [key]: 1 }), key).toBe(true);
    }
  });

  it("never floats with the workstation hidden, in Station, or over the open panel", () => {
    const busy = { ...base, online: 2, unseen: 3 };
    expect(shouldShowAgentFab({ ...busy, workstation: false })).toBe(false);
    expect(shouldShowAgentFab({ ...busy, station: true })).toBe(false);
    expect(shouldShowAgentFab({ ...busy, open: true })).toBe(false);
  });
});

describe("agentFabBadge", () => {
  it("puts what waits on the viewer before unread messages", () => {
    expect(agentFabBadge({ unseen: 0, pendingAsks: 0, held: 0 })).toBeNull();
    expect(agentFabBadge({ unseen: 4, pendingAsks: 0, held: 0 })).toEqual({ tone: "unread", count: 4 });
    expect(agentFabBadge({ unseen: 4, pendingAsks: 1, held: 2 })).toEqual({ tone: "waiting", count: 3 });
  });
});
