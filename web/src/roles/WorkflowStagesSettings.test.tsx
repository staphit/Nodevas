import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph, Workflow } from "../types";
import { WorkflowStagesSettings } from "./WorkflowStagesSettings";

const custom: Workflow = {
  preset: "custom",
  stages: { planner: true, goalQa: false, tester: true, reviewer: false, ci: false, goalReview: false },
};

function setGraph(workflow: Workflow | null, type = "workflow") {
  useApp.setState({
    activeProject: "demo",
    graph: { version: 1, type, workflow, nodes: [] } as Graph,
  });
}

function lastWorkflowOp(spy: ReturnType<typeof vi.spyOn>) {
  const calls = spy.mock.calls as unknown as [Array<{ kind: string; workflow?: Workflow | null }>][];
  return calls.flatMap((call) => call[0]).filter((op) => op.kind === "workflow").at(-1);
}

let graphOps: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  graphOps = vi.spyOn(api, "graphOps").mockResolvedValue({} as never);
});

afterEach(() => vi.restoreAllMocks());

describe("WorkflowStagesSettings", () => {
  it("offers the presets with their cost hints", () => {
    setGraph(null);
    render(<WorkflowStagesSettings />);
    const group = screen.getByRole("radiogroup", { name: "Workflow stages" });
    const radios = within(group).getAllByRole("radio");
    expect(radios.map((radio) => radio.getAttribute("value"))).toEqual(["light", "standard", "full", "custom"]);
    expect(within(group).getByRole("radio", { name: /Light.*1×/ })).toBeInTheDocument();
    expect(within(group).getByRole("radio", { name: /Standard.*≈2–3×/ })).toBeInTheDocument();
    expect(within(group).getByRole("radio", { name: /Full.*≈3–4×/ })).toBeInTheDocument();
  });

  it("says a project without stages is legacy", () => {
    setGraph(null);
    render(<WorkflowStagesSettings />);
    expect(screen.getByText(/Legacy \(configured per autopilot\) — choose a preset/)).toBeInTheDocument();
    for (const radio of screen.getAllByRole("radio")) expect(radio).not.toBeChecked();
  });

  it("writes the chosen preset to the project", async () => {
    setGraph(null);
    const user = userEvent.setup();
    render(<WorkflowStagesSettings />);
    await user.click(screen.getByRole("radio", { name: /Standard/ }));
    await waitFor(() => expect(lastWorkflowOp(graphOps)).toBeTruthy());
    expect(lastWorkflowOp(graphOps)?.workflow).toMatchObject({
      preset: "standard",
      stages: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false },
    });
  });

  it("toggles each stage under Custom, with a live cost hint", async () => {
    setGraph(custom);
    const user = userEvent.setup();
    render(<WorkflowStagesSettings />);
    expect(screen.getByRole("radio", { name: /Custom/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /Tester/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /Reviewer/ })).not.toBeChecked();
    expect(screen.getByText(/≈1\.8×|≈2–3×/)).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: /CI gate/ }));
    await waitFor(() => expect(lastWorkflowOp(graphOps)?.workflow?.stages.ci).toBe(true));
    expect(lastWorkflowOp(graphOps)?.workflow?.preset).toBe("custom");
  });

  it("names tests Checklist judging in a story project", () => {
    setGraph({ ...custom, judge: "checklist" }, "story");
    render(<WorkflowStagesSettings />);
    expect(screen.getByRole("checkbox", { name: /Checklist judging/ })).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: /^Tester/ })).toBeNull();
  });

  it("bounds the cost levers", async () => {
    setGraph({ ...custom, preset: "full", stages: { ...custom.stages, goalQa: true, reviewer: true, ci: true, goalReview: true } });
    render(<WorkflowStagesSettings />);
    const rounds = screen.getByRole("spinbutton", { name: "Max QA re-verification rounds" });
    expect(rounds).toHaveAttribute("min", "1");
    expect(rounds).toHaveAttribute("max", "5");
    const skip = screen.getByRole("spinbutton", { name: /Skip Reviewer for changes under/ });
    expect(skip).toHaveAttribute("min", "0");
    expect(skip).toHaveAttribute("max", "10000");
    expect(screen.getByRole("spinbutton", { name: "Max tester rounds" })).toHaveAttribute("max", "3");
    expect(screen.getByRole("spinbutton", { name: "Max green tries" })).toHaveAttribute("max", "5");
  });
});
