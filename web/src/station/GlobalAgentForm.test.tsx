import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  AuthError,
  ConflictError,
  GlobalRunError,
  RequestError,
  type Actor,
  type Run,
  type RunBudgetStatus,
  type Runner,
  type RunnerLoad,
} from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useStepUp } from "../stepup/stepUpStore";
import { useApp } from "../store";
import { GlobalAgentForm, globalRunnerBlock, REPO_ADD_COMMAND, startFailure } from "./GlobalAgentForm";
import { useGlobalAgents } from "./globalAgentsStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const alice: Actor = { id: "u1", name: "alice", role: "admin" };

const CAPS = ["headless", "interactive", "global-runs", "global-runs-on"];

const desktop: Runner = {
  id: "r1",
  name: "desktop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-11-01T00:00:00Z",
  lastSeenAt: "2026-11-14T08:00:00Z",
  online: true,
  info: {
    version: "2.0.0",
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    capabilities: CAPS,
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

const studio: Runner = {
  ...desktop,
  id: "r6",
  name: "studio",
  info: { ...desktop.info!, capabilities: ["headless", "global-runs", "global-runs-on"], repos: [{ id: "repo9", name: "docs" }] },
};
const laptop: Runner = { ...desktop, id: "r2", name: "laptop", online: false };
const oldie: Runner = { ...desktop, id: "r3", name: "oldie", info: { ...desktop.info!, capabilities: ["headless"] } };
const offbox: Runner = {
  ...desktop,
  id: "r4",
  name: "offbox",
  info: { ...desktop.info!, capabilities: ["headless", "global-runs"] },
};
const nohl: Runner = { ...desktop, id: "r5", name: "nohl", info: { ...desktop.info!, capabilities: [] } };
const bare: Runner = { ...desktop, id: "r7", name: "bare", info: { ...desktop.info!, repos: [] } };
const bobs: Runner = { ...desktop, id: "r8", name: "bob-pc", ownerId: "u2", ownerName: "bob" };

const withinBudget: RunBudgetStatus = {
  budget: { dailyRuns: 10, dailyCostUsd: 5, dailyTokens: 0 },
  used: { runs: 2, costUsd: 0.5, tokens: 1000 },
  exceeded: "",
};

function started(overrides: Partial<Run> = {}): Run {
  return {
    id: "g1",
    project: "",
    nodeId: "",
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
    global: true,
    title: "Fix it",
    noPR: true,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-14T08:00:00Z",
    ...overrides,
  };
}

function load(runnerId: string, globalRuns: string): RunnerLoad {
  return {
    runnerId,
    name: runnerId,
    online: true,
    maxConcurrent: 2,
    ceiling: 8,
    settable: "",
    globalRuns,
    busy: 0,
    queued: 0,
    globalActive: 0,
    today: { runs: 0, tokens: 0, costUsd: 0 },
  };
}

function seedRunners(runners: Runner[]) {
  useRunners.setState({ loaded: true, loading: false, runners, error: null, creating: false, revoking: {} });
}

function combo(name: string): HTMLSelectElement {
  return screen.getByRole("combobox", { name }) as HTMLSelectElement;
}

function optionTexts(select: HTMLSelectElement): string[] {
  return within(select)
    .getAllByRole("option")
    .map((option) => option.textContent ?? "");
}

function taskBox(): HTMLTextAreaElement {
  return screen.getByRole("textbox", { name: "Task" }) as HTMLTextAreaElement;
}

function startButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Start" }) as HTMLButtonElement;
}

beforeEach(() => {
  viewer.current = alice;
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  seedRunners([desktop]);
  useGlobalAgents.getState().reset();
  useRuns.getState().reset();
  useRuns.setState({ globalById: {} });
  useBudget.getState().reset();
  useStepUp.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue(withinBudget);
  vi.spyOn(api, "getStepUp").mockResolvedValue({ required: false, active: false });
});

afterEach(() => {
  useStepUp.getState().reset();
  vi.restoreAllMocks();
});

describe("globalRunnerBlock", () => {
  it("reads the runner's own hello when the hub said nothing", () => {
    expect(globalRunnerBlock(desktop, undefined)).toBe("");
    expect(globalRunnerBlock(laptop, undefined)).toBe("offline");
    expect(globalRunnerBlock(nohl, undefined)).toBe("headless");
    expect(globalRunnerBlock(oldie, undefined)).toBe("outdated");
    expect(globalRunnerBlock(offbox, undefined)).toBe("off");
  });

  it("takes the hub's word from the runner's load first", () => {
    expect(globalRunnerBlock(desktop, load("r1", "off"))).toBe("off");
    expect(globalRunnerBlock(laptop, load("r2", ""))).toBe("");
  });
});

describe("GlobalAgentForm", () => {
  it("lists the viewer's runners, the unavailable ones disabled with why", () => {
    seedRunners([desktop, laptop, oldie, offbox, nohl, bobs]);
    render(<GlobalAgentForm />);
    const runner = combo("Runner");
    expect(optionTexts(runner)).toEqual([
      "desktop",
      "laptop (offline)",
      "oldie (update nodevas on it)",
      "offbox (global agents turned off there)",
      "nohl (takes no runs)",
    ]);
    const disabled = within(runner)
      .getAllByRole("option")
      .map((option) => (option as HTMLOptionElement).disabled);
    expect(disabled).toEqual([false, true, true, true, true]);
    expect(runner.value).toBe("r1");
  });

  it("follows the hub's word on a runner over its hello", () => {
    seedRunners([desktop, studio]);
    useGlobalAgents.setState({ runners: [load("r1", "off"), load("r6", "")] });
    render(<GlobalAgentForm />);
    expect(optionTexts(combo("Runner"))).toEqual(["desktop (global agents turned off there)", "studio"]);
    expect(combo("Runner").value).toBe("r6");
  });

  it("says when none of the viewer's runners can start one", () => {
    seedRunners([laptop, oldie]);
    render(<GlobalAgentForm />);
    expect(screen.getByText("None of your runners can start a global agent now.")).toBeInTheDocument();
    expect(combo("Runner")).toBeDisabled();
    expect(screen.queryByRole("textbox", { name: "Task" })).not.toBeInTheDocument();
  });

  it("asks for a runner when the viewer has none", () => {
    seedRunners([bobs]);
    render(<GlobalAgentForm />);
    expect(screen.getByText("You have no runner yet. Add one to start global agents.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add a runner" }));
    expect(useApp.getState().preferences.projectMode).toBe("settings");
  });

  it("waits for the runner list", () => {
    useRunners.setState({ loaded: false, loading: true, runners: [] });
    vi.spyOn(api, "listRunners").mockReturnValue(new Promise(() => undefined));
    render(<GlobalAgentForm />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });

  it("offers the chosen runner's repositories and follows a runner change", () => {
    seedRunners([desktop, studio]);
    render(<GlobalAgentForm />);
    expect(optionTexts(combo("Repository"))).toEqual(["nodevas — H:\\visual-editor", "site"]);
    expect(screen.getByText("Only repositories added on that runner can be used.")).toBeInTheDocument();
    fireEvent.change(combo("Repository"), { target: { value: "repo2" } });
    expect(combo("Repository").value).toBe("repo2");
    fireEvent.change(combo("Runner"), { target: { value: "r6" } });
    expect(optionTexts(combo("Repository"))).toEqual(["docs"]);
    expect(combo("Repository").value).toBe("repo9");
  });

  it("shows the command that adds a repository, and copies it", async () => {
    seedRunners([bare]);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    render(<GlobalAgentForm />);
    expect(screen.getByText("bare has no repositories yet. On that machine, run:")).toBeInTheDocument();
    expect(screen.getByText(REPO_ADD_COMMAND)).toBeInTheDocument();
    expect(REPO_ADD_COMMAND).toBe("nodevas runner repo add <path>");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
    expect(writeText).toHaveBeenCalledWith("nodevas runner repo add <path>");
    expect(startButton()).toBeDisabled();
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  });

  it("offers the installed agents and their models, the default first chosen", () => {
    render(<GlobalAgentForm />);
    expect(optionTexts(combo("Agent"))).toEqual(["claude · 2.1.0", "codex"]);
    expect(optionTexts(combo("Model"))).toEqual(["Opus", "Sonnet (default)"]);
    expect(combo("Model").value).toBe("sonnet");
    fireEvent.change(combo("Agent"), { target: { value: "codex" } });
    expect(combo("Model").value).toBe("gpt-5");
  });

  it("offers interactive only on a runner that can do it", () => {
    seedRunners([studio]);
    const { unmount } = render(<GlobalAgentForm />);
    expect(screen.getByRole("radio", { name: "Interactive" })).toBeDisabled();
    unmount();
    seedRunners([desktop]);
    render(<GlobalAgentForm />);
    expect(screen.getByRole("radio", { name: "Interactive" })).toBeEnabled();
  });

  it("checks the base branch and the branch name while typing", () => {
    render(<GlobalAgentForm />);
    fireEvent.change(taskBox(), { target: { value: "Fix it" } });
    const base = screen.getByRole("textbox", { name: "Base branch" });
    expect(base).toHaveAttribute("placeholder", "Repository default");
    fireEvent.change(base, { target: { value: "-oops" } });
    expect(screen.getByText(translate("runs.baseInvalid", "en"))).toBeInTheDocument();
    expect(base).toHaveAttribute("aria-invalid", "true");
    expect(startButton()).toBeDisabled();
    fireEvent.change(base, { target: { value: "release/2.0" } });
    expect(startButton()).toBeEnabled();

    const slug = screen.getByRole("textbox", { name: "Branch name" });
    fireEvent.change(slug, { target: { value: "Bad_Slug" } });
    expect(screen.getByText("Lowercase letters, digits and single dashes, at most 40")).toBeInTheDocument();
    expect(slug).toHaveAttribute("aria-invalid", "true");
    expect(startButton()).toBeDisabled();
    fireEvent.change(slug, { target: { value: "ok-slug" } });
    expect(startButton()).toBeEnabled();
  });

  it("names the branch from the task's first line until the person writes one", () => {
    render(<GlobalAgentForm />);
    const slug = screen.getByRole("textbox", { name: "Branch name" }) as HTMLInputElement;
    expect(slug.value).toBe("task");
    fireEvent.change(taskBox(), { target: { value: "\n  Fix the Login form!\nThen tell me" } });
    expect(slug.value).toBe("fix-the-login-form");
    expect(screen.getByText("The runner creates agent/fix-the-login-form-xxxxxxxx")).toBeInTheDocument();
    fireEvent.change(taskBox(), { target: { value: "修正登入表單" } });
    expect(slug.value).toBe("task");
    fireEvent.change(slug, { target: { value: "my-branch" } });
    fireEvent.change(taskBox(), { target: { value: "Something else" } });
    expect(slug.value).toBe("my-branch");
    expect(screen.getByText("The runner creates agent/my-branch-xxxxxxxx")).toBeInTheDocument();
  });

  it("asks for a task, and counts its size against the hub's limit", () => {
    const create = vi.spyOn(api, "createGlobalRun");
    render(<GlobalAgentForm />);
    expect(screen.getByText("0.0 / 32 KB")).toBeInTheDocument();
    fireEvent.click(startButton());
    expect(screen.getByText("Write the task first")).toBeInTheDocument();
    expect(taskBox()).toHaveAttribute("aria-invalid", "true");
    expect(taskBox()).toHaveFocus();
    expect(create).not.toHaveBeenCalled();

    fireEvent.change(taskBox(), { target: { value: "a".repeat(2048) } });
    expect(screen.getByText("2.0 / 32 KB")).toBeInTheDocument();
    expect(screen.queryByText("Write the task first")).not.toBeInTheDocument();
    fireEvent.change(taskBox(), { target: { value: "a".repeat(32 * 1024 + 1) } });
    expect(screen.getByText("The task is over 32 KB")).toBeInTheDocument();
    expect(startButton()).toBeDisabled();
  });

  it("grows the task box with its lines", () => {
    render(<GlobalAgentForm />);
    expect(taskBox().rows).toBe(4);
    fireEvent.change(taskBox(), { target: { value: Array.from({ length: 8 }, (_, i) => `line ${i}`).join("\n") } });
    expect(taskBox().rows).toBe(9);
    fireEvent.change(taskBox(), { target: { value: "x\n".repeat(40) } });
    expect(taskBox().rows).toBe(16);
  });

  it("sends exactly what was chosen: no pull request, no slug unless written", async () => {
    const run = started();
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(run);
    const onStarted = vi.fn();
    render(<GlobalAgentForm onStarted={onStarted} />);
    expect(screen.getByRole("checkbox", { name: "Open a pull request when done" })).not.toBeChecked();
    expect(
      screen.getByText("Off: the branch stays on the runner; merge or discard it from its workspace."),
    ).toBeInTheDocument();
    expect(screen.getByText(translate("globalAgents.form.noBoard", "en"))).toBeInTheDocument();
    fireEvent.change(taskBox(), { target: { value: "  Fix it\nproperly  " } });
    fireEvent.click(startButton());
    await waitFor(() => expect(onStarted).toHaveBeenCalledWith(run));
    expect(create).toHaveBeenCalledWith({
      runnerId: "r1",
      repoId: "repo1",
      agent: "claude",
      model: "sonnet",
      task: "Fix it\nproperly",
      openPR: false,
    });
    // Ready for the next one: the place stays, the task goes.
    expect(taskBox().value).toBe("");
    expect(combo("Runner").value).toBe("r1");
    expect(combo("Model").value).toBe("sonnet");
    expect(useGlobalAgents.getState().focus).toBe("g1");
    expect(useRuns.getState().globalById.g1).toEqual(run);
  });

  it("sends the mode, base, written slug and pull request when chosen", async () => {
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(started());
    render(<GlobalAgentForm />);
    fireEvent.change(combo("Agent"), { target: { value: "codex" } });
    fireEvent.change(combo("Repository"), { target: { value: "repo2" } });
    fireEvent.click(screen.getByRole("radio", { name: "Interactive" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Base branch" }), { target: { value: " develop " } });
    fireEvent.change(screen.getByRole("textbox", { name: "Branch name" }), { target: { value: "login-fix" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "Open a pull request when done" }));
    fireEvent.change(taskBox(), { target: { value: "Fix the login" } });
    fireEvent.click(startButton());
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith({
      runnerId: "r1",
      repoId: "repo2",
      agent: "codex",
      model: "gpt-5",
      mode: "interactive",
      base: "develop",
      task: "Fix the login",
      slug: "login-fix",
      openPR: true,
    });
  });

  it("starts on Ctrl+Enter or ⌘+Enter in the task", async () => {
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(started());
    render(<GlobalAgentForm />);
    expect(screen.getByText("Ctrl+Enter to start")).toBeInTheDocument();
    fireEvent.change(taskBox(), { target: { value: "Fix it" } });
    fireEvent.keyDown(taskBox(), { key: "Enter" });
    expect(create).not.toHaveBeenCalled();
    fireEvent.keyDown(taskBox(), { key: "Enter", ctrlKey: true });
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    fireEvent.change(taskBox(), { target: { value: "Again" } });
    fireEvent.keyDown(taskBox(), { key: "Enter", metaKey: true });
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
  });

  it("marks the field the hub refused", async () => {
    vi.spyOn(api, "createGlobalRun").mockRejectedValue(
      new GlobalRunError(400, "INVALID_ARGUMENT", "", "task", 0, "task holds a NUL byte"),
    );
    render(<GlobalAgentForm />);
    fireEvent.change(taskBox(), { target: { value: "Fix it" } });
    fireEvent.click(startButton());
    expect(await screen.findByRole("alert")).toHaveTextContent("task holds a NUL byte");
    expect(taskBox()).toHaveAttribute("aria-invalid", "true");
  });

  it("opens the step-up when the hub wants one first", async () => {
    vi.spyOn(api, "createGlobalRun").mockRejectedValue(
      new GlobalRunError(403, "STEP_UP_REQUIRED", "", "", 0, "verify it's you first"),
    );
    render(<GlobalAgentForm />);
    fireEvent.change(taskBox(), { target: { value: "Fix it" } });
    fireEvent.click(startButton());
    expect(await screen.findByRole("dialog", { name: "Verify it's you" })).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("verify it's you first");
  });

  it("warns up front when today's budget is used up, and links to it", async () => {
    vi.spyOn(api, "getRunBudget").mockResolvedValue({
      ...withinBudget,
      used: { runs: 0, costUsd: 5.1, tokens: 0 },
      exceeded: "cost",
    });
    const opened = vi.fn();
    window.addEventListener("nodevas-open-budget", opened);
    render(<GlobalAgentForm />);
    expect(await screen.findByText(/Today's budget is used up \(Estimated cost\)/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Adjust budget" }));
    expect(opened).toHaveBeenCalled();
    window.removeEventListener("nodevas-open-budget", opened);
  });

  it("shows the hub's 429 and catches the budget line up", async () => {
    const budget = vi
      .spyOn(api, "getRunBudget")
      .mockResolvedValueOnce(withinBudget)
      .mockResolvedValue({ ...withinBudget, used: { runs: 10, costUsd: 0, tokens: 0 }, exceeded: "runs" });
    vi.spyOn(api, "createGlobalRun").mockRejectedValue(
      new GlobalRunError(429, "BUDGET_EXCEEDED", "", "", 0, "daily run budget reached", "runs"),
    );
    render(<GlobalAgentForm />);
    await waitFor(() => expect(budget).toHaveBeenCalledTimes(1));
    fireEvent.change(taskBox(), { target: { value: "Fix it" } });
    await act(async () => {
      fireEvent.click(startButton());
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("Over budget: daily run budget reached");
    expect(await screen.findByText(/Today's budget is used up \(Runs\)/)).toBeInTheDocument();
  });

  it("offers Close only when the panel lets it close", () => {
    const onClose = vi.fn();
    const { unmount } = render(<GlobalAgentForm onClose={onClose} />);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalled();
    unmount();
    render(<GlobalAgentForm />);
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
  });
});

describe("startFailure", () => {
  const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);
  const context = { runner: "desktop", interactive: false };
  const failure = (error: unknown, interactive = false) => startFailure(t, error, { ...context, interactive });

  it("words each refusal of §5.2", () => {
    expect(failure(new GlobalRunError(400, "INVALID_ARGUMENT", "", "slug", 0, "bad slug"))).toEqual({
      text: "bad slug",
      field: "slug",
      stepUp: false,
      budget: false,
    });
    expect(failure(new GlobalRunError(403, "PERMISSION_DENIED", "", "", 0, "agents cannot use global agents")).text).toBe(
      "Not allowed: agents cannot use global agents",
    );
    expect(failure(new GlobalRunError(403, "STEP_UP_REQUIRED", "", "", 0, "verify first")).stepUp).toBe(true);
    expect(failure(new GlobalRunError(404, "NOT_FOUND", "", "", 0, "runner not found")).text).toBe("runner not found");
    expect(failure(new GlobalRunError(409, "GLOBAL_RUNS_OFF", "off", "", 0, "turned off")).text).toBe(
      "desktop's owner turned global agents off (nodevas runner set globalRuns on)",
    );
    expect(failure(new GlobalRunError(409, "FAILED_PRECONDITION", "", "", 0, "the runner takes no runs")).text).toBe(
      "the runner takes no runs",
    );
    expect(failure(new GlobalRunError(409, "FAILED_PRECONDITION", "", "", 0, "409")).text).toBe(
      translate("runs.runnerCannotRun", "en"),
    );
    expect(failure(new GlobalRunError(409, "FAILED_PRECONDITION", "", "", 0, ""), true).text).toBe(
      translate("terminal.runnerCannotInteract", "en"),
    );
    expect(failure(new GlobalRunError(412, "RUNNER_OUTDATED", "outdated", "", 0, "outdated")).text).toBe(
      "Update nodevas on desktop to start global agents there",
    );
    expect(failure(new GlobalRunError(429, "BUDGET_EXCEEDED", "", "", 0, "daily cap", "cost"))).toEqual({
      text: "Over budget: daily cap",
      field: "",
      stepUp: false,
      budget: true,
    });
  });

  it("words the generic errors too", () => {
    expect(failure(new AuthError(403, "nope")).text).toBe("Not allowed: nope");
    expect(failure(new AuthError(403, "verify", "STEP_UP_REQUIRED")).stepUp).toBe(true);
    expect(failure(new ConflictError("", "", "old runner")).text).toBe("old runner");
    expect(failure(new RequestError(429, "too much")).budget).toBe(true);
    expect(failure(new Error("boom")).text).toBe("boom");
  });

  it("speaks Traditional Chinese for a zh-TW reader", () => {
    const zh = (key: string, values?: Record<string, string | number>) => translate(key, "zh-TW", values);
    expect(startFailure(zh, new GlobalRunError(412, "RUNNER_OUTDATED", "", "", 0, ""), context).text).toBe(
      "請更新 desktop 上的 nodevas 才能在那裡啟動全域 agent",
    );
  });
});
