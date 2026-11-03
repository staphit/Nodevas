import { afterEach, describe, expect, it } from "vitest";
import { readPreferences } from "../preferences";
import { undoEntryLabel } from "../state/undo";
import { useApp } from "../store";
import type { Graph } from "../types";
import { applyGraphCommand } from "./commands";
import { planOrderIssue } from "./graph/plan";

function speak(language: "en" | "zh-TW") {
  useApp.setState({ preferences: { ...readPreferences(), language } });
}

function graph(): Graph {
  return { version: 1, nodes: [{ id: "a", title: "A" }], edges: [] } as unknown as Graph;
}

function failure(run: () => void): string {
  try {
    run();
  } catch (error) {
    return error instanceof Error ? error.message : String(error);
  }
  return "";
}

afterEach(() => speak("en"));

// A refusal from the command layer reaches the reader as written, so it has
// to be in the reader's language: an English interface showed Chinese.
describe("domain refusals speak the interface language", () => {
  it("names a missing node in English", () => {
    speak("en");
    const message = failure(() =>
      applyGraphCommand(graph(), { type: "node.addRef", nodeId: "missing", ref: "a" }),
    );
    expect(message).toBe("Node “missing” does not exist or was deleted.");
  });

  it("names a missing node in Traditional Chinese", () => {
    speak("zh-TW");
    const message = failure(() =>
      applyGraphCommand(graph(), { type: "node.addRef", nodeId: "missing", ref: "a" }),
    );
    expect(message).toBe("節點「missing」不存在或已被刪除。");
  });

  it("refuses a self reference in English", () => {
    speak("en");
    const message = failure(() =>
      applyGraphCommand(graph(), { type: "node.addRef", nodeId: "a", ref: "a" }),
    );
    expect(message).toBe("A node cannot reference itself.");
  });

  it("explains a schedule out of order in English", () => {
    speak("en");
    const plans = [{ status: "done", date: "2026-01-01" }] as Parameters<typeof planOrderIssue>[0];
    expect(planOrderIssue(plans, "started", "2026-02-01")).toBe(
      "The deadline cannot be earlier than the start date.",
    );
  });

  it("labels an undo step in English by default", () => {
    speak("en");
    expect(undoEntryLabel({ kind: "create-node", ids: ["a"] } as Parameters<typeof undoEntryLabel>[0])).toBe(
      "Add node",
    );
    speak("zh-TW");
    expect(undoEntryLabel({ kind: "create-node", ids: ["a"] } as Parameters<typeof undoEntryLabel>[0])).toBe(
      "新增節點",
    );
  });
});
