// The dispatch dialog in Simple mode: a check the project asks for but the
// task does not have (criteria, a test command) no longer stops the start.
// The run goes through the existing override with a fixed reason, so the
// hub records it, and carries `simple` so the plain instruction is added.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type Actor, type CreateRunRequest, type Run, type Runner } from "../api";
import { translate } from "../i18n";
import { LOCAL_CAPS, localRunner } from "../local/localFixtures";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { SIMPLE_OVERRIDE_REASON } from "../simple/simpleMode";
import { useApp } from "../store";
import type { Acceptance, Graph, Workflow } from "../types";
import { useBudget } from "./budgetStore";
import { DispatchDialog } from "./DispatchDialog";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const standard: Workflow = {
  preset: "standard",
  stages: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false },
};

const criteria: Acceptance = { items: [{ id: "a1", text: "the page opens" }] };

/** The built-in runner, new enough for staged runs, its repo with or
 * without a test command. */
function runner(verify: boolean): Runner {
  const base = localRunner();
  return {
    ...base,
    info: {
      ...base.info!,
      capabilities: [...LOCAL_CAPS, "roles", "verify"],
      repos: [{ id: "repo1", name: "my-app", path: "D:\\code\\my-app", verify }],
    },
  };
}

const created: Run = {
  id: "run1",
  project: "p",
  nodeId: "alpha",
  runnerId: "local1",
  runnerName: "this machine",
  agent: "claude",
  model: "sonnet",
  repoId: "repo1",
  repoName: "my-app",
  base: "",
  branch: "",
  prompt: "",
  status: "queued",
  exitCode: 0,
  createdBy: "local",
  createdByName: "",
  createdAt: "2026-11-17T08:00:00Z",
};

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function setup(experience: "simple" | "full", acceptance: Acceptance | undefined, verify: boolean) {
  configurePreferenceStorage(fakeStorage({ "vised-experience": experience }));
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: {
      version: 1,
      workflow: standard,
      nodes: [{ id: "alpha", title: "Bakery page", kind: "task", acceptance }],
    } as Graph,
  });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [runner(verify)] });
}

function open(edition: Edition = "single") {
  return render(
    <EditionProvider edition={edition}>
      <DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />
    </EditionProvider>,
  );
}

const NO_CHECKS = translate("dispatch.simpleNoChecksLocal", "en");

function dispatchButton() {
  return screen.getByRole("button", { name: "Dispatch" });
}

async function dispatch(): Promise<CreateRunRequest> {
  const create = vi.spyOn(api, "createRun").mockResolvedValue(created);
  fireEvent.click(dispatchButton());
  await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  return create.mock.calls[0][0];
}

beforeEach(() => {
  viewer.current = null;
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useRuns.getState().reset();
  useBudget.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
    used: { runs: 0, costUsd: 0, tokens: 0 },
    exceeded: "",
  });
});

afterEach(() => vi.restoreAllMocks());

describe("DispatchDialog in Simple mode", () => {
  it("starts a task with no criteria without checks, through the override", async () => {
    setup("simple", undefined, true);
    open();
    expect(await screen.findByText(NO_CHECKS)).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByText(/acceptance criteria/i)).toBeNull();
    expect(screen.queryByRole("list", { name: "Stages" })).toBeNull();
    expect(screen.queryByRole("checkbox", { name: /Skip the stages/ })).toBeNull();
    await screen.findByRole("combobox", { name: "Agent" });
    expect(dispatchButton()).toBeEnabled();
    const body = await dispatch();
    expect(body.override).toBe(SIMPLE_OVERRIDE_REASON);
    expect(body.simple).toBe(true);
    expect(body.pipeline).toBeUndefined();
    expect(body).not.toHaveProperty("role");
  });

  it("starts a Tester stage on a repo without a test command the same way", async () => {
    setup("simple", criteria, false);
    open();
    expect(await screen.findByText(NO_CHECKS)).toBeInTheDocument();
    await screen.findByRole("combobox", { name: "Agent" });
    expect(screen.queryByText(translate("dispatch.noTestCommandLocal", "en"))).toBeNull();
    expect(screen.queryByRole("list", { name: "Stages" })).toBeNull();
    expect(dispatchButton()).toBeEnabled();
    const body = await dispatch();
    expect(body.override).toBe(SIMPLE_OVERRIDE_REASON);
    expect(body.simple).toBe(true);
    expect(body.pipeline).toBeUndefined();
    expect(body).not.toHaveProperty("role");
  });

  it("keeps the stages when nothing would block them", async () => {
    setup("simple", criteria, true);
    open();
    await screen.findByRole("list", { name: "Stages" });
    await screen.findByRole("combobox", { name: "Agent" });
    expect(screen.queryByText(NO_CHECKS)).toBeNull();
    const body = await dispatch();
    expect(body.override).toBeUndefined();
    expect(body.pipeline).toBeDefined();
    expect(body.simple).toBe(true);
  });

  it("blocks as before with Everything", async () => {
    setup("full", undefined, true);
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(/acceptance criteria/i);
    await screen.findByRole("combobox", { name: "Agent" });
    expect(screen.queryByText(NO_CHECKS)).toBeNull();
    expect(dispatchButton()).toBeDisabled();
  });

  it("blocks as before on a team hub, and sends no simple there", async () => {
    viewer.current = { id: "local", name: "alice", role: "admin" };
    setup("simple", undefined, true);
    open("team");
    expect(await screen.findByRole("alert")).toHaveTextContent(/acceptance criteria/i);
    expect(screen.queryByText(NO_CHECKS)).toBeNull();
    await screen.findByRole("combobox", { name: "Agent" });
    expect(dispatchButton()).toBeDisabled();

    // With the criteria there it starts, as any team dispatch: no simple.
    useApp.setState((state) => ({
      graph: { ...state.graph!, nodes: [{ id: "alpha", title: "Bakery page", kind: "task", acceptance: criteria }] } as Graph,
    }));
    await waitFor(() => expect(dispatchButton()).toBeEnabled());
    const body = await dispatch();
    expect(body).not.toHaveProperty("simple");
    expect(body.override).toBeUndefined();
  });
});
