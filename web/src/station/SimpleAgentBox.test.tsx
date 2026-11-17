import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, GlobalRunError, type Run, type Runner } from "../api";
import { translate } from "../i18n";
import { localRunner, startingRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import { useGlobalAgents } from "./globalAgentsStore";
import { SimpleAgentBox } from "./SimpleAgentBox";
import { useStation } from "./stationStore";

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);

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

let create: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", experience: "simple" } });
  hold([localRunner()]);
  useRuns.getState().reset();
  useRuns.setState({ globalById: {} });
  useGlobalAgents.setState({ runs: [], focus: null });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
  create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(startedRun());
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("SimpleAgentBox", () => {
  it.each([
    ["simple", true],
    ["full", false],
  ] as const)("starts what was described with exactly the fields it needs (%s)", async (experience, simple) => {
    setPrefs({ experience });
    const { onStarted } = renderBox();
    expect(screen.getByText("Claude will work in my-app.")).toBeInTheDocument();
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
      task: "Make a page for my bakery",
      openPR: false,
      simple,
    });
    expect(await screen.findByText("Started. The agent is working on it.")).toBeInTheDocument();
    expect(onStarted).toHaveBeenCalledWith(startedRun());
    expect(screen.getByRole("textbox", { name: "What do you want done?" })).toHaveValue("");

    fireEvent.click(screen.getByRole("button", { name: "Watch it work" }));
    expect(useStation.getState().tab).toBe("global");
    expect(useGlobalAgents.getState().focus).toBe("g1");
  });

  // The started line follows the run: working, then done (pointing to the
  // result buttons), failed or stopped in plain words; the box stays ready.
  it("follows the run it started until it ends", async () => {
    renderBox();
    const field = screen.getByRole("textbox", { name: "What do you want done?" });
    fireEvent.change(field, { target: { value: "Make a page" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(await screen.findByText("Started. The agent is working on it.")).toBeInTheDocument();

    act(() => useRuns.getState().putRun(startedRun({ global: true, status: "running" })));
    expect(screen.getByRole("status")).toHaveTextContent("The agent is working on it.");
    expect(screen.getByRole("button", { name: "Watch it work" })).toBeInTheDocument();

    act(() =>
      useRuns.getState().putRun(
        startedRun({ global: true, status: "succeeded", commit: "c0ffee", finishedAt: "2026-11-17T08:05:00Z" }),
      ),
    );
    const done = screen.getByRole("status");
    expect(done).toHaveTextContent(
      'Done. Press "Take a look" to see it, then "Apply to my folder" if you like it.',
    );
    expect(done.textContent).not.toMatch(/working on it/);
    fireEvent.click(within(done).getByRole("button", { name: "See the result" }));
    expect(useStation.getState().tab).toBe("global");
    expect(useGlobalAgents.getState().focus).toBe("g1");

    // Ready for the next request.
    expect(field).toHaveValue("");
    expect(field).toBeEnabled();
    expect(screen.getByRole("button", { name: "Start" })).toBeEnabled();
  });

  it.each([
    [{ status: "succeeded" as const }, "Done, but the agent did not change any files."],
    [{ status: "failed" as const, error: "exit status 1" }, "The agent could not finish this. Nothing in your folder was changed."],
    [{ status: "cancelled" as const }, "Stopped. Nothing in your folder was changed."],
    [{ status: "declined" as const }, "It did not start. Nothing in your folder was changed."],
  ])("says how the run ended in plain words (%o)", async (end, text) => {
    renderBox();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await screen.findByText("Started. The agent is working on it.");
    act(() => useRuns.getState().putRun(startedRun({ global: true, ...end })));
    const line = screen.getByRole("status");
    expect(line).toHaveTextContent(text);
    expect(line.textContent).not.toMatch(/working on it|exit status|runner/i);
  });

  it("lets the person pick the agent right in the box", async () => {
    renderBox();
    const who = screen.getByRole("combobox", { name: "Who does it" });
    expect(within(who).getAllByRole("option").map((option) => option.textContent)).toEqual(["Claude", "Codex"]);
    fireEvent.change(who, { target: { value: "codex" } });
    expect(useApp.getState().preferences.simpleAgentKind).toBe("codex");
    expect(screen.getByText("Codex will work in my-app.")).toBeInTheDocument();
    fireEvent.change(screen.getByRole("textbox", { name: "What do you want done?" }), { target: { value: "Do it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ agent: "codex", model: "gpt-5" })));
  });

  it("shows no agent picker with only one agent ready", () => {
    const base = localRunner();
    hold([{ ...base, info: { ...base.info!, agents: [base.info!.agents![0]] } }]);
    renderBox();
    expect(screen.queryByRole("combobox", { name: "Who does it" })).not.toBeInTheDocument();
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
    const base = localRunner();
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
    const base = localRunner();
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

  it("remembers the agent and the folder chosen", async () => {
    const base = localRunner();
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
    fireEvent.change(screen.getByRole("combobox", { name: "Who does it" }), { target: { value: "codex" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Folder" }), { target: { value: "repo2" } });
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
    act(() => hold([localRunner()]));
    expect(container).toBeEmptyDOMElement();
  });
});
