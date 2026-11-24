import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, BusyError, GlobalRunError, type BusyResponse, type Run, type Runner } from "../api";
import { translate } from "../i18n";
import { localRunner, startingRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useCoordStore } from "../runs/coord/coordStore";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import { useGlobalAgents } from "./globalAgentsStore";
import { SimpleAgentBox } from "./SimpleAgentBox";
import { useStation } from "./stationStore";

vi.mock("../runs/RunTerminal", () => ({
  default: ({ run }: { run: Run }) => <div data-testid="mock-run-terminal">{run.id}</div>,
}));

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);

function interactiveLocalRunner(overrides: Partial<Runner> = {}): Runner {
  const base = localRunner(overrides);
  return {
    ...base,
    info: {
      ...base.info!,
      agents: (base.info?.agents ?? []).map((agent) =>
        agent.installed ? { ...agent, interactive: true } : agent,
      ),
    },
  };
}

function hold(runners: Runner[]) {
  useRunners.setState({ loaded: true, loading: false, runners, error: null, creating: false, revoking: {} });
}

function startedRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "g1",
    project: "",
    nodeId: "",
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
    mode: "interactive",
    global: true,
    exitCode: 0,
    createdBy: "local",
    createdByName: "",
    createdAt: "2026-11-17T08:00:00Z",
    ...overrides,
  };
}

function setPrefs(values: Record<string, unknown>) {
  useApp.setState({ preferences: { ...useApp.getState().preferences, ...values } });
}

function renderBox(edition: "single" | "team" = "single", onStarted = vi.fn()) {
  const view = render(
    <EditionProvider edition={edition}>
      <SimpleAgentBox variant="station" onStarted={onStarted} />
    </EditionProvider>,
  );
  return { onStarted, ...view };
}

const simpleBusy: BusyResponse = {
  holders: [
    {
      runId: "g-hold-1",
      title: "Earlier bakery page",
      agent: "claude",
      model: "sonnet",
      mode: "interactive",
      status: "running",
      since: new Date(Date.now() - 3 * 60_000).toISOString(),
      by: "local",
      repoName: "my-app",
      mayStop: true,
    },
  ],
  others: 0,
  choices: ["queue", "takeOver", "both", "continue"],
  continue: { runId: "g-hold-1", agent: "claude", model: "sonnet", openPR: false },
};

let create: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", experience: "simple", runMode: "auto" } });
  hold([interactiveLocalRunner()]);
  useRuns.getState().reset();
  useRuns.setState({ globalById: {} });
  useGlobalAgents.setState({ runs: [], focus: null });
  useCoordStore.getState().reset();
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [interactiveLocalRunner()] });
  vi.spyOn(api, "getGlobalRunBusy").mockResolvedValue({ repos: [] });
  vi.spyOn(api, "getGlobalRunThread").mockResolvedValue({ runs: [] });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
  vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks: [] });
  create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(startedRun());
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("SimpleAgentBox", () => {
  it.each([
    ["simple", true],
    ["full", false],
  ] as const)("starts interactively by default with mode and renders inline RunDialog simple (%s)", async (experience, simple) => {
    setPrefs({ experience });
    const { onStarted } = renderBox();
    expect(screen.getByText("Claude will work in my-app.")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: /Compare agents/ })).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), {
      target: { value: "  Make a page for my bakery  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0]).toStrictEqual({
      runnerId: "local1",
      repoId: "repo1",
      agent: "claude",
      model: "sonnet",
      mode: "interactive",
      task: "Make a page for my bakery",
      openPR: false,
      simple,
    });
    // Renders inline RunDialog with the plain-word frame.
    expect(
      await screen.findByText("This is Claude's own screen. It may speak English. Type below, or use the buttons."),
    ).toBeInTheDocument();
    expect(await screen.findByTestId("mock-run-terminal")).toHaveTextContent("g1");
    expect(onStarted).toHaveBeenCalledWith(startedRun());
    expect(screen.getByRole("textbox", { name: "What do you want done?" })).toHaveValue("");

    fireEvent.click(screen.getByRole("button", { name: "Watch it work" }));
    expect(useStation.getState().tab).toBe("global");
    expect(useGlobalAgents.getState().focus).toBe("g1");
  });

  it("falls back to background with a plain reason when the agent has not trusted the folder", async () => {
    const base = interactiveLocalRunner();
    hold([
      {
        ...base,
        info: {
          ...base.info!,
          agents: base.info!.agents!.map((a) => (a.kind === "claude" ? { ...a, untrusted: true } : a)),
        },
      },
    ]);
    create.mockResolvedValue(startedRun({ mode: "" }));
    renderBox();

    expect(
      screen.getByText("Claude has not been allowed to work in this folder yet, so it works quietly for now"),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), {
      target: { value: "Make a page" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0]).not.toHaveProperty("mode");
    // Renders background RunDialog transcript (no interactive frame).
    expect(await screen.findByRole("log")).toBeInTheDocument();
    expect(screen.queryByTestId("mock-run-terminal")).not.toBeInTheDocument();
  });

  it("follows the run it started until it ends inside the inline dialog header", async () => {
    create.mockResolvedValue(startedRun({ mode: "" }));
    renderBox();
    const field = screen.getByRole("textbox", { name: "What do you want done?" });
    fireEvent.change(field, { target: { value: "Make a page" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(await screen.findByText("Started. The agent is working on it.")).toBeInTheDocument();

    act(() => useRuns.getState().putRun(startedRun({ mode: "", status: "running" })));
    expect(screen.getByText("Started. The agent is working on it.")).toBeInTheDocument();

    act(() =>
      useRuns.getState().putRun(
        startedRun({ mode: "", status: "succeeded", commit: "c0ffee", finishedAt: "2026-11-17T08:05:00Z" }),
      ),
    );
    const done = screen.getByText(
      'Done. Press "Take a look" to see it, then "Apply to my folder" if you like it.',
    );
    expect(done).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "See the result" }));
    expect(useStation.getState().tab).toBe("global");
    expect(useGlobalAgents.getState().focus).toBe("g1");

    // Ready for the next request.
    expect(field).toHaveValue("");
    expect(field).toBeEnabled();
    expect(screen.getByRole("button", { name: "Start" })).toBeEnabled();
  });

  it("does not say it waits for an OK while a built-in offer is just starting", async () => {
    create.mockResolvedValue(startedRun({ mode: "" }));
    renderBox();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await screen.findByText("Started. The agent is working on it.");

    act(() => useRuns.getState().putRun(startedRun({ mode: "", status: "offered" })));
    expect(screen.getByText("Started. The agent is working on it.")).toBeInTheDocument();
    expect(screen.queryByText(t("simple.box.waitingOk"))).not.toBeInTheDocument();

    const ask = { runId: "g1", where: "app", reasons: null, expiresAt: "2026-11-17T08:10:00Z" };
    act(() => useRuns.getState().putRun(startedRun({ mode: "", status: "offered", ask })));
    expect(screen.getByText(t("simple.box.waitingOk"))).toBeInTheDocument();
  });

  it.each([
    [{ status: "succeeded" as const }, "Done, but the agent did not change any files."],
    [{ status: "failed" as const, error: "exit status 1" }, "The agent could not finish this. Nothing in your folder was changed."],
    [{ status: "cancelled" as const }, "Stopped. Nothing in your folder was changed."],
    [{ status: "declined" as const }, "It did not start. Nothing in your folder was changed."],
    [
      {
        status: "failed" as const,
        error: "Not logged in · Please run /login",
        failureKind: "agent_unavailable",
        failureCode: "signed_out",
      },
      "Claude is on this computer but isn't signed in yet. Open the Terminal app on this computer, type claude, press Enter and sign in. Nodevas notices within a minute; then try again.",
    ],
  ])("says how the run ended in plain words (%o)", async (end, text) => {
    create.mockResolvedValue(startedRun({ mode: "" }));
    renderBox();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await screen.findByText("Started. The agent is working on it.");
    act(() => useRuns.getState().putRun(startedRun({ mode: "", ...end })));
    const line = screen.getByTestId("simple-agent-status");
    expect(line).toHaveTextContent(text);
    expect(line.textContent).not.toMatch(/working on it|exit status|runner/i);
  });

  it("keeps saying how to sign in once the runner reports the agent signed out", async () => {
    const base = interactiveLocalRunner();
    const claude = base.info!.agents![0];
    hold([{ ...base, info: { ...base.info!, agents: [claude] } }]);
    renderBox();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await screen.findByTestId("simple-agent-status");
    act(() =>
      useRuns.getState().putRun(
        startedRun({ status: "failed", error: "Not logged in · Please run /login", failureCode: "signed_out" }),
      ),
    );

    const signedOut = { ...claude, installed: false, signedOut: true, error: "claude: not signed in on this computer" };
    act(() => hold([{ ...base, info: { ...base.info!, agents: [signedOut] } }]));
    const sentence =
      "Claude is on this computer but isn't signed in yet. Open the Terminal app on this computer, type claude, press Enter and sign in. Nodevas notices within a minute; then try again.";
    expect(screen.getByText(sentence)).toBeInTheDocument();
    expect(screen.queryByText("No agent is ready on this computer yet.")).not.toBeInTheDocument();
    expect(screen.queryByText(/Not logged in/)).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "What do you want done?" })).not.toBeInTheDocument();

    act(() => hold([{ ...base, info: { ...base.info!, agents: [claude] } }]));
    expect(screen.getByRole("textbox", { name: "What do you want done?" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start" })).toBeEnabled();
  });

  it("preflights global busy in plain words, shows BusyChoice on 409, and resends with holderIds", async () => {
    const busySpy = vi.spyOn(api, "getGlobalRunBusy").mockResolvedValue({
      repos: [{ repoId: "repo1", ...simpleBusy }],
    });
    const nextRun = startedRun({ id: "g2", afterRun: "g-hold-1" });
    create
      .mockRejectedValueOnce(new BusyError("busy", simpleBusy))
      .mockResolvedValueOnce(nextRun);

    renderBox();
    await waitFor(() => expect(busySpy).toHaveBeenCalledWith("local1", ["repo1"]));
    expect(await screen.findByTestId("simple-busy-preflight")).toHaveTextContent(
      /Your helper is still working in this folder/,
    );

    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Update menu" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));

    const busyRegion = await screen.findByRole("region", { name: /Your helper is still working in this folder/ });
    fireEvent.click(within(busyRegion).getByRole("radio", { name: "Wait, then do mine" }));
    fireEvent.click(within(busyRegion).getByRole("button", { name: "Confirm" }));

    await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
    expect(create).toHaveBeenNthCalledWith(2, {
      runnerId: "local1",
      repoId: "repo1",
      agent: "claude",
      model: "sonnet",
      mode: "interactive",
      task: "Update menu",
      openPR: false,
      simple: true,
      onBusy: "queue",
      holderIds: ["g-hold-1"],
    });
  });

  it("lets the person pick the agent with picture cards right in the box", async () => {
    renderBox();
    const who = screen.getByRole("radiogroup", { name: "Who works" });
    expect(
      within(who)
        .getAllByRole("radio")
        .map((el) => el.querySelector(".picker-card-text")?.textContent ?? ""),
    ).toEqual(["Claude", "Codex"]);
    fireEvent.click(within(who).getByRole("radio", { name: "Codex" }));
    expect(useApp.getState().preferences.simpleAgentKind).toBe("codex");
    expect(screen.getByText("Codex will work in my-app.")).toBeInTheDocument();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ agent: "codex", model: "gpt-5" })));
  });

  it("shows no agent picker with only one agent ready", () => {
    const base = interactiveLocalRunner();
    hold([{ ...base, info: { ...base.info!, agents: [base.info!.agents![0]] } }]);
    renderBox();
    expect(screen.queryByRole("radiogroup", { name: "Who works" })).not.toBeInTheDocument();
    expect(screen.getByText("Claude will work in my-app.")).toBeInTheDocument();
  });

  it("starts nothing for a blank description", () => {
    renderBox();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "   " } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(screen.getByText("Describe what you want first.")).toBeInTheDocument();
    expect(create).not.toHaveBeenCalled();
  });

  it("says why a start was refused", async () => {
    create.mockRejectedValue(new GlobalRunError(429, "BUDGET_EXCEEDED", "", "", 0, "daily tokens", "tokens"));
    renderBox();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(t("runs.overBudget", { message: "daily tokens" }));
  });

  it.each([
    ["turned off", new GlobalRunError(409, "GLOBAL_RUNS_OFF", "off", "", 0, "global runs are off"), "globalAgents.simple.off"],
    ["cannot run", new GlobalRunError(409, "CONFLICT", "", "", 0, "runner cannot run jobs"), "globalAgents.simple.cannotStart"],
    ["outdated", new GlobalRunError(412, "RUNNER_OUTDATED", "", "", 0, "update the runner"), "globalAgents.simple.cannotStart"],
  ])("says a refusal (%s) in plain words, with no command to type", async (_what, error, key) => {
    create.mockRejectedValue(error);
    renderBox();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(t(key));
    expect(alert.textContent).not.toMatch(/runner|nodevas runner set|^'s/i);
  });

  it("offers to set an agent up when none is ready", async () => {
    const base = interactiveLocalRunner();
    hold([{ ...base, info: { ...base.info!, agents: [{ kind: "claude", installed: false, models: [] }] } }]);
    renderBox();
    expect(screen.getByText("No agent is ready on this computer yet.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "What do you want done?" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Set one up" }));
    const dialog = await screen.findByRole("dialog", { name: "Agents on this computer" });
    expect(within(dialog).getByText("Claude", { selector: "strong" })).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    // Escape closes the file picker over it first, then the scan itself.
    vi.spyOn(api, "listDirs").mockResolvedValue({ path: "", parent: "", home: "", dirs: [] });
    fireEvent.click(screen.getByRole("button", { name: "Set one up" }));
    const again = await screen.findByRole("dialog", { name: "Agents on this computer" });
    const claude = within(again).getByText("Claude", { selector: "strong" }).closest("li") as HTMLElement;
    fireEvent.click(within(claude).getByRole("button", { name: "I already have it" }));
    const picker = await screen.findByRole("dialog", { name: "Show Nodevas where Claude is" });
    fireEvent.keyDown(picker, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "Show Nodevas where Claude is" })).not.toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Agents on this computer" })).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("asks for a first folder when there is none", () => {
    vi.spyOn(api, "listDirs").mockResolvedValue({ path: "", parent: "", home: "C:\\Users\\me", dirs: [] });
    const base = interactiveLocalRunner();
    hold([{ ...base, info: { ...base.info!, repos: [] } }]);
    renderBox();
    expect(screen.getByRole("heading", { name: "Where should the agent work?" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "What do you want done?" })).not.toBeInTheDocument();
  });

  it("waits calmly while this computer gets ready", () => {
    hold([startingRunner()]);
    renderBox();
    expect(screen.getByTestId("local-not-ready")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Start" })).not.toBeInTheDocument();
  });

  it("remembers the agent and the folder chosen via cards", async () => {
    const base = interactiveLocalRunner();
    hold([
      {
        ...base,
        info: {
          ...base.info!,
          repos: [...base.info!.repos!, { id: "repo2", name: "bakery", path: "D:\\code\\bakery" }],
        },
      },
    ]);
    renderBox();
    fireEvent.click(screen.getByRole("button", { name: "Change" }));
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "Who works" })).getByRole("radio", { name: "Codex" }));
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "Which folder" })).getByRole("radio", { name: /bakery/ }));
    expect(useApp.getState().preferences.simpleAgentKind).toBe("codex");
    expect(useApp.getState().preferences.simpleAgentRepoId).toBe("repo2");
    expect(screen.getByText("Codex will work in bakery.")).toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() =>
      expect(create).toHaveBeenCalledWith(expect.objectContaining({ agent: "codex", model: "gpt-5", repoId: "repo2" })),
    );
  });

  it("falls back to the first ready agent and folder when the remembered ones are gone", () => {
    setPrefs({ simpleAgentKind: "gemini", simpleAgentRepoId: "gone" });
    renderBox();
    expect(screen.getByText("Claude will work in my-app.")).toBeInTheDocument();
  });

  it("renders nothing on a team hub", () => {
    const { container } = renderBox("team");
    expect(container).toBeEmptyDOMElement();
    act(() => hold([interactiveLocalRunner()]));
    expect(container).toBeEmptyDOMElement();
  });
});
