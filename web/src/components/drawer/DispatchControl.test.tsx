import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useAutopilot } from "../../autopilot/autopilotStore";
import { readPreferences } from "../../preferences";
import { useRuns } from "../../runs/runStore";
import { useApp } from "../../store";
import type { NodeKind } from "../../types";
import { DispatchControl } from "./DispatchControl";

function show(kind: NodeKind | undefined) {
  useApp.setState({
    graph: { version: 1, nodes: [{ id: "a", title: "Alpha", kind }], edges: [] },
  });
  return render(<DispatchControl id="a" onShowRuns={vi.fn()} />);
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useAutopilot.setState({ loadForGoal: vi.fn(async () => {}), byGoal: {}, views: {} });
  // Loaded already, so the planning check asks the hub nothing.
  useRuns.setState({ runsByNode: { a: [] } });
});

describe("DispatchControl", () => {
  it("dispatches a Task as before", () => {
    show("task");
    expect(screen.getByRole("button", { name: "Dispatch to agent" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "More agent actions" })).toBeInTheDocument();
  });

  it("offers no agent action for a type agents cannot work", () => {
    const { container } = show("gate");
    expect(screen.queryByRole("button", { name: "Dispatch to agent" })).toBeNull();
    expect(container).toBeEmptyDOMElement();
  });

  it("makes Autopilot the primary action for a Goal", () => {
    show("goal");
    expect(screen.queryByRole("button", { name: "Dispatch to agent" })).toBeNull();
    expect(screen.getByRole("button", { name: /Autopilot/ })).toHaveClass("dispatch-main");
  });
});
