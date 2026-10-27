import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  AuthError,
  ConflictError,
  RequestError,
  type Actor,
  type Run,
  type RunBudgetStatus,
  type Runner,
} from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import { DispatchDialog, ownRunners, runnerUnavailableReason } from "./DispatchDialog";
import { useBudget } from "./budgetStore";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const alice: Actor = { id: "u1", name: "alice", role: "admin" };

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
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless"],
    agents: [
      {
        kind: "claude",
        installed: true,
        version: "2.1.0",
        models: [
          { id: "opus", label: "Opus" },
          { id: "sonnet", label: "Sonnet", default: true },
        ],
      },
      { kind: "gemini", installed: false, models: [{ id: "flash" }] },
      { kind: "codex", installed: true, models: [{ id: "gpt-5" }] },
    ],
    repos: [
      { id: "repo1", name: "nodevas", path: "H:\\visual-editor" },
      { id: "repo2", name: "site" },
    ],
  },
};

const laptop: Runner = {
  ...desktop,
  id: "r2",
  name: "laptop",
  online: false,
  info: { ...desktop.info!, hostname: "laptop" },
};

const oldRunner: Runner = {
  ...desktop,
  id: "r3",
  name: "old",
  info: { ...desktop.info!, hostname: "old", capabilities: [] },
};

const bobs: Runner = { ...desktop, id: "r4", name: "bob-pc", ownerId: "u2", ownerName: "bob" };

const bare: Runner = {
  ...desktop,
  id: "r5",
  name: "bare",
  info: { ...desktop.info!, hostname: "bare", repos: [] },
};

const created: Run = {
  id: "run1",
  project: "p",
  nodeId: "alpha",
  runnerId: "r1",
  runnerName: "desktop",
  agent: "claude",
  model: "sonnet",
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

const withinBudget: RunBudgetStatus = {
  budget: { dailyRuns: 10, dailyCostUsd: 5, dailyTokens: 0 },
  used: { runs: 2, costUsd: 0.5, tokens: 1000 },
  exceeded: "",
};

function selectNamed(name: string): HTMLSelectElement {
  return screen.getByRole("combobox", { name }) as HTMLSelectElement;
}

function optionTexts(select: HTMLSelectElement): string[] {
  return within(select)
    .getAllByRole("option")
    .map((option) => option.textContent ?? "");
}

beforeEach(() => {
  viewer.current = alice;
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
  vi.spyOn(api, "getRunBudget").mockResolvedValue(withinBudget);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("runner eligibility", () => {
  it("keeps only the viewer's runners, or all of them without accounts", () => {
    expect(ownRunners([desktop, bobs], alice).map((runner) => runner.id)).toEqual(["r1"]);
    expect(ownRunners([desktop, bobs], null)).toHaveLength(2);
  });

  it("explains why a runner cannot take a run", () => {
    expect(runnerUnavailableReason(desktop)).toBe("");
    expect(runnerUnavailableReason(laptop)).toBe("offline");
    expect(runnerUnavailableReason(oldRunner)).toBe("headless");
  });
});

describe("DispatchDialog", () => {
  it("offers own headless runners, installed agents, their models and repos", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({
      runners: [laptop, desktop, oldRunner, bobs],
    });

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);

    const runner = await screen.findByRole("combobox", { name: "Runner" });
    const runnerSelect = runner as HTMLSelectElement;
    expect(optionTexts(runnerSelect)).toEqual([
      "laptop — offline",
      "desktop (PC-01)",
      "old — cannot take runs (update the runner)",
    ]);
    expect(runnerSelect.value).toBe("r1");
    const options = within(runnerSelect).getAllByRole("option") as HTMLOptionElement[];
    expect(options.map((option) => option.disabled)).toEqual([true, false, true]);

    const agent = selectNamed("Agent");
    expect(optionTexts(agent)).toEqual(["claude · 2.1.0", "codex"]);
    const model = selectNamed("Model");
    expect(optionTexts(model)).toEqual(["Opus", "Sonnet (default)"]);
    expect(model.value).toBe("sonnet");

    fireEvent.change(agent, { target: { value: "codex" } });
    expect(optionTexts(selectNamed("Model"))).toEqual(["gpt-5"]);
    expect(selectNamed("Model").value).toBe("gpt-5");

    const repo = selectNamed("Repository");
    expect(optionTexts(repo)).toEqual(["nodevas — H:\\visual-editor", "site"]);
    expect(screen.getByText(/waits until someone at the runner accepts it/)).toBeInTheDocument();
  });

  it("submits the chosen runner, agent, model, repo, base and instructions", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);
    const onCreated = vi.fn();

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("combobox", { name: "Runner" });

    fireEvent.change(selectNamed("Model"), { target: { value: "opus" } });
    fireEvent.change(selectNamed("Repository"), { target: { value: "repo2" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Base branch" }), {
      target: { value: " develop " },
    });
    fireEvent.change(screen.getByRole("textbox", { name: /Extra instructions/ }), {
      target: { value: "Keep the diff small." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(create).toHaveBeenCalledWith({
      nodeId: "alpha",
      runnerId: "r1",
      agent: "claude",
      model: "opus",
      repoId: "repo2",
      base: "develop",
      prompt: "Keep the diff small.",
    });
    expect(useRuns.getState().runsByNode.alpha?.[0].id).toBe("run1");
  });

  it("omits an empty base and empty instructions", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    await waitFor(() =>
      expect(create).toHaveBeenCalledWith({
        nodeId: "alpha",
        runnerId: "r1",
        agent: "claude",
        model: "sonnet",
        repoId: "repo1",
      }),
    );
  });

  it("shows an API error inline and stays open", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    vi.spyOn(api, "createRun").mockRejectedValue(new AuthError(403, "remote dispatch is off"));
    const onCreated = vi.fn();

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("combobox", { name: "Runner" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Not allowed: remote dispatch is off",
    );
    expect(onCreated).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Dispatch" })).toBeEnabled();
  });

  it("explains a 409 as a runner that cannot take runs", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    vi.spyOn(api, "createRun").mockRejectedValue(new ConflictError("", ""));

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/This runner cannot take runs/);
  });

  it("checks the base branch and the instruction size before sending", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });
    const dispatch = screen.getByRole("button", { name: "Dispatch" });
    const base = screen.getByRole("textbox", { name: "Base branch" });

    for (const bad of ["-x", "a..b", "feat branch", "main;rm"]) {
      fireEvent.change(base, { target: { value: bad } });
      expect(base).toHaveAttribute("aria-invalid", "true");
      expect(dispatch).toBeDisabled();
    }
    fireEvent.change(base, { target: { value: "feature/run-1.2_x" } });
    expect(base).not.toHaveAttribute("aria-invalid");
    expect(dispatch).toBeEnabled();

    // 3000 CJK characters fit the character limit but are ~9 KB of UTF-8.
    fireEvent.change(screen.getByRole("textbox", { name: /Extra instructions/ }), {
      target: { value: "字".repeat(3000) },
    });
    expect(screen.getByText(/limited to 8 KB/)).toBeInTheDocument();
    expect(dispatch).toBeDisabled();
  });

  it("explains how to add a repository when the runner has none", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [bare] });

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });

    expect(screen.getByText("nodevas runner repo add <path>")).toBeInTheDocument();
    expect(selectNamed("Repository")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Dispatch" })).toBeDisabled();
  });

  it("points to the runners dialog when no runner can take a run", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [laptop] });
    const onClose = vi.fn();
    const opened = vi.fn();
    window.addEventListener("nodevas-open-runners", opened);

    render(<DispatchDialog nodeId="alpha" onClose={onClose} onCreated={vi.fn()} />);
    expect(await screen.findByText(/None of your runners can take a run/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Dispatch" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Manage runners" }));
    expect(onClose).toHaveBeenCalled();
    expect(opened).toHaveBeenCalled();
    window.removeEventListener("nodevas-open-runners", opened);
  });

  it("says when there are no runners at all", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [bobs] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(await screen.findByText(/You have no runners yet/)).toBeInTheDocument();
  });

  it("warns up front when today's budget is used up, and links to it", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const budget = vi.spyOn(api, "getRunBudget").mockResolvedValue({
      ...withinBudget,
      used: { runs: 0, costUsd: 5.1, tokens: 0 },
      exceeded: "cost",
    });
    const onClose = vi.fn();
    const opened = vi.fn();
    window.addEventListener("nodevas-open-budget", opened);

    render(<DispatchDialog nodeId="alpha" onClose={onClose} onCreated={vi.fn()} />);
    expect(
      await screen.findByText(/Today's budget is used up \(Estimated cost\)/),
    ).toBeInTheDocument();
    expect(budget).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "Adjust budget" }));
    expect(onClose).toHaveBeenCalled();
    expect(opened).toHaveBeenCalled();
    window.removeEventListener("nodevas-open-budget", opened);
  });

  it("says nothing about the budget while within it", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });
    await waitFor(() => expect(useBudget.getState().status).not.toBeNull());
    expect(screen.queryByText(/budget is used up/)).not.toBeInTheDocument();
  });

  it("shows the hub's 429 budget refusal and catches the warning up", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const budget = vi
      .spyOn(api, "getRunBudget")
      .mockResolvedValueOnce(withinBudget)
      .mockResolvedValueOnce({ ...withinBudget, used: { runs: 10, costUsd: 0, tokens: 0 }, exceeded: "runs" });
    vi.spyOn(api, "createRun").mockRejectedValue(
      new RequestError(
        429,
        "daily run budget reached: 10 of 10 runs today (UTC)",
        "RESOURCE_EXHAUSTED",
      ),
    );
    const onCreated = vi.fn();
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("combobox", { name: "Runner" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Over budget: daily run budget reached: 10 of 10 runs today (UTC)",
    );
    expect(onCreated).not.toHaveBeenCalled();
    expect(await screen.findByText(/Today's budget is used up \(Runs\)/)).toBeInTheDocument();
    expect(budget).toHaveBeenCalledTimes(2);
  });
});
