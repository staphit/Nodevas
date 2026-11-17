import { afterEach, describe, expect, it } from "vitest";

import { firstRunHolding, looksNew, setFirstRunHolding, type FirstRunInputs } from "./gate";

/** Everything a brand-new person brings: a fresh workspace and nothing else. */
const NEW: FirstRunInputs = {
  freshWorkspace: true,
  startScreenDismissed: false,
  tourPrompt: "pending",
  tourChaptersDone: [],
  agentWorkstation: false,
  graphConnectTipDismissed: false,
  workstationInUse: false,
  experience: "full",
};

afterEach(() => setFirstRunHolding(false));

describe("looksNew", () => {
  it("is true for a fresh workspace and untouched choices", () => {
    expect(looksNew(NEW)).toBe(true);
  });

  // Any one sign of someone who already used the app is enough.
  it.each<[string, Partial<FirstRunInputs>]>([
    ["a workspace with work in it", { freshWorkspace: false }],
    ["the start screen answered", { startScreenDismissed: true }],
    ["the tour accepted", { tourPrompt: "accepted" }],
    ["the tour declined", { tourPrompt: "declined" }],
    ["a tour chapter done", { tourChaptersDone: ["shell"] }],
    ["the workstation turned on", { agentWorkstation: true }],
    ["the connect tip closed", { graphConnectTipDismissed: true }],
    ["the workstation in use", { workstationInUse: true }],
    ["Simple already chosen", { experience: "simple" }],
  ])("is false with %s", (_, signal) => {
    expect(looksNew({ ...NEW, ...signal })).toBe(false);
  });
});

describe("holding", () => {
  it("is off until set, and reads back what was set", () => {
    expect(firstRunHolding()).toBe(false);
    setFirstRunHolding(true);
    expect(firstRunHolding()).toBe(true);
    setFirstRunHolding(false);
    expect(firstRunHolding()).toBe(false);
  });
});
