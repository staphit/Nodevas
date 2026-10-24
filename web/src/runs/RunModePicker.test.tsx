import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError, type Actor, type Run, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "./budgetStore";
import { DispatchDialog } from "./DispatchDialog";
import { effectiveDispatchMode, runnerSupportsInteractive } from "./RunModePicker";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const alice: Actor = { id: "u1", name: "alice", role: "admin" };

const headlessOnly: Runner = {
  id: "r1",
  name: "laptop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.3.0",
    hostname: "laptop",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless"],
    agents: [{ kind: "claude", installed: true, models: [{ id: "opus", default: true }] }],
    repos: [{ id: "repo1", name: "nodevas" }],
  },
};

const desktop: Runner = {
  ...headlessOnly,
  id: "r2",
  name: "desktop",
  info: { ...headlessOnly.info!, hostname: "desktop", capabilities: ["headless", "interactive"] },
};

const created: Run = {
  id: "run1",
  project: "p",
  nodeId: "alpha",
  runnerId: "r2",
  runnerName: "desktop",
  agent: "claude",
  model: "opus",
  repoId: "repo1",
  repoName: "nodevas",
  base: "",
  branch: "",
  prompt: "",
  status: "queued",
  mode: "interactive",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-10-24T08:00:00Z",
};

const NOTE = /Opens the agent's own terminal UI on the runner/;

beforeEach(() => {
  viewer.current = alice;
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useRuns.getState().reset();
  useBudget.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
    used: { runs: 0, costUsd: 0, tokens: 0 },
    exceeded: "",
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("mode helpers", () => {
  it("allow interactive only where the runner has the capability", () => {
    expect(runnerSupportsInteractive(desktop)).toBe(true);
    expect(runnerSupportsInteractive(headlessOnly)).toBe(false);
    expect(runnerSupportsInteractive(undefined)).toBe(false);
    expect(effectiveDispatchMode("interactive", desktop)).toBe("interactive");
    expect(effectiveDispatchMode("interactive", headlessOnly)).toBe("headless");
    expect(effectiveDispatchMode("headless", desktop)).toBe("headless");
  });
});

describe("DispatchDialog mode", () => {
  it("gates Interactive on the selected runner's capability", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [headlessOnly, desktop] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    const runner = (await screen.findByRole("combobox", { name: "Runner" })) as HTMLSelectElement;

    expect(runner.value).toBe("r1");
    expect(screen.getByRole("radio", { name: "Headless" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Interactive" })).toBeDisabled();
    expect(screen.getByText(/Interactive needs a runner version/)).toBeInTheDocument();

    fireEvent.change(runner, { target: { value: "r2" } });
    const interactive = screen.getByRole("radio", { name: "Interactive" });
    expect(interactive).toBeEnabled();
    expect(screen.queryByText(NOTE)).toBeNull();
    fireEvent.click(interactive);
    expect(interactive).toBeChecked();
    expect(screen.getByText(NOTE)).toBeInTheDocument();

    // Back to a runner without it: the pick falls back to headless.
    fireEvent.change(runner, { target: { value: "r1" } });
    expect(screen.getByRole("radio", { name: "Headless" })).toBeChecked();
  });

  it("sends mode interactive only when chosen", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);
    const onCreated = vi.fn();
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("combobox", { name: "Runner" });

    fireEvent.click(screen.getByRole("radio", { name: "Interactive" }));
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(create).toHaveBeenCalledWith({
      nodeId: "alpha",
      runnerId: "r2",
      agent: "claude",
      model: "opus",
      repoId: "repo1",
      mode: "interactive",
    });
  });

  it("sends no mode for a headless run", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue({ ...created, mode: undefined });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });

    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).not.toHaveProperty("mode");
  });

  it("explains a 409 for an interactive dispatch", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    vi.spyOn(api, "createRun").mockRejectedValue(
      new ConflictError("", "", "runner lacks interactive", "FAILED_PRECONDITION"),
    );
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });

    fireEvent.click(screen.getByRole("radio", { name: "Interactive" }));
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/cannot run agents interactively/);
  });
});
