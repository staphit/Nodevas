import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph, Workflow } from "../types";
import { GoalStagesOverride } from "./GoalStagesOverride";

const standard: Workflow = {
  preset: "standard",
  stages: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false },
};
const full: Workflow = {
  preset: "full",
  stages: { planner: true, goalQa: true, tester: true, reviewer: true, ci: true, goalReview: true },
};

function setGraph(goalWorkflow?: Workflow) {
  useApp.setState({
    graph: {
      version: 1,
      workflow: standard,
      nodes: [
        { id: "g", title: "Big feature", kind: "goal", workflow: goalWorkflow },
        { id: "t", title: "Task", kind: "task" },
      ],
    } as Graph,
  });
}

let graphOps: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  graphOps = vi.spyOn(api, "graphOps").mockResolvedValue({} as never);
});

afterEach(() => vi.restoreAllMocks());

describe("GoalStagesOverride", () => {
  it("shows the project's stages on a goal, and nothing on a task", () => {
    setGraph();
    render(<GoalStagesOverride id="g" />);
    expect(screen.getByText(/Stages: Standard \(project\)/)).toBeInTheDocument();
    const { container } = render(<GoalStagesOverride id="t" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("overrides one goal's preset, e.g. QA on for a big feature", async () => {
    setGraph();
    const user = userEvent.setup();
    render(<GoalStagesOverride id="g" />);
    await user.click(screen.getByRole("button", { name: /Override/ }));
    await user.click(screen.getByRole("radio", { name: /Full/ }));
    await waitFor(() => expect(graphOps).toHaveBeenCalled());
    const ops = (graphOps.mock.calls.at(-1) as unknown as [unknown[]])[0];
    expect(ops).toContainEqual(expect.objectContaining({ kind: "node-workflow", nodeId: "g", workflow: expect.objectContaining({ preset: "full" }) }));
  });

  it("shows an override and can drop it", async () => {
    setGraph(full);
    const user = userEvent.setup();
    render(<GoalStagesOverride id="g" />);
    expect(screen.getByText(/Stages: Full \(this goal\)/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Use the project's/ }));
    await waitFor(() => expect(graphOps).toHaveBeenCalled());
    const ops = (graphOps.mock.calls.at(-1) as unknown as [unknown[]])[0];
    expect(ops).toContainEqual({ kind: "node-workflow", nodeId: "g", workflow: null });
  });
});
