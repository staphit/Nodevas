import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Acceptance, Graph, Workflow } from "../types";
import { AcceptanceCriteria } from "./AcceptanceCriteria";

const standard: Workflow = {
  preset: "standard",
  stages: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false },
};

function setGraph(acceptance?: Acceptance, workflow: Workflow | null = null) {
  useApp.setState({
    graph: {
      version: 1,
      workflow,
      nodes: [
        { id: "t", title: "Task", kind: "task", acceptance },
        { id: "g", title: "Goal", kind: "goal" },
        { id: "n", title: "Note", kind: "note" },
        { id: "r", title: "Review", kind: "gate" },
      ],
      edges: [],
    } as Graph,
  });
}

const twoItems: Acceptance = {
  items: [
    { id: "a1", text: "GET /x returns 200" },
    { id: "a2", text: "unknown ids 404" },
  ],
  by: "planner (run abc123)",
};

function lastAcceptanceOp(spy: ReturnType<typeof vi.spyOn>) {
  const calls = spy.mock.calls as unknown as [Array<{ kind: string; nodeId?: string; acceptance?: Acceptance }>][];
  const ops = calls.flatMap((call) => call[0]);
  return ops.filter((op) => op.kind === "acceptance").at(-1);
}

let graphOps: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  graphOps = vi.spyOn(api, "graphOps").mockResolvedValue({} as never);
});

afterEach(() => vi.restoreAllMocks());

describe("AcceptanceCriteria", () => {
  // The drawer's input is about 320px on a phone; at 150% text the example
  // placeholder needed 380 and read "…e.g. “GET /x retu". The placeholder is
  // the short ask in both languages; the example is said on hover.
  it("keeps the new criterion's placeholder short and its example on hover", () => {
    setGraph(twoItems);
    const { unmount } = render(<AcceptanceCriteria id="t" />);
    const input = screen.getByRole("textbox", { name: "New criterion" });
    expect(input).toHaveAttribute("placeholder", "One checkable line");
    expect(input).toHaveAttribute("title", "One checkable line, e.g. “GET /x returns 200”");
    unmount();

    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    render(<AcceptanceCriteria id="t" />);
    const zh = screen.getByRole("textbox", { name: "新條件" });
    expect(zh).toHaveAttribute("placeholder", "一行可驗證的條件");
    expect(zh).toHaveAttribute("title", "一行可驗證的條件，例如「GET /x 回傳 200」");
  });

  it("shows on the types agents work, and nowhere else", () => {
    setGraph(twoItems);
    const { unmount } = render(<AcceptanceCriteria id="t" />);
    expect(screen.getByRole("region", { name: "Acceptance criteria" })).toBeInTheDocument();
    unmount();
    render(<AcceptanceCriteria id="g" />);
    expect(screen.getByRole("region", { name: "Acceptance criteria" })).toBeInTheDocument();
    for (const id of ["n", "r"]) {
      const { container, unmount: done } = render(<AcceptanceCriteria id={id} />);
      expect(container).toBeEmptyDOMElement();
      done();
    }
  });

  it("lists the criteria with who wrote them", () => {
    setGraph(twoItems);
    render(<AcceptanceCriteria id="t" />);
    const region = screen.getByRole("region", { name: "Acceptance criteria" });
    expect(within(region).getByRole("textbox", { name: "Criterion a1" })).toHaveValue("GET /x returns 200");
    expect(within(region).getByRole("textbox", { name: "Criterion a2" })).toHaveValue("unknown ids 404");
    expect(within(region).getByText(/planner \(run abc123\)/)).toBeInTheDocument();
  });

  it("adds, edits, removes and reorders, keeping ids", async () => {
    setGraph(twoItems);
    const user = userEvent.setup();
    render(<AcceptanceCriteria id="t" />);

    await user.type(screen.getByRole("textbox", { name: "New criterion" }), "errors are logged");
    await user.click(screen.getByRole("button", { name: "Add criterion" }));
    await waitFor(() => expect(lastAcceptanceOp(graphOps)).toBeTruthy());
    expect(lastAcceptanceOp(graphOps)).toMatchObject({
      kind: "acceptance",
      nodeId: "t",
      acceptance: { items: [twoItems.items[0], twoItems.items[1], { id: "a3", text: "errors are logged" }] },
    });

    await user.click(screen.getByRole("button", { name: "Move a2 up" }));
    await waitFor(() =>
      expect(lastAcceptanceOp(graphOps)?.acceptance?.items.map((item) => item.id)).toEqual(["a2", "a1"]),
    );

    await user.click(screen.getByRole("button", { name: "Remove a1" }));
    await waitFor(() =>
      expect(lastAcceptanceOp(graphOps)?.acceptance?.items).toEqual([{ id: "a2", text: "unknown ids 404" }]),
    );

    const a2 = screen.getByRole("textbox", { name: "Criterion a2" });
    await user.clear(a2);
    await user.type(a2, "unknown ids return 404{Enter}");
    await waitFor(() =>
      expect(lastAcceptanceOp(graphOps)?.acceptance?.items.find((item) => item.id === "a2")?.text).toBe(
        "unknown ids return 404",
      ),
    );
  });

  it("marks locked criteria and asks before a person changes them", async () => {
    setGraph({ ...twoItems, locked: true });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    render(<AcceptanceCriteria id="t" />);
    expect(screen.getByText(/Locked/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Remove a1" }));
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(lastAcceptanceOp(graphOps)).toBeUndefined();

    confirm.mockReturnValue(true);
    await user.click(screen.getByRole("button", { name: "Remove a1" }));
    await waitFor(() => expect(lastAcceptanceOp(graphOps)?.acceptance?.items).toHaveLength(1));
  });

  it("warns when the project's stages need criteria and there are none", () => {
    setGraph(undefined, standard);
    render(<AcceptanceCriteria id="t" />);
    expect(screen.getByRole("alert")).toHaveTextContent(/Required before an Executor can be dispatched/);
  });

  it("does not warn under legacy or Light", () => {
    setGraph(undefined, null);
    render(<AcceptanceCriteria id="t" />);
    expect(screen.getByRole("region", { name: "Acceptance criteria" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
