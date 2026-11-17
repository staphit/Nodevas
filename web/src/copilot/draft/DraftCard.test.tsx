import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type CopilotDraftData } from "../../api";
import { readPreferences } from "../../preferences";
import { clearUndo, peekUndo } from "../../state/undo";
import { EditionProvider } from "../../runners/edition";
import { useRunners } from "../../runners/runnerStore";
import { useApp } from "../../store";
import { canCreate } from "./draftApi";
import { currentDraft, entryToItem, useCopilot, type DraftItem } from "../copilotStore";
import { DraftCard } from "./DraftCard";
import { DraftToast } from "./DraftToast";
import { useDraftUi } from "./draftUi";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      checkSubgraph: vi.fn(),
      createSubgraph: vi.fn(),
      projectAccess: vi.fn(),
      putCopilotDraftState: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      listRunners: vi.fn(),
    },
  };
});

/** An online runner with a repo and an agent: the coding agent can plan. */
const capableRunner = {
  id: "r1",
  name: "laptop",
  ownerId: "u1",
  ownerName: "me",
  createdAt: "",
  lastSeenAt: "",
  online: true,
  info: {
    version: "1",
    hostname: "laptop",
    os: "darwin",
    arch: "arm64",
    agents: [{ kind: "claude", installed: true, models: [] }],
    repos: [{ id: "repo1", name: "web" }],
  },
};

const base: CopilotDraftData = {
  draft: {
    summary: "Launch",
    questions: ["Which date?"],
    nodes: [
      { key: "docs", title: "Write docs", kind: "task" },
      { key: "ship", title: "Ship", kind: "task", after: ["docs"] },
    ],
  },
  issues: [],
  provider: "anthropic",
  model: "claude-x",
  state: "open",
};

function item(data: CopilotDraftData = base): DraftItem {
  const value = entryToItem({ id: "d1", seq: 1, kind: "draft", text: "Launch", data: JSON.stringify(data), createdAt: 1 });
  return value as DraftItem;
}

function show(data: CopilotDraftData = base, props: { themed?: boolean; prompt?: string } = {}) {
  const draftItem = item(data);
  useCopilot.setState({ items: [draftItem] });
  return render(<DraftCard item={draftItem} active themed={props.themed ?? false} prompt={props.prompt} />);
}

beforeEach(() => {
  clearUndo();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "main",
    graph: { version: 1, nodes: [{ id: "node-1", title: "Docs page" }], edges: [], ui: {} },
    graphUnreadable: null,
    canvasSelection: { project: "main", ids: [] },
  });
  useCopilot.setState({ items: [], drafts: {}, activeId: "c1", running: false, refining: null, composerFill: null });
  useRunners.setState({ loaded: true, loading: false, runners: [capableRunner], error: null });
  vi.mocked(api.listRunners).mockResolvedValue({ runners: [capableRunner] });
  useDraftUi.setState({ ghost: null, checks: {}, busy: {}, canWrite: { project: "main", value: true }, toast: null });
  vi.mocked(api.projectAccess).mockResolvedValue({ member: "editor", admin: false, canWrite: true, canManage: false } as never);
  vi.mocked(api.checkSubgraph).mockResolvedValue({ ok: true, ids: {}, created: [], rev: "r", issues: [], dependencies: 1, references: 0 });
  vi.mocked(api.getGraph).mockResolvedValue({ graph: { version: 1, nodes: [], edges: [], ui: {} }, rev: "r", statuses: {}, issues: null });
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
});

const createButton = () => screen.getByRole("button", { name: /^Create \d+ nodes$/ });

describe("DraftCard", () => {
  it("lists the nodes with the header, the model and what each waits for", () => {
    show();
    expect(screen.getByText("Draft: 2 nodes, 1 links")).toBeInTheDocument();
    expect(screen.getByText("claude-x")).toBeInTheDocument();
    expect(screen.getByText("Write docs")).toBeInTheDocument();
    expect(screen.getByText("After Write docs")).toBeInTheDocument();
    expect(createButton()).toBeEnabled();
  });

  it("says issues by code, never the model's English, and offers link-instead for a duplicate", () => {
    show({
      ...base,
      issues: [
        {
          key: "docs",
          severity: "warning",
          code: "duplicate_existing",
          message: "MODEL-ONLY TEXT",
          existingId: "node-1",
          title: "Docs page",
        },
      ],
    });
    expect(screen.queryByText(/MODEL-ONLY/)).toBeNull();
    expect(screen.getByText('Maybe the same as "Docs page". Link to it instead?')).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Link instead" }));
    const draft = currentDraft(useCopilot.getState(), "d1")!;
    expect(draft.nodes.map((node) => node.key)).toEqual(["ship"]);
    expect(draft.nodes[0].after).toEqual(["existing:node-1"]);
  });

  it("disables Create on an error, and again once an edit makes a cycle", () => {
    const { unmount } = show({
      ...base,
      issues: [{ key: "ship", severity: "error", code: "cycle", message: "x" }],
    });
    expect(createButton()).toBeDisabled();
    expect(screen.getAllByText("These steps wait for each other").length).toBeGreaterThan(0);
    unmount();
    show();
    act(() => {
      useCopilot.getState().editDraft("d1", (draft) => ({
        ...draft,
        nodes: draft.nodes.map((node) => (node.key === "docs" ? { ...node, after: ["ship"] } : node)),
      }));
    });
    expect(createButton()).toBeDisabled();
  });

  it("disables Create without write access, and says so", () => {
    useDraftUi.setState({ canWrite: { project: "main", value: false } });
    show();
    expect(createButton()).toBeDisabled();
    expect(screen.getByText("You can't write to this project")).toBeInTheDocument();
  });

  it("puts a question into the composer", () => {
    show();
    fireEvent.click(screen.getByRole("button", { name: "Which date?" }));
    expect(useCopilot.getState().composerFill?.text).toBe("Which date?");
  });

  it("removes a node, bridging its dependents", () => {
    show();
    fireEvent.click(screen.getByRole("button", { name: "Remove Write docs" }));
    expect(currentDraft(useCopilot.getState(), "d1")!.nodes).toEqual([
      { key: "ship", title: "Ship", kind: "task", after: [] },
    ]);
  });

  it("suggests the coding agent for a request that names code", () => {
    show(base, { prompt: "split web/src/App.tsx" });
    expect(screen.getByRole("button", { name: /Plan with my coding agent/ })).toHaveClass("is-suggested");
  });

  it("offers the coding agent only while a runner with a repo and an agent is online (finding 5)", () => {
    useRunners.setState({ runners: [{ ...capableRunner, online: false }] });
    const { unmount } = show(base, { prompt: "split web/src/App.tsx" });
    expect(screen.queryByRole("button", { name: /Plan with my coding agent/ })).toBeNull();
    unmount();
    useRunners.setState({ runners: [{ ...capableRunner, info: { ...capableRunner.info, repos: [] } }] });
    const second = show(base);
    expect(screen.queryByRole("button", { name: /Plan with my coding agent/ })).toBeNull();
    second.unmount();
    useRunners.setState({ runners: [capableRunner] });
    show(base);
    expect(screen.getByRole("button", { name: /Plan with my coding agent/ })).toBeInTheDocument();
  });

  it("says on a team hub that the plan run may wait for a runner to accept it, not in the single-user edition", () => {
    const draftItem = item(base);
    useCopilot.setState({ items: [draftItem] });
    const { unmount } = render(
      <EditionProvider edition="team">
        <DraftCard item={draftItem} active themed={false} />
      </EditionProvider>,
    );
    expect(screen.getByText("On a team hub, the plan run may wait until a runner accepts it.")).toBeInTheDocument();
    unmount();
    render(
      <EditionProvider edition="single">
        <DraftCard item={draftItem} active themed={false} />
      </EditionProvider>,
    );
    expect(screen.queryByText(/may wait until a runner accepts it/)).toBeNull();
  });

  it("is read-only while a refine of it is running: no Create, no Discard, no edits (finding 19)", () => {
    useCopilot.setState({ running: true, refining: "d1" });
    show();
    expect(createButton()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Discard" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Remove Write docs" })).toBeNull();
    expect(screen.getByText("Updating this draft…")).toBeInTheDocument();
    expect(canCreate("d1")).toBe(false);
  });

  it("stays creatable while some other turn runs", () => {
    useCopilot.setState({ running: true, refining: null });
    show();
    expect(createButton()).toBeEnabled();
    expect(canCreate("d1")).toBe(true);
  });

  it("the kind picker fixes an unknown kind by choosing Task (finding 23)", () => {
    const { container } = show({
      ...base,
      draft: { ...base.draft, nodes: [{ key: "docs", title: "Write docs", kind: "bug" }] },
    });
    const kind = within(container.querySelector('[data-draft-key="docs"]') as HTMLElement).getByRole("combobox", {
      name: "Type: bug",
    });
    expect(kind).toHaveValue("");
    expect(createButton()).toBeDisabled();
    fireEvent.change(kind, { target: { value: "task" } });
    expect(currentDraft(useCopilot.getState(), "d1")!.nodes[0].kind).toBe("task");
    expect(createButton()).toBeEnabled();
  });

  it("folds to a row of kind glyphs with a theme pack, and opens the list on demand", () => {
    const { container } = show(base, { themed: true });
    expect(container.querySelectorAll(".draft-glyph-row .draft-kind-glyph")).toHaveLength(2);
    expect(screen.queryByText("Write docs")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show list" }));
    expect(screen.getByText("Write docs")).toBeInTheDocument();
  });

  it("creates the nodes where the ghosts were, records it and offers an undo", async () => {
    useDraftUi.setState({ ghost: { entryId: "d1", cells: { docs: { col: 4, row: 0 }, ship: { col: 5, row: 0 } } } });
    vi.mocked(api.createSubgraph).mockResolvedValue({
      ok: true,
      ids: { docs: "node-2", ship: "node-3" },
      created: [
        { key: "docs", id: "node-2", title: "Write docs", kind: "task" },
        { key: "ship", id: "node-3", title: "Ship", kind: "task" },
      ],
      rev: "r2",
      issues: [],
      dependencies: 1,
      references: 0,
      batchId: "b1",
      undoable: true,
    });
    vi.mocked(api.putCopilotDraftState).mockResolvedValue({
      id: "d1",
      seq: 1,
      kind: "draft",
      text: "Launch",
      data: JSON.stringify({ ...base, state: "committed", batchId: "b1", createdIds: ["node-2", "node-3"] }),
      createdAt: 1,
    });
    show();
    render(<DraftToast />);
    fireEvent.click(createButton());
    await waitFor(() => expect(api.putCopilotDraftState).toHaveBeenCalled());
    expect(api.createSubgraph).toHaveBeenCalledWith({
      nodes: [
        { key: "docs", title: "Write docs", kind: "task" },
        { key: "ship", title: "Ship", kind: "task", dependsOn: ["docs"] },
      ],
      positions: { docs: { col: 4, row: 0 }, ship: { col: 5, row: 0 } },
      batch: { via: "copilot-draft:d1", label: "anthropic · claude-x" },
    });
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", {
      state: "committed",
      batchId: "b1",
      ids: ["node-2", "node-3"],
    });
    expect(peekUndo()).toMatchObject({ kind: "batch", batchId: "b1", ids: ["node-2", "node-3"], entryId: "d1" });
    expect(useApp.getState().canvasSelectRequest?.ids).toEqual(["node-2", "node-3"]);
    expect(screen.getByText("Created 2 nodes")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Undo" })).toBeInTheDocument();
  });
});

describe("DraftCard for a coding agent's plan", () => {
  const plan: CopilotDraftData = {
    draft: {
      summary: "",
      nodes: [
        { key: "t1", title: "Add the users table", kind: "task" },
        { key: "t2", title: "Login form", kind: "task", after: ["t1"] },
        { key: "t3", title: "Auth notes", kind: "note" },
      ],
    },
    issues: [],
    provider: "",
    model: "",
    state: "open",
    origin: { runId: "run-7", goalId: "n-12", agent: "claude", model: "sonnet" },
    extras: {
      t1: { tags: ["goal:n-12", "db", "schema"], acceptance: ["Migration creates users"], priority: "high" },
      t2: { tags: ["goal:n-12"], acceptance: ["The form logs in", "Errors are shown"] },
    },
  };

  beforeEach(() => {
    useApp.setState({
      graph: {
        version: 1,
        nodes: [
          { id: "node-1", title: "Docs page" },
          { id: "n-12", title: "Login page" },
        ],
        edges: [],
        ui: {},
      },
    });
  });

  const row = (container: HTMLElement, key: string) =>
    container.querySelector(`[data-draft-key="${key}"]`) as HTMLElement;

  it("a plan draft names the agent, model and goal", () => {
    const { container } = show(plan);
    expect(screen.getByText("claude · sonnet")).toBeInTheDocument();
    expect(screen.getByText("Planned by claude for “Login page”")).toBeInTheDocument();
    expect(
      screen.getByText("Nothing is on the board yet. Create adds these tasks and makes the goal wait for them."),
    ).toBeInTheDocument();
    expect(container.querySelector(".draft-origin")).not.toBeNull();
  });

  it("prefers the goal title the plan carries, and falls back to the goal id", () => {
    const { unmount } = show({ ...plan, origin: { ...plan.origin!, goalTitle: "Sign in" } });
    expect(screen.getByText("Planned by claude for “Sign in”")).toBeInTheDocument();
    unmount();
    useApp.setState({ graph: { version: 1, nodes: [], edges: [], ui: {} } });
    show(plan);
    expect(screen.getByText("Planned by claude for “n-12”")).toBeInTheDocument();
  });

  it("shows tag and acceptance chips per step", () => {
    const { container } = show(plan);
    const first = within(row(container, "t1"));
    // The goal tag is on every step: only the others are counted.
    expect(first.getByText("2 tags")).toHaveAttribute("title", "db, schema");
    expect(first.getByText("1 criteria")).toHaveAttribute("title", "Migration creates users");
    const second = within(row(container, "t2"));
    expect(second.queryByText(/tags$/)).toBeNull();
    expect(second.getByText("2 criteria")).toHaveAttribute("title", "The form logs in\nErrors are shown");
    expect(within(row(container, "t3")).queryByText(/criteria$/)).toBeNull();
  });

  it("greys the acceptance chip after the step's kind changes to Review, and Create leaves its acceptance out", async () => {
    vi.mocked(api.createSubgraph).mockResolvedValue({
      ok: true,
      ids: { t1: "node-2", t2: "node-3", t3: "node-4" },
      created: [],
      rev: "r2",
      issues: [],
      dependencies: 1,
      references: 0,
      batchId: "b9",
      undoable: true,
    });
    vi.mocked(api.putCopilotDraftState).mockResolvedValue(null as never);
    const { container } = show(plan);
    const kind = within(row(container, "t2")).getByRole("combobox", { name: /^Type/ });
    fireEvent.change(kind, { target: { value: "gate" } });
    const chip = within(row(container, "t2")).getByText("2 criteria");
    expect(chip).toHaveClass("draft-chip", "is-unused");
    expect(chip).toHaveAttribute(
      "title",
      "Not used for this type: Create leaves these criteria out.\nThe form logs in\nErrors are shown",
    );
    expect(within(row(container, "t1")).getByText("1 criteria")).not.toHaveClass("is-unused");
    fireEvent.click(createButton());
    await waitFor(() => expect(api.createSubgraph).toHaveBeenCalled());
    const spec = vi.mocked(api.createSubgraph).mock.calls[0][0];
    const t2 = spec.nodes.find((node) => node.key === "t2")!;
    expect(t2.kind).toBe("gate");
    expect(t2.acceptance).toBeUndefined();
    expect(spec.nodes.find((node) => node.key === "t1")!.acceptance).toEqual(["Migration creates users"]);
  });

  it("hides Plan with my coding agent", () => {
    show(plan, { prompt: "split web/src/App.tsx" });
    expect(screen.queryByRole("button", { name: /Plan with my coding agent/ })).toBeNull();
    expect(screen.queryByText(/closer look at the code/)).toBeNull();
  });

  it("Create sends tags, acceptance, goal edges and via copilot-plan:<id>", async () => {
    useDraftUi.setState({
      ghost: {
        entryId: "d1",
        cells: { t1: { col: 4, row: 2 }, t2: { col: 5, row: 2 }, t3: { col: 4, row: 3 } },
      },
    });
    vi.mocked(api.createSubgraph).mockResolvedValue({
      ok: true,
      ids: { t1: "node-2", t2: "node-3", t3: "node-4" },
      created: [
        { key: "t1", id: "node-2", title: "Add the users table", kind: "task" },
        { key: "t2", id: "node-3", title: "Login form", kind: "task" },
        { key: "t3", id: "node-4", title: "Auth notes", kind: "note" },
      ],
      rev: "r2",
      issues: [],
      dependencies: 2,
      references: 0,
      batchId: "b7",
      undoable: true,
    });
    vi.mocked(api.putCopilotDraftState).mockResolvedValue({
      id: "d1",
      seq: 1,
      kind: "draft",
      text: "",
      data: JSON.stringify({ ...plan, state: "committed", batchId: "b7", createdIds: ["node-2", "node-3", "node-4"] }),
      createdAt: 1,
    });
    show(plan);
    fireEvent.click(createButton());
    await waitFor(() => expect(api.putCopilotDraftState).toHaveBeenCalled());
    expect(api.createSubgraph).toHaveBeenCalledWith({
      nodes: [
        {
          key: "t1",
          title: "Add the users table",
          kind: "task",
          tags: ["goal:n-12", "db", "schema"],
          acceptance: ["Migration creates users"],
          priority: "high",
        },
        {
          key: "t2",
          title: "Login form",
          kind: "task",
          dependsOn: ["t1"],
          tags: ["goal:n-12"],
          acceptance: ["The form logs in", "Errors are shown"],
        },
        { key: "t3", title: "Auth notes", kind: "note", tags: ["goal:n-12"] },
      ],
      edges: [{ from: "t2", to: "n-12" }],
      positions: { t1: { col: 4, row: 2 }, t2: { col: 5, row: 2 }, t3: { col: 4, row: 3 } },
      batch: { via: "copilot-plan:d1", label: "claude · sonnet" },
    });
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", {
      state: "committed",
      batchId: "b7",
      ids: ["node-2", "node-3", "node-4"],
    });
    expect(peekUndo()).toMatchObject({ kind: "batch", batchId: "b7", entryId: "d1" });
  });

  it("the dry run sends the extras and the goal edges, without positions or batch", async () => {
    vi.useFakeTimers();
    try {
      show(plan);
      fireEvent.click(screen.getByRole("button", { name: "Remove Auth notes" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
    } finally {
      vi.useRealTimers();
    }
    const spec = vi.mocked(api.checkSubgraph).mock.calls.at(-1)![0];
    expect(spec.edges).toEqual([{ from: "t2", to: "n-12" }]);
    expect(spec.nodes[0].tags).toEqual(["goal:n-12", "db", "schema"]);
    expect(spec.positions).toBeUndefined();
    expect(spec.batch).toBeUndefined();
  });

  it("stays live when a newer draft follows: a plan is never superseded, and its goal waits on it", async () => {
    vi.mocked(api.putCopilotDraftState).mockResolvedValue(null as never);
    const planItem = item(plan);
    const newer = entryToItem({ id: "d2", seq: 2, kind: "draft", text: "B", data: JSON.stringify(plan), createdAt: 2 });
    useCopilot.setState({ items: [planItem, newer] });
    const { container } = render(<DraftCard item={planItem} active={false} themed={false} />);
    expect(screen.queryByText("Replaced by a newer draft")).toBeNull();
    expect(container.querySelector(".draft-card.is-folded")).toBeNull();
    expect(createButton()).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    await waitFor(() =>
      expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", { state: "discarded" }),
    );
  });

  it("a copilot draft a newer one follows still folds as superseded", () => {
    const own = item(base);
    useCopilot.setState({ items: [own] });
    const { container } = render(<DraftCard item={own} active={false} themed={false} />);
    expect(screen.getByText("Replaced by a newer draft")).toBeInTheDocument();
    expect(container.querySelector(".draft-card.is-folded")).not.toBeNull();
    expect(screen.queryByRole("button", { name: "Discard" })).toBeNull();
  });

  it("allows up to 100 steps", () => {
    const nodes = Array.from({ length: 60 }, (_, index) => ({ key: `k${index}`, title: `Step ${index}`, kind: "task" }));
    show({ ...plan, draft: { summary: "", nodes }, extras: {} });
    expect(screen.getByRole("button", { name: "Create 60 nodes" })).toBeEnabled();
    expect(screen.queryByText(/More than \d+ nodes/)).toBeNull();
  });
});
