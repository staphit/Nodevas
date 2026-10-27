import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Actor, type Run, type RouteSuggestion, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import { DispatchDialog } from "./DispatchDialog";
import { useBudget } from "./budgetStore";
import { useRouting } from "./routingStore";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const desktop: Runner = {
  id: "r1",
  name: "desktop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.3.0",
    hostname: "desktop",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless"],
    agents: [
      {
        kind: "claude",
        installed: true,
        models: [
          { id: "opus", label: "Opus" },
          { id: "sonnet", label: "Sonnet", default: true },
        ],
      },
      { kind: "codex", installed: true, models: [{ id: "gpt-5" }, { id: "gpt-5-codex" }] },
    ],
    repos: [{ id: "repo1", name: "nodevas" }],
  },
};
const studio: Runner = { ...desktop, id: "r2", name: "studio", info: { ...desktop.info!, hostname: "studio" } };

const created: Run = {
  id: "run1",
  project: "p",
  nodeId: "alpha",
  runnerId: "r2",
  runnerName: "studio",
  agent: "codex",
  model: "gpt-5",
  repoId: "repo1",
  repoName: "nodevas",
  base: "",
  branch: "",
  prompt: "",
  status: "queued",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-10-24T08:00:00Z",
};

const suggestion: RouteSuggestion = {
  ruleId: "rule-1",
  match: "tag",
  runnerId: "r2",
  target: { agent: "codex", model: "gpt-5" },
  fallbacks: [
    { agent: "claude", model: "opus" },
    { agent: "claude", model: "sonnet" },
  ],
};

function selectNamed(name: string): HTMLSelectElement {
  return screen.getByRole("combobox", { name }) as HTMLSelectElement;
}

beforeEach(() => {
  viewer.current = { id: "u1", name: "alice", role: "admin" };
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({
    loaded: false,
    loading: false,
    runners: [],
    error: null,
    creating: false,
    revoking: {},
  });
  useRuns.getState().reset();
  useBudget.getState().reset();
  useRouting.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
    used: { runs: 0, costUsd: 0, tokens: 0 },
    exceeded: "",
  });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop, studio] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("DispatchDialog routing", () => {
  it("pre-fills runner, agent, model and fallbacks from the matched rule", async () => {
    vi.spyOn(api, "getRoute").mockResolvedValue(suggestion);
    // The suggestion names its rule; the rule says which tag matched.
    const rules = vi.spyOn(api, "getRouting").mockResolvedValue({
      rules: [
        {
          id: "rule-1",
          project: "",
          match: "tag",
          value: "review",
          target: { agent: "codex", model: "gpt-5" },
        },
      ],
    });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);

    await waitFor(() =>
      expect(screen.getByTestId("dispatch-rule-note")).toHaveTextContent(
        "From rule: Tag = review",
      ),
    );
    expect(api.getRoute).toHaveBeenCalledWith("alpha");
    expect(rules).toHaveBeenCalledTimes(1);
    expect(selectNamed("Runner").value).toBe("r2");
    expect(selectNamed("Agent").value).toBe("codex");
    expect(selectNamed("Model").value).toBe("gpt-5");
    // The chain is collapsed, its length in the summary.
    const chain = screen.getByTestId("dispatch-fallbacks");
    expect(chain).not.toHaveAttribute("open");
    expect(within(chain).getByText("Fallback chain (2)")).toBeInTheDocument();
    expect(
      (within(chain).getByRole("combobox", { name: "Fallback 1 model" }) as HTMLSelectElement).value,
    ).toBe("opus");

    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith({
      nodeId: "alpha",
      runnerId: "r2",
      agent: "codex",
      model: "gpt-5",
      repoId: "repo1",
      fallbacks: [
        { agent: "claude", model: "opus" },
        { agent: "claude", model: "sonnet" },
      ],
    });
  });

  it("lets the user override what the rule picked", async () => {
    vi.spyOn(api, "getRoute").mockResolvedValue({ ...suggestion, value: "review" });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByTestId("dispatch-rule-note");

    fireEvent.change(selectNamed("Runner"), { target: { value: "r1" } });
    fireEvent.change(selectNamed("Agent"), { target: { value: "claude" } });
    fireEvent.change(selectNamed("Model"), { target: { value: "opus" } });
    const chain = screen.getByTestId("dispatch-fallbacks");
    fireEvent.click(within(chain).getByRole("button", { name: "Remove Fallback 1" }));
    fireEvent.change(within(chain).getByRole("combobox", { name: "Fallback 1 agent" }), {
      target: { value: "codex" },
    });
    // An override is not a mismatch worth a warning.
    expect(screen.getByTestId("dispatch-rule-note")).toHaveTextContent("From rule: Tag = review");
    expect(screen.queryByText(/does not offer/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toEqual({
      nodeId: "alpha",
      runnerId: "r1",
      agent: "claude",
      model: "opus",
      repoId: "repo1",
      fallbacks: [{ agent: "codex", model: "gpt-5" }],
    });
  });

  it("pre-fills a pipeline and sends it with the implementer from the form", async () => {
    vi.spyOn(api, "getRoute").mockResolvedValue({
      ruleId: "rule-2",
      match: "default",
      target: { agent: "claude", model: "sonnet" },
      pipeline: {
        implement: { agent: "claude", model: "opus" },
        review: { agent: "codex", model: "gpt-5-codex" },
        maxRounds: 3,
      },
    });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(await screen.findByTestId("dispatch-rule-note")).toHaveTextContent(
      "From rule: Global default",
    );
    expect(screen.getByRole("checkbox", { name: "Pipeline: implement → review" })).toBeChecked();
    expect(selectNamed("Model").value).toBe("opus");
    expect(screen.getByText("claude/opus (the agent and model above)")).toBeInTheDocument();
    expect(selectNamed("Review model").value).toBe("gpt-5-codex");
    expect(selectNamed("Rounds").value).toBe("3");

    fireEvent.change(selectNamed("Rounds"), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toEqual({
      nodeId: "alpha",
      runnerId: "r1",
      agent: "claude",
      model: "opus",
      repoId: "repo1",
      pipeline: {
        implement: { agent: "claude", model: "opus" },
        review: { agent: "codex", model: "gpt-5-codex" },
        maxRounds: 2,
      },
    });
  });

  it("starts unfilled when no rule matches, and builds a pipeline and chain by hand", async () => {
    vi.spyOn(api, "getRoute").mockResolvedValue({
      ruleId: "",
      match: "",
      target: { agent: "", model: "" },
    });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });
    await waitFor(() => expect(api.getRoute).toHaveBeenCalled());
    expect(screen.queryByTestId("dispatch-rule-note")).not.toBeInTheDocument();
    expect(selectNamed("Runner").value).toBe("r1");
    expect(screen.getByText("Fallback chain (0)")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("checkbox", { name: "Pipeline: implement → review" }));
    // The reviewer defaults to another agent than the implementer.
    expect(selectNamed("Review agent").value).toBe("codex");
    const chain = screen.getByTestId("dispatch-fallbacks");
    fireEvent.click(within(chain).getByRole("button", { name: "Add fallback" }));
    // A free-text fallback left blank blocks the dispatch.
    fireEvent.change(within(chain).getByRole("combobox", { name: "Fallback 1 agent" }), {
      target: { value: "\u0000other" },
    });
    fireEvent.change(within(chain).getByLabelText("Fallback 1 model"), { target: { value: "" } });
    expect(screen.getByRole("button", { name: "Dispatch" })).toBeDisabled();
    fireEvent.change(within(chain).getByLabelText("Fallback 1 model"), {
      target: { value: " haiku " },
    });

    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toEqual({
      nodeId: "alpha",
      runnerId: "r1",
      agent: "claude",
      model: "sonnet",
      repoId: "repo1",
      fallbacks: [{ agent: "claude", model: "haiku" }],
      pipeline: {
        implement: { agent: "claude", model: "sonnet" },
        review: { agent: "codex", model: "gpt-5" },
        maxRounds: 1,
      },
    });
  });

  it("says when the runner cannot honour the rule's model", async () => {
    vi.spyOn(api, "getRoute").mockResolvedValue({
      ruleId: "rule-3",
      match: "kind",
      value: "task",
      target: { agent: "gemini", model: "flash" },
    });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    const note = await screen.findByTestId("dispatch-rule-note");
    // A kind rule reads as the type's name.
    expect(note).toHaveTextContent("From rule: Node kind = Task");
    expect(note).toHaveTextContent("This runner does not offer gemini/flash; using claude/sonnet.");
  });

  it("still dispatches when the hub cannot suggest a route", async () => {
    vi.spyOn(api, "getRoute").mockRejectedValue(new Error("404"));
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(screen.queryByTestId("dispatch-rule-note")).not.toBeInTheDocument();
  });
});
