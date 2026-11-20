import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  AuthError,
  BusyError,
  ConflictError,
  GlobalRunError,
  RequestError,
  type Actor,
  type BusyResponse,
  type Run,
  type RunBudgetStatus,
  type Runner,
  type RunnerLoad,
} from "../api";
import { translate } from "../i18n";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useCoordStore } from "../runs/coord/coordStore";
import { useRuns } from "../runs/runStore";
import { useStepUp } from "../stepup/stepUpStore";
import { useApp } from "../store";
import { GlobalAgentForm, globalRunnerBlock, REPO_ADD_COMMAND, startFailure } from "./GlobalAgentForm";
import { useGlobalAgents } from "./globalAgentsStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const alice: Actor = { id: "u1", name: "alice", role: "admin" };

const CAPS = ["headless", "interactive", "global-runs", "global-runs-on", "coord"];

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
        interactive: true,
        version: "2.1.0",
        models: [
          { id: "opus", label: "Opus" },
          { id: "sonnet", label: "Sonnet", default: true },
        ],
      },
      { kind: "gemini", installed: false, models: [{ id: "flash" }] },
      { kind: "codex", installed: true, interactive: true, models: [{ id: "gpt-5" }] },
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
  info: {
    ...desktop.info!,
    capabilities: ["headless", "global-runs", "global-runs-on"],
    repos: [{ id: "repo9", name: "docs" }],
  },
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

const busyPayload: BusyResponse = {
  holders: [
    {
      runId: "g-hold-1",
      title: "Earlier job",
      agent: "claude",
      model: "sonnet",
      mode: "interactive",
      status: "running",
      since: "2026-11-14T07:50:00Z",
      by: "alice",
      repoName: "nodevas",
      mayStop: true,
    },
  ],
  others: 0,
  choices: ["queue", "takeOver", "both", "continue"],
  continue: { runId: "g-hold-1", agent: "claude", model: "sonnet", openPR: false },
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

function radioGroup(name: string) {
  return screen.getByRole("radiogroup", { name });
}

function cardText(el: HTMLElement): string {
  return (el.querySelector(".picker-card-text")?.textContent ?? el.textContent ?? "").trim();
}

function radioTexts(groupName: string): string[] {
  return within(radioGroup(groupName))
    .getAllByRole("radio")
    .map(cardText);
}

function checkedRadioText(groupName: string): string {
  const checked = within(radioGroup(groupName))
    .getAllByRole("radio")
    .find((el) => el.getAttribute("aria-checked") === "true");
  return checked ? cardText(checked) : "";
}

function pickCard(groupName: string, label: string | RegExp) {
  fireEvent.click(within(radioGroup(groupName)).getByRole("radio", { name: label }));
}

function repoGroup() {
  return screen.getByRole("group", { name: "Which folders" });
}

function taskBox(): HTMLTextAreaElement {
  return screen.getByRole("textbox", { name: "Task" }) as HTMLTextAreaElement;
}

function startButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Start" }) as HTMLButtonElement;
}

beforeEach(() => {
  viewer.current = alice;
  useApp.setState({ preferences: { ...readPreferences(), language: "en", runMode: "auto" } });
  seedRunners([desktop]);
  useGlobalAgents.getState().reset();
  useRuns.getState().reset();
  useRuns.setState({ globalById: {} });
  useBudget.getState().reset();
  useStepUp.getState().reset();
  useCoordStore.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue(withinBudget);
  vi.spyOn(api, "getStepUp").mockResolvedValue({ required: false, active: false });
  vi.spyOn(api, "getGlobalRunBusy").mockResolvedValue({ repos: [] });
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
    const radios = within(radioGroup("Which computer")).getAllByRole("radio") as HTMLButtonElement[];
    expect(radios.map((el) => el.textContent)).toEqual([
      "desktop",
      "laptoplaptop (offline)",
      "oldieoldie (update nodevas on it)",
      "offboxoffbox (global agents turned off there)",
      "nohlnohl (takes no runs)",
    ]);
    expect(radios.map((el) => el.disabled)).toEqual([false, true, true, true, true]);
    expect(checkedRadioText("Which computer")).toBe("desktop");
  });

  it("follows the hub's word on a runner over its hello", () => {
    seedRunners([desktop, studio]);
    useGlobalAgents.setState({ runners: [load("r1", "off"), load("r6", "")] });
    render(<GlobalAgentForm />);
    expect(radioTexts("Which computer")).toEqual(["desktopdesktop (global agents turned off there)", "studio"]);
    expect(checkedRadioText("Which computer")).toBe("studio");
  });

  it("says when none of the viewer's runners can start one", () => {
    seedRunners([laptop, oldie]);
    render(<GlobalAgentForm />);
    expect(screen.getByText("None of your runners can start a global agent now.")).toBeInTheDocument();
    const radios = within(radioGroup("Which computer")).getAllByRole("radio") as HTMLButtonElement[];
    expect(radios.every((el) => el.disabled)).toBe(true);
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

  it("offers the chosen runner's repositories as checkboxes and follows a runner change", () => {
    seedRunners([desktop, studio]);
    render(<GlobalAgentForm />);
    const repos = repoGroup();
    const checkboxes = within(repos).getAllByRole("checkbox") as HTMLInputElement[];
    expect(checkboxes).toHaveLength(2);
    expect(checkboxes[0]).toBeChecked();
    expect(checkboxes[1]).not.toBeChecked();
    expect(screen.getByText("Only repositories added on that runner can be used.")).toBeInTheDocument();

    fireEvent.click(checkboxes[1]);
    expect(checkboxes[1]).toBeChecked();

    pickCard("Which computer", "studio");
    const studioBoxes = within(repoGroup()).getAllByRole("checkbox") as HTMLInputElement[];
    expect(studioBoxes).toHaveLength(1);
    expect(studioBoxes[0]).toBeChecked();
    expect(within(repoGroup()).getByText("docs")).toBeInTheDocument();
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

  it("offers the installed agents and their models as cards, the default first chosen, and notes signed-out agents", () => {
    const withSignedOut: Runner = {
      ...desktop,
      info: {
        ...desktop.info!,
        agents: [
          ...desktop.info!.agents!.filter((a) => a.kind !== "gemini"),
          { kind: "gemini", installed: false, signedOut: true, models: [] },
        ],
      },
    };
    seedRunners([withSignedOut]);
    render(<GlobalAgentForm />);
    expect(radioTexts("Who works")).toEqual(["Claude2.1.0", "Codex"]);
    expect(
      screen.getByText("Gemini is not signed in on that computer; sign in there to use it"),
    ).toBeInTheDocument();
    expect(radioTexts("Which model")).toEqual(["Sonnet", "Opus"]);
    expect(checkedRadioText("Which model")).toBe("Sonnet");
    pickCard("Who works", /Codex/);
    expect(checkedRadioText("Which model")).toBe("gpt-5");
  });

  it("defaults to interactive when supported, disables it when unsupported, and remembers runMode", () => {
    seedRunners([studio]);
    const { unmount } = render(<GlobalAgentForm />);
    expect(screen.getByRole("radio", { name: "Talk with it" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Background" })).toHaveAttribute("aria-checked", "true");
    unmount();

    seedRunners([desktop]);
    const view = render(<GlobalAgentForm />);
    expect(screen.getByRole("radio", { name: "Talk with it" })).toBeEnabled();
    expect(screen.getByRole("radio", { name: "Talk with it" })).toHaveAttribute("aria-checked", "true");

    fireEvent.click(screen.getByRole("radio", { name: "Background" }));
    expect(useApp.getState().preferences.runMode).toBe("background");
    view.unmount();

    render(<GlobalAgentForm />);
    expect(screen.getByRole("radio", { name: "Background" })).toHaveAttribute("aria-checked", "true");
  });

  it("sends repoIds to createGlobalGroup and forces background when several folders are picked", async () => {
    const groupRuns = [started({ id: "g1", repoId: "repo1", groupId: "grp1" }), started({ id: "g2", repoId: "repo2", groupId: "grp1" })];
    const createGroup = vi.spyOn(api, "createGlobalGroup").mockResolvedValue({
      group: {
        id: "grp1",
        runnerId: "r1",
        createdBy: "u1",
        title: "Bump deps",
        agent: "claude",
        model: "sonnet",
        noPR: true,
        repoCount: 2,
        createdAt: "2026-11-14T08:00:00Z",
      },
      runs: groupRuns,
    });
    const onStarted = vi.fn();
    render(<GlobalAgentForm onStarted={onStarted} />);

    // Pick both folders -> becomes a group (background only).
    fireEvent.click(within(repoGroup()).getByRole("checkbox", { name: /site/ }));
    expect(screen.getByRole("radio", { name: "Talk with it" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Background" })).toHaveAttribute("aria-checked", "true");
    expect(
      screen.getByText("Several folders run in the background; talk to any one after it finishes"),
    ).toBeInTheDocument();

    fireEvent.change(taskBox(), { target: { value: "Bump deps" } });
    fireEvent.click(startButton());

    await waitFor(() => expect(createGroup).toHaveBeenCalledTimes(1));
    const sent = createGroup.mock.calls[0][0];
    expect(sent).toEqual({
      runnerId: "r1",
      repoIds: ["repo1", "repo2"],
      agent: "claude",
      model: "sonnet",
      task: "Bump deps",
      openPR: false,
    });
    expect(sent).not.toHaveProperty("mode");
    expect(onStarted).toHaveBeenCalledWith(groupRuns[0]);
  });

  it("trims picked folders when a group start hits 429 with room", async () => {
    vi.spyOn(api, "createGlobalGroup").mockRejectedValue(
      new GlobalRunError(429, "BUDGET_EXCEEDED", "", "", 0, "only 1 fits", "runs", "", 1),
    );
    render(<GlobalAgentForm />);
    fireEvent.click(within(repoGroup()).getByRole("checkbox", { name: /site/ }));
    fireEvent.change(taskBox(), { target: { value: "Bump deps" } });
    fireEvent.click(startButton());

    expect(await screen.findByRole("alert")).toHaveTextContent("Today's limit leaves room for 1 runs");
    const boxes = within(repoGroup()).getAllByRole("checkbox") as HTMLInputElement[];
    expect(boxes[0]).toBeChecked();
    expect(boxes[1]).not.toBeChecked();
  });

  it("preflights global busy, opens BusyChoice on 409, and resends with holderIds", async () => {
    const busySpy = vi.spyOn(api, "getGlobalRunBusy").mockResolvedValue({
      repos: [{ repoId: "repo1", ...busyPayload }],
    });
    const run = started({ id: "g2", continueOf: "g-hold-1" });
    const create = vi
      .spyOn(api, "createGlobalRun")
      .mockRejectedValueOnce(new BusyError("busy", busyPayload))
      .mockResolvedValueOnce(run);
    const onStarted = vi.fn();

    render(<GlobalAgentForm onStarted={onStarted} />);
    await waitFor(() => expect(busySpy).toHaveBeenCalledWith("r1", ["repo1"]));
    expect(await screen.findByTestId("global-busy-preflight")).toHaveTextContent(
      "Someone is already on this",
    );

    fireEvent.change(taskBox(), { target: { value: "Also fix footer" } });
    fireEvent.click(startButton());

    const busyRegion = await screen.findByRole("region", { name: "Someone is already on this" });
    fireEvent.click(within(busyRegion).getByRole("radio", { name: /Add this to its work instead/ }));
    fireEvent.click(within(busyRegion).getByRole("button", { name: "Confirm" }));

    await waitFor(() => expect(onStarted).toHaveBeenCalledWith(run));
    expect(create).toHaveBeenNthCalledWith(2, {
      runnerId: "r1",
      repoId: "repo1",
      agent: "claude",
      model: "sonnet",
      mode: "interactive",
      task: "Also fix footer",
      openPR: false,
      onBusy: "continue",
      holderIds: ["g-hold-1"],
    });
  });

  it("renders ContestForm when Compare agents (比稿) is toggled and calls createGlobalContest", async () => {
    const contestRun = started({ id: "gc1", contestId: "c1", contestLabel: "A" });
    const createContest = vi.spyOn(api, "createGlobalContest").mockResolvedValue({
      contest: {
        id: "c1",
        title: "Refactor auth",
        global: true,
        state: "running",
        entryCount: 2,
        createdBy: "u1",
        createdAt: "2026-11-14T08:00:00Z",
      },
      runs: [contestRun, started({ id: "gc2", contestId: "c1", contestLabel: "B" })],
    });
    const onStarted = vi.fn();

    render(<GlobalAgentForm onStarted={onStarted} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Compare agents (比稿)" }));
    expect(screen.getByText("2 agents on the same task")).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Mode" })).not.toBeInTheDocument();

    fireEvent.change(taskBox(), { target: { value: "Refactor auth" } });
    fireEvent.click(startButton());

    await waitFor(() => expect(onStarted).toHaveBeenCalledWith(contestRun));
    expect(createContest).toHaveBeenCalledWith({
      runnerId: "r1",
      repoId: "repo1",
      task: "Refactor auth",
      entries: [
        { agent: "claude", model: "sonnet" },
        { agent: "codex", model: "gpt-5" },
      ],
    });
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

  it("sends exactly what was chosen: interactive by default, no pull request, no slug unless written", async () => {
    const run = started({ mode: "interactive" });
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
      mode: "interactive",
      task: "Fix it\nproperly",
      openPR: false,
    });
    // Ready for the next one: the place stays, the task goes.
    expect(taskBox().value).toBe("");
    expect(checkedRadioText("Which computer")).toBe("desktop");
    expect(checkedRadioText("Which model")).toBe("Sonnet");
    expect(useGlobalAgents.getState().focus).toBe("g1");
    expect(useRuns.getState().globalById.g1).toEqual(run);
  });

  it("sends background mode, base, written slug and pull request when chosen", async () => {
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(started());
    render(<GlobalAgentForm />);
    pickCard("Who works", /Codex/);
    // Uncheck repo1 and check repo2 so only repo2 is selected.
    fireEvent.click(within(repoGroup()).getByRole("checkbox", { name: /nodevas/ }));
    fireEvent.click(within(repoGroup()).getByRole("checkbox", { name: /site/ }));
    fireEvent.click(screen.getByRole("radio", { name: "Background" }));
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

// Simple mode (single-user edition): finished work stays on this computer,
// so there is no pull request to ask for, no contest toggle, and the hub adds
// its plain-words instruction to the task.
describe("GlobalAgentForm in Simple mode", () => {
  const PR = "Open a pull request when done";

  function simpleForm(experience: "simple" | "full") {
    viewer.current = null;
    useApp.setState((state) => ({ preferences: { ...state.preferences, experience, runMode: "auto" } }));
    seedRunners([localRunner()]);
    return render(
      <EditionProvider edition="single">
        <GlobalAgentForm />
      </EditionProvider>,
    );
  }

  it("sends simple and no pull request, with no pull request box and no contest toggle", async () => {
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(started({ runnerId: "local1" }));
    simpleForm("simple");
    expect(screen.queryByRole("checkbox", { name: PR })).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: /Compare agents/ })).not.toBeInTheDocument();
    fireEvent.change(taskBox(), { target: { value: "Make a page for my bakery" } });
    fireEvent.click(startButton());
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith({
      runnerId: "local1",
      repoId: "repo1",
      agent: "claude",
      model: "sonnet",
      task: "Make a page for my bakery",
      openPR: false,
      simple: true,
    });
  });

  it("is unchanged with Everything", async () => {
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(started({ runnerId: "local1" }));
    simpleForm("full");
    fireEvent.click(screen.getByRole("checkbox", { name: PR }));
    fireEvent.change(taskBox(), { target: { value: "Fix it" } });
    fireEvent.click(startButton());
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith({
      runnerId: "local1",
      repoId: "repo1",
      agent: "claude",
      model: "sonnet",
      task: "Fix it",
      openPR: true,
    });
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
