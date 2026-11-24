import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  AuthError,
  BusyError,
  ConflictError,
  RequestError,
  type Actor,
  type BusyResponse,
  type Run,
  type RunBudgetStatus,
  type Runner,
} from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import { DispatchDialog, ownRunners, runnerUnavailableReason } from "./DispatchDialog";
import { useBudget } from "./budgetStore";
import { useCoordStore } from "./coord/coordStore";
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
    capabilities: ["headless", "coord"],
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

const busyPayload: BusyResponse = {
  holders: [
    {
      runId: "holder-1",
      title: "Fix login",
      agent: "claude",
      model: "sonnet",
      mode: "interactive",
      status: "running",
      since: "2026-10-24T08:00:00Z",
      by: "alice",
      nodeId: "alpha",
      mayStop: true,
      sameNode: true,
    },
  ],
  others: 0,
  choices: ["queue", "takeOver", "both"],
};

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

beforeEach(() => {
  viewer.current = alice;
  useApp.setState({ preferences: { ...readPreferences(), language: "en", runMode: "auto" } });
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
  useCoordStore.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue(withinBudget);
  vi.spyOn(api, "getRunBusy").mockResolvedValue({ holders: [], others: 0, choices: [] });
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
  it("offers own headless runners, installed agents, their models and repos as picture cards", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({
      runners: [laptop, desktop, oldRunner, bobs],
    });

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);

    const runners = await screen.findByRole("radiogroup", { name: "Which computer" });
    const runnerRadios = within(runners).getAllByRole("radio") as HTMLButtonElement[];
    expect(runnerRadios.map((el) => el.textContent)).toEqual([
      "laptopoffline",
      "desktop (PC-01)",
      "oldcannot take runs (update the runner)",
    ]);
    expect(runnerRadios.map((el) => el.disabled)).toEqual([true, false, true]);
    expect(checkedRadioText("Which computer")).toBe("desktop (PC-01)");

    expect(radioTexts("Who works")).toEqual(["Claude2.1.0", "Codex"]);
    expect(radioTexts("Which model")).toEqual(["Sonnet", "Opus"]);
    expect(checkedRadioText("Which model")).toBe("Sonnet");

    pickCard("Who works", /Codex/);
    expect(radioTexts("Which model")).toEqual(["gpt-5"]);
    expect(checkedRadioText("Which model")).toBe("gpt-5");

    expect(radioTexts("Which folders")).toEqual(["nodevasH:/visual-editor", "site"]);
    expect(screen.getByText(/waits until someone at the runner accepts it/)).toBeInTheDocument();
  });

  it("leaves a signed-out agent without a card and shows picker.signedOut", async () => {
    const withSignedOut: Runner = {
      ...desktop,
      info: {
        ...desktop.info!,
        agents: [
          { kind: "claude", installed: false, signedOut: true, models: [] },
          { kind: "codex", installed: true, models: [{ id: "gpt-5", default: true }] },
        ],
      },
    };
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [withSignedOut] });

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Who works" });

    expect(radioTexts("Who works")).toEqual(["Codex"]);
    expect(
      screen.getByText("Claude is not signed in on that computer; sign in there to use it"),
    ).toBeInTheDocument();
  });

  it("submits the chosen runner, agent, model, repo, base and instructions", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);
    const onCreated = vi.fn();

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    pickCard("Which model", "Opus");
    pickCard("Which folders", "site");
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
    await screen.findByRole("radiogroup", { name: "Which computer" });
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

  it("preflights node busy status, shows BusyChoice on 409, and resends with holderIds", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const busySpy = vi.spyOn(api, "getRunBusy").mockResolvedValue(busyPayload);
    const create = vi
      .spyOn(api, "createRun")
      .mockRejectedValueOnce(new BusyError("busy", busyPayload))
      .mockResolvedValueOnce(created);
    const onCreated = vi.fn();

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    // Preflight note appears before clicking Dispatch.
    await waitFor(() =>
      expect(busySpy).toHaveBeenCalledWith({ runnerId: "r1", repoId: "repo1", nodeId: "alpha" }),
    );
    expect(await screen.findByTestId("dispatch-busy-preflight")).toHaveTextContent(
      "Someone is already on this",
    );

    // Submitting hits 409 BUSY -> BusyChoice renders.
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    const busyRegion = await screen.findByRole("region", { name: "Someone is already on this" });
    fireEvent.click(within(busyRegion).getByRole("radio", { name: /Stop it and start mine/ }));
    fireEvent.click(within(busyRegion).getByRole("button", { name: "Confirm" }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(create).toHaveBeenNthCalledWith(2, {
      nodeId: "alpha",
      runnerId: "r1",
      agent: "claude",
      model: "sonnet",
      repoId: "repo1",
      onBusy: "takeOver",
      holderIds: ["holder-1"],
    });
  });

  it("shows busy.changed when the holder set changes between 409 and retry", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const updatedBusy: BusyResponse = {
      ...busyPayload,
      holders: [{ ...busyPayload.holders![0], runId: "holder-2", title: "Newer run" }],
    };
    vi.spyOn(api, "createRun")
      .mockRejectedValueOnce(new BusyError("busy", busyPayload))
      .mockRejectedValueOnce(new BusyError("busy changed", updatedBusy));

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    const busyRegion = await screen.findByRole("region", { name: "Someone is already on this" });
    fireEvent.click(within(busyRegion).getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("Something changed; check again")).toBeInTheDocument();
    expect(screen.getByText("Newer run")).toBeInTheDocument();
  });

  it("mounts ContestForm behind the compare toggle and submits a node contest", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const contestRun: Run = { ...created, id: "crun-1", contestId: "c1", contestLabel: "A" };
    const createContest = vi.spyOn(api, "createContest").mockResolvedValue({
      contest: {
        id: "c1",
        title: "Alpha",
        nodeId: "alpha",
        project: "p",
        global: false,
        state: "running",
        entryCount: 2,
        createdBy: "u1",
        createdAt: "2026-10-24T08:00:00Z",
      },
      runs: [contestRun, { ...contestRun, id: "crun-2", contestLabel: "B" }],
    });
    const onCreated = vi.fn();

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    fireEvent.click(screen.getByRole("checkbox", { name: "Compare agents" }));
    expect(screen.getByText("2 agents on the same task")).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Mode" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(contestRun));
    expect(createContest).toHaveBeenCalledWith({
      nodeId: "alpha",
      repoId: "repo1",
      entries: [
        { runnerId: "r1", agent: "claude", model: "sonnet" },
        { runnerId: "r1", agent: "codex", model: "gpt-5" },
      ],
    });
  });

  it("shows an API error inline and stays open", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    vi.spyOn(api, "createRun").mockRejectedValue(new AuthError(403, "remote dispatch is off"));
    const onCreated = vi.fn();

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });
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
    await screen.findByRole("radiogroup", { name: "Which computer" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/This runner cannot take runs/);
  });

  it("shows the hub's own reason for a 409 that names one, such as a missing test command", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const reason =
      'configure a test command for repo on the runner, so it can verify red and green: nodevas runner repo set repo --test-command "go test ./..."';
    vi.spyOn(api, "createRun").mockRejectedValue(new ConflictError("", "", reason, "FAILED_PRECONDITION"));

    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(reason);
    expect(alert).not.toHaveTextContent(/cannot take runs/);
  });

  it("checks the base branch and the instruction size before sending", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });
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
    await screen.findByRole("radiogroup", { name: "Which computer" });

    expect(screen.getByText("nodevas runner repo add <path>")).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Which folders" })).not.toBeInTheDocument();
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
    await screen.findByRole("radiogroup", { name: "Which computer" });
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
    await screen.findByRole("radiogroup", { name: "Which computer" });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Over budget: daily run budget reached: 10 of 10 runs today (UTC)",
    );
    expect(onCreated).not.toHaveBeenCalled();
    expect(await screen.findByText(/Today's budget is used up \(Runs\)/)).toBeInTheDocument();
    expect(budget).toHaveBeenCalledTimes(2);
  });
});
