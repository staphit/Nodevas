// The dispatch dialog and the global-agent form in the single-user edition:
// no runner to pick, repos added right there, and not one word of "runner"
// outside a collapsed Advanced.

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type Actor, type RunBudgetStatus, type Runner, type RunnerRepo } from "../api";
import { localRunner, restartedRunner, startingRunner, visibleText } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { GlobalAgentForm } from "../station/GlobalAgentForm";
import { useGlobalAgents } from "../station/globalAgentsStore";
import { useApp } from "../store";
import { useBudget } from "./budgetStore";
import { DispatchDialog } from "./DispatchDialog";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const withinBudget: RunBudgetStatus = {
  budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
  used: { runs: 0, costUsd: 0, tokens: 0 },
  exceeded: "",
};

const big: RunnerRepo = { id: "repo9", name: "big", path: "D:\\code\\big" };

const STARTING = "Getting this computer ready to run agents… This usually takes a few seconds.";
const UNAVAILABLE = "The part of Nodevas that runs agents isn't running. Restart Nodevas.";

/** A runner the person connected by hand beside the app's own. */
const laptop: Runner = {
  ...localRunner(),
  id: "r9",
  name: "laptop",
  builtIn: undefined,
  hubLocal: undefined,
};

function withRepos(runner: Runner, repos: RunnerRepo[]): Runner {
  return { ...runner, info: { ...runner.info!, repos } };
}

function withLocal(runner: Runner, local: Partial<NonNullable<NonNullable<Runner["info"]>["local"]>>): Runner {
  return { ...runner, info: { ...runner.info!, local: { ...runner.info!.local!, ...local } } };
}

function serve(runners: Runner[]) {
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners });
}

function dispatchDialog(onClose = vi.fn()) {
  return render(
    <EditionProvider edition="single">
      <DispatchDialog nodeId="alpha" onClose={onClose} onCreated={vi.fn()} />
    </EditionProvider>,
  );
}

function globalForm() {
  return render(
    <EditionProvider edition="single">
      <GlobalAgentForm />
    </EditionProvider>,
  );
}

/** Adds D:\code\big through the folder dialog its "+ Add repo" opens. */
async function addBig(after: Runner) {
  vi.spyOn(api, "listDirs").mockImplementation(async (path = "") =>
    path === ""
      ? { path: "", parent: "", dirs: [{ name: "big", path: "D:\\code\\big" }] }
      : { path, parent: "D:\\code", dirs: [] },
  );
  vi.spyOn(api, "inspectRepo").mockResolvedValue({ path: "D:\\code\\big", state: "repo", repo: big });
  vi.spyOn(api, "addRepo").mockResolvedValue({ repo: big, runner: after });
  serve([after]);
  fireEvent.click(screen.getByRole("button", { name: "+ Add repo" }));
  const dialog = await screen.findByRole("dialog", { name: "Add a repo" });
  fireEvent.click(await within(dialog).findByRole("button", { name: /big/ }));
  await within(dialog).findByText("D:\\code\\big");
  fireEvent.click(within(dialog).getByRole("button", { name: "Use this folder" }));
  fireEvent.click(await within(dialog).findByRole("button", { name: "Add" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add a repo" })).not.toBeInTheDocument());
}

beforeEach(() => {
  viewer.current = null;
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useRuns.getState().reset();
  useBudget.getState().reset();
  useGlobalAgents.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue(withinBudget);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("DispatchDialog, single-user edition", () => {
  it("has no runner to pick, starts right away, and never says runner", async () => {
    serve([localRunner()]);
    dispatchDialog();
    const dialog = await screen.findByRole("dialog", { name: "Dispatch to agent" });
    await screen.findByRole("combobox", { name: "Agent" });
    expect(screen.queryByRole("combobox", { name: "Runner" })).not.toBeInTheDocument();
    expect(dialog).toHaveTextContent("on this computer");
    expect(screen.getByTestId("dispatch-auto-accept")).toHaveTextContent("It starts right away on this computer.");
    expect(screen.getByRole("button", { name: "+ Add repo" })).toBeInTheDocument();
    expect(visibleText(dialog)).not.toMatch(/runner/i);
    expect(visibleText(dialog)).not.toMatch(/nodevas runner/);
  });

  it("adds a repo from the dialog and picks it", async () => {
    serve([localRunner()]);
    dispatchDialog();
    await screen.findByRole("combobox", { name: "Repository" });
    await addBig(withRepos(localRunner(), [...localRunner().info!.repos!, big]));
    await waitFor(() =>
      expect((screen.getByRole("combobox", { name: "Repository" }) as HTMLSelectElement).value).toBe("repo9"),
    );
    expect(screen.getByText("big added. You can pick it when you start an agent.")).toBeInTheDocument();
  });

  it("offers to add the first repo in the empty box, with no command to type", async () => {
    serve([withRepos(localRunner(), [])]);
    const { container } = dispatchDialog();
    expect(await screen.findByText("No repo yet: add the folder of a project.")).toBeInTheDocument();
    expect(visibleText(container)).not.toMatch(/nodevas runner/);
    await addBig(withRepos(localRunner(), [big]));
    await waitFor(() =>
      expect((screen.getByRole("combobox", { name: "Repository" }) as HTMLSelectElement).value).toBe("repo9"),
    );
  });

  it("says beside Start when the agent may run any command", async () => {
    serve([withLocal(localRunner(), { permission: { claude: "full", codex: "edit" } })]);
    dispatchDialog();
    expect(await screen.findByText("Claude can run any command on this computer.")).toBeInTheDocument();
    fireEvent.change(screen.getByRole("combobox", { name: "Agent" }), { target: { value: "codex" } });
    expect(screen.queryByText(/can run any command/)).not.toBeInTheDocument();
  });

  it("says it asks first when asking before every run is on", async () => {
    serve([withLocal(localRunner(), { askEveryRun: true })]);
    dispatchDialog();
    expect(await screen.findByTestId("dispatch-auto-accept")).toHaveTextContent("You'll be asked before it starts");
  });

  it("says the part that runs agents is not running when it is not", async () => {
    serve([{ ...localRunner(), online: false }]);
    dispatchDialog();
    expect(
      await screen.findByText("The part of Nodevas that runs agents isn't running. Restart Nodevas."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Manage runners" })).not.toBeInTheDocument();
  });

  // The hub lists its own runner from the moment it starts, a few seconds
  // before that runner connects. Until then the dialog showed a team hub's
  // "None of your runners can take a run" and "Manage runners".
  it.each([
    ["on a first start", () => startingRunner()],
    ["after a restart", () => restartedRunner()],
  ])("waits calmly while this computer gets ready, %s", async (_when, runner) => {
    serve([runner()]);
    dispatchDialog();
    const dialog = await screen.findByRole("dialog", { name: "Dispatch to agent" });
    expect(await screen.findByText(STARTING)).toBeInTheDocument();
    expect(dialog).toHaveTextContent("on this computer");
    expect(screen.queryByText(UNAVAILABLE)).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Runner" })).not.toBeInTheDocument();
    // Nothing is wrong yet, so there is nothing to press.
    expect(within(screen.getByTestId("local-not-ready")).queryByRole("button")).not.toBeInTheDocument();
    expect(visibleText(dialog)).not.toMatch(/runner/i);

    // It connects: the list changes and the form is there, no click needed.
    act(() => useRunners.setState({ runners: [localRunner()] }));
    expect(await screen.findByRole("combobox", { name: "Agent" })).toBeInTheDocument();
    expect(screen.queryByText(STARTING)).not.toBeInTheDocument();
  });

  it("offers what to try once it has stopped: check again, or open This computer", async () => {
    const list = vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [{ ...localRunner(), online: false }] });
    const onClose = vi.fn();
    dispatchDialog(onClose);
    const dialog = await screen.findByRole("dialog", { name: "Dispatch to agent" });
    const block = await screen.findByTestId("local-not-ready");
    expect(within(block).getByText(UNAVAILABLE)).toBeInTheDocument();
    expect(
      within(block).getByText(
        "It may only need a moment: click Check again. If it still isn't running, close Nodevas completely and open it again.",
      ),
    ).toBeInTheDocument();
    expect(visibleText(dialog)).not.toMatch(/runner/i);

    // Check again asks the hub afresh; this time it is back.
    list.mockResolvedValue({ runners: [localRunner()] });
    fireEvent.click(within(block).getByRole("button", { name: "Check again" }));
    expect(await screen.findByRole("combobox", { name: "Agent" })).toBeInTheDocument();
    expect(list).toHaveBeenCalledTimes(2);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("opens This computer from the stopped state", async () => {
    serve([{ ...localRunner(), online: false }]);
    const onClose = vi.fn();
    dispatchDialog(onClose);
    fireEvent.click(await screen.findByRole("button", { name: "Open This computer" }));
    expect(onClose).toHaveBeenCalled();
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
    expect(useApp.getState().preferences.projectMode).toBe("settings");
  });

  it("never falls back to a team hub's words when nothing here can take a run", async () => {
    // No runner at all (serve --no-runner), or only one connected by hand
    // that is offline.
    for (const runners of [[], [{ ...laptop, online: false }]]) {
      serve(runners);
      const { unmount } = dispatchDialog();
      const dialog = await screen.findByRole("dialog", { name: "Dispatch to agent" });
      expect(await screen.findByText(UNAVAILABLE)).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Manage runners" })).not.toBeInTheDocument();
      expect(visibleText(dialog)).not.toMatch(/runner/i);
      unmount();
      useRunners.setState({ loaded: false, runners: [] });
    }
  });

  it("still dispatches the classic way to a runner connected by hand that is online", async () => {
    serve([startingRunner(), laptop]);
    dispatchDialog();
    const runner = (await screen.findByRole("combobox", { name: "Runner" })) as HTMLSelectElement;
    expect(runner.value).toBe("r9");
    expect(screen.queryByTestId("local-not-ready")).not.toBeInTheDocument();
    expect(await screen.findByRole("combobox", { name: "Agent" })).toBeInTheDocument();
  });

  // The top-bar pill says "agents can't start" in this state; the dialog
  // must agree and not offer the runner connected by hand.
  it("sticks to this computer once its runner was welcomed, even with a runner connected by hand online", async () => {
    serve([{ ...localRunner(), online: false }, laptop]);
    dispatchDialog();
    expect(await screen.findByTestId("local-not-ready")).toHaveTextContent(UNAVAILABLE);
    expect(screen.queryByRole("combobox", { name: "Runner" })).not.toBeInTheDocument();
  });

  it("links to This computer when no agent is installed", async () => {
    const base = localRunner();
    serve([{ ...base, info: { ...base.info!, agents: [] } }]);
    const onClose = vi.fn();
    dispatchDialog(onClose);
    const hint = await screen.findByText(/No agent is installed on this computer/);
    fireEvent.click(within(hint).getByRole("button", { name: "Open This computer" }));
    expect(onClose).toHaveBeenCalled();
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
  });
});

describe("GlobalAgentForm, single-user edition", () => {
  it("has no runner to pick and never says runner", async () => {
    serve([localRunner()]);
    const { container } = globalForm();
    await screen.findByRole("combobox", { name: "Agent" });
    expect(screen.queryByRole("combobox", { name: "Runner" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "+ Add repo" })).toBeInTheDocument();
    expect(visibleText(container)).not.toMatch(/runner/i);
  });

  it("keeps the repo command under Advanced and adds the first repo in place", async () => {
    serve([withRepos(localRunner(), [])]);
    const { container } = globalForm();
    expect(await screen.findByText("No repo yet: add the folder of a project.")).toBeInTheDocument();
    const advanced = screen.getByText("Advanced: add from the command line").closest("details");
    expect(advanced).not.toHaveAttribute("open");
    expect(advanced).toHaveTextContent("nodevas runner repo add <path>");
    expect(visibleText(container)).not.toMatch(/runner/i);

    await addBig(withRepos(localRunner(), [big]));
    await waitFor(() =>
      expect((screen.getByRole("combobox", { name: "Repository" }) as HTMLSelectElement).value).toBe("repo9"),
    );
  });

  it("starts on this computer's runner", async () => {
    serve([localRunner()]);
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue({
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
      status: "offered",
      exitCode: 0,
      global: true,
      createdBy: "local",
      createdByName: "",
      createdAt: "2026-11-14T08:00:00Z",
    });
    globalForm();
    fireEvent.change(await screen.findByRole("textbox", { name: "Task" }), { target: { value: "Fix it" } });
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toMatchObject({ runnerId: "local1", repoId: "repo1", agent: "claude" });
  });

  // Before its runner connects, the form said "None of your runners can
  // start a global agent now." over a disabled Runner box.
  it("waits calmly while this computer gets ready, then shows the form", async () => {
    serve([startingRunner()]);
    const { container } = globalForm();
    expect(await screen.findByText(STARTING)).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Runner" })).not.toBeInTheDocument();
    expect(within(screen.getByTestId("local-not-ready")).queryByRole("button")).not.toBeInTheDocument();
    expect(visibleText(container)).not.toMatch(/runner/i);

    act(() => useRunners.setState({ runners: [localRunner()] }));
    expect(await screen.findByRole("textbox", { name: "Task" })).toBeInTheDocument();
  });

  it("offers what to try once it has stopped", async () => {
    const list = vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [{ ...localRunner(), online: false }] });
    const { container } = globalForm();
    const block = await screen.findByTestId("local-not-ready");
    expect(within(block).getByText(UNAVAILABLE)).toBeInTheDocument();
    expect(visibleText(container)).not.toMatch(/runner/i);

    fireEvent.click(within(block).getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    fireEvent.click(within(await screen.findByTestId("local-not-ready")).getByRole("button", { name: "Open This computer" }));
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
    expect(useApp.getState().preferences.projectMode).toBe("settings");
  });
});
