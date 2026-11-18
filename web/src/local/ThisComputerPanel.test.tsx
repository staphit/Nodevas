import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, GlobalRunError, type Runner } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { LOCAL_CONFIG, localRunner, restartedRunner, startingRunner, visibleText } from "./localFixtures";
import { localCommands, localSummary, permissionOf, ThisComputerPanel } from "./ThisComputerPanel";
import { ThisComputerCard } from "./ThisComputerCard";
import { translate } from "../i18n";

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);

const UNAVAILABLE = "The part of Nodevas that runs agents isn't running. Restart Nodevas.";

function hold(runners: Runner[]) {
  useRunners.setState({ loaded: true, loading: false, runners, error: null, creating: false, revoking: {} });
}

function renderPanel(edition: "single" | "team" = "single") {
  return render(
    <EditionProvider edition={edition}>
      <ThisComputerPanel />
      <ConfirmDialogHost />
    </EditionProvider>,
  );
}

/** The settings call answers with the runner as its new hello says. */
function answerWith(change: (runner: Runner) => Runner, persisted = true) {
  return vi.spyOn(api, "setRunnerSettings").mockImplementation(async () => {
    const runner = change(localRunner());
    return { runner, persisted };
  });
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  hold([localRunner()]);
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ThisComputerPanel", () => {
  it("says what this computer has, in plain words, with commands only under Advanced", () => {
    const { container } = renderPanel();
    expect(screen.getByRole("heading", { name: "Agents found" })).toBeInTheDocument();
    expect(screen.getByText("Claude")).toBeInTheDocument();
    expect(screen.getByText("Gemini", { selector: "strong" }).closest("li")).toHaveTextContent("Not on this computer yet");
    expect(screen.getByRole("button", { name: "How to get Gemini" })).toBeInTheDocument();
    expect(screen.getByText("my-app")).toBeInTheDocument();
    expect(screen.getByText("D:\\code\\my-app")).toBeInTheDocument();
    expect(screen.getByLabelText("2 at a time")).toHaveTextContent("2");
    expect(screen.getByRole("checkbox", { name: "Ask me before every run starts" })).not.toBeChecked();

    // Advanced is closed: the settings file and command lines wait in it.
    const advanced = screen.getByText("Advanced").closest("details");
    expect(advanced).not.toHaveAttribute("open");
    expect(advanced).toHaveTextContent(LOCAL_CONFIG);
    expect(advanced).toHaveTextContent("nodevas runner set codexSandbox workspace-write");
    expect(visibleText(container)).not.toMatch(/runner/i);
  });

  it("saves a lower level right away and says new runs use it", async () => {
    const save = answerWith((runner) => ({
      ...runner,
      info: { ...runner.info!, local: { ...runner.info!.local!, permission: { claude: "edit", codex: "read" } } },
    }));
    renderPanel();
    const codex = screen.getByRole("group", { name: "What Codex may do without asking" });
    fireEvent.click(within(codex).getByRole("radio", { name: "Only read files" }));

    await waitFor(() => expect(save).toHaveBeenCalledWith("local1", { permission: { codex: "read" } }));
    expect(
      await screen.findByText("Saved. New runs use it; runs already going keep their settings."),
    ).toBeInTheDocument();
    expect(within(codex).getByRole("radio", { name: "Only read files" })).toBeChecked();
  });

  it("warns before an agent may run any command, and keeps asking for automatic runs by default", async () => {
    const save = answerWith((runner) => ({
      ...runner,
      info: { ...runner.info!, local: { ...runner.info!.local!, permission: { claude: "full", codex: "edit" } } },
    }));
    renderPanel();
    const claude = screen.getByRole("group", { name: "What Claude may do without asking" });

    fireEvent.click(within(claude).getByRole("radio", { name: "Run any command on this computer" }));
    let dialog = screen.getByRole("alertdialog", { name: "Let Claude run any command?" });
    expect(dialog).toHaveTextContent("delete files, install programs");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(save).not.toHaveBeenCalled();

    fireEvent.click(within(claude).getByRole("radio", { name: "Run any command on this computer" }));
    dialog = screen.getByRole("alertdialog", { name: "Let Claude run any command?" });
    const askFirst = within(dialog).getByRole("checkbox", {
      name: "Still ask me before runs that start automatically (recommended)",
    });
    expect(askFirst).toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: "I understand, allow it" }));
    await waitFor(() =>
      expect(save).toHaveBeenCalledWith("local1", {
        permission: { claude: "full" },
        shellWithoutAsking: { claude: false },
      }),
    );
    // Full: the guard stays switchable beside it.
    expect(
      await within(claude).findByRole("checkbox", {
        name: "Still ask me before runs that start automatically (recommended)",
      }),
    ).toBeChecked();
  });

  it("unticking the guard in the warning lets automatic runs start without asking", async () => {
    const save = answerWith((runner) => runner);
    renderPanel();
    const codex = screen.getByRole("group", { name: "What Codex may do without asking" });
    fireEvent.click(within(codex).getByRole("radio", { name: "Run any command on this computer" }));
    const dialog = screen.getByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "I understand, allow it" }));
    await waitFor(() =>
      expect(save).toHaveBeenCalledWith("local1", {
        permission: { codex: "full" },
        shellWithoutAsking: { codex: true },
      }),
    );
  });

  it("shows a mode set outside the app as Custom", () => {
    const base = localRunner();
    hold([
      {
        ...base,
        info: {
          ...base.info!,
          local: { ...base.info!.local!, permission: { claude: "custom" }, permissionDetail: { claude: "auto" } },
        },
      },
    ]);
    renderPanel();
    const claude = screen.getByRole("group", { name: "What Claude may do without asking" });
    expect(within(claude).getByRole("radio", { name: "Custom (auto)" })).toBeChecked();
    expect(permissionOf(useRunners.getState().runners[0], "claude")).toBe("custom");
    expect(permissionOf(useRunners.getState().runners[0], "opencode")).toBe("edit");
  });

  it("turns on asking before every run, changes runs at once and looks for agents again", async () => {
    const save = answerWith((runner) => runner);
    renderPanel();
    fireEvent.click(screen.getByRole("checkbox", { name: "Ask me before every run starts" }));
    await waitFor(() => expect(save).toHaveBeenLastCalledWith("local1", { askEveryRun: true }));

    fireEvent.click(screen.getByRole("button", { name: "More at the same time" }));
    await waitFor(() => expect(save).toHaveBeenLastCalledWith("local1", { maxConcurrent: 3 }));

    fireEvent.click(screen.getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(save).toHaveBeenLastCalledWith("local1", { redetect: true }));
    expect(await screen.findByText("Checked again.")).toBeInTheDocument();
  });

  it("stops at the ceiling", () => {
    const base = localRunner();
    hold([{ ...base, info: { ...base.info!, maxConcurrent: 4 } }]);
    renderPanel();
    expect(screen.getByRole("button", { name: "More at the same time" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Fewer at the same time" })).toBeEnabled();
  });

  it("says when a change only lasts until a restart, and when it was refused", async () => {
    answerWith((runner) => runner, false);
    renderPanel();
    fireEvent.click(screen.getByRole("checkbox", { name: "Ask me before every run starts" }));
    expect(
      await screen.findByText("Applied until Nodevas restarts: the settings file couldn't be saved."),
    ).toBeInTheDocument();

    vi.spyOn(api, "setRunnerSettings").mockRejectedValue(
      new GlobalRunError(503, "RUNNER_OFFLINE", "offline", "", 0, "offline"),
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "Ask me before every run starts" }));
    expect(
      await screen.findByText("The part of Nodevas that runs agents isn't running. Restart Nodevas."),
    ).toBeInTheDocument();
  });

  it("sets a repo's test command and removes a repo after asking", async () => {
    const setTest = vi
      .spyOn(api, "setRepoTestCommand")
      .mockResolvedValue({ runner: localRunner() });
    const base = localRunner();
    const remove = vi
      .spyOn(api, "removeRepo")
      .mockResolvedValue({ runner: { ...base, info: { ...base.info!, repos: [] } } });
    renderPanel();

    const field = screen.getByRole("textbox", { name: "Test command" });
    expect(field).toHaveAttribute("placeholder", "A test command is set. Type a new one to replace it.");
    fireEvent.change(field, { target: { value: "npm test" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(setTest).toHaveBeenCalledWith("local1", "repo1", "npm test"));
    expect(await screen.findByText("Test command saved for my-app.")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove my-app?" });
    expect(confirm).toHaveTextContent("Agents can no longer work in my-app. Your files are not touched.");
    await act(async () => {
      fireEvent.click(within(confirm).getByRole("button", { name: "Remove" }));
    });
    await waitFor(() => expect(remove).toHaveBeenCalledWith("local1", "repo1"));
    expect(await screen.findByText("No repo yet. Add the folder of a project you want agents to work on.")).toBeInTheDocument();
  });

  it("says the part that runs agents is not running when there is no built-in runner", () => {
    hold([{ ...localRunner(), builtIn: false }]);
    renderPanel();
    expect(
      screen.getByText("The part of Nodevas that runs agents isn't running. Restart Nodevas."),
    ).toBeInTheDocument();
  });

  // Every start, for a few seconds: the hub lists its own runner before
  // that runner has connected. Not an error, and nothing to press.
  it.each([
    ["on a first start", () => startingRunner()],
    ["after a restart", () => restartedRunner()],
  ])("waits calmly while this computer gets ready, %s", (_when, runner) => {
    hold([runner()]);
    const { container } = renderPanel();
    expect(
      screen.getByText("Getting this computer ready to run agents… This usually takes a few seconds."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("If it is still not ready after a minute, close Nodevas completely and open it again."),
    ).toBeInTheDocument();
    expect(screen.queryByText(UNAVAILABLE)).not.toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(visibleText(container)).not.toMatch(/runner/i);

    // It connects: the panel is there without a click.
    act(() => hold([localRunner()]));
    expect(screen.getByRole("heading", { name: "Agents found" })).toBeInTheDocument();
  });

  it("offers to check again once it has stopped, above what it last knew", async () => {
    hold([{ ...localRunner(), online: false }]);
    const list = vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
    renderPanel();
    const block = screen.getByTestId("local-not-ready");
    expect(within(block).getByText(UNAVAILABLE)).toBeInTheDocument();
    // Already in This computer: no button that leads here.
    expect(within(block).queryByRole("button", { name: "Open This computer" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "+ Add repo" })).toBeDisabled();

    fireEvent.click(within(block).getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(screen.queryByTestId("local-not-ready")).not.toBeInTheDocument());
    expect(list).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "+ Add repo" })).toBeEnabled();
  });

  it("offers to check again when the hub names no runner of its own", async () => {
    hold([]);
    const list = vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
    renderPanel();
    expect(screen.getByText(UNAVAILABLE)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(UNAVAILABLE)).toBeInTheDocument();
  });

  it("asks each time an agent wants to run a command, when switched on", async () => {
    const base = localRunner();
    const save = answerWith((runner) => ({ ...runner, info: { ...runner.info!, permissionRelay: true } }));
    renderPanel();
    const relay = screen.getByRole("checkbox", { name: "Ask me each time an agent wants to run a command" });
    expect(relay).not.toBeChecked();
    expect(screen.getByText(/no answer means no/)).toBeInTheDocument();
    fireEvent.click(relay);
    await waitFor(() => expect(save).toHaveBeenCalledWith("local1", { permissionRelay: true }));
    await waitFor(() => expect(relay).toBeChecked());

    act(() => hold([{ ...base, info: { ...base.info!, permissionRelay: false } }]));
    expect(relay).not.toBeChecked();
  });

  it("hides the online review toggles in Simple, and keeps them otherwise", () => {
    renderPanel();
    expect(screen.getByRole("checkbox", { name: t("local.autoPR") })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: t("local.prDraft") })).toBeInTheDocument();
    cleanup();

    useApp.setState({ preferences: { ...useApp.getState().preferences, experience: "simple" } });
    renderPanel();
    expect(screen.queryByRole("checkbox", { name: t("local.autoPR") })).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: t("local.prDraft") })).not.toBeInTheDocument();
    // What stays in Simple: the agents and the relay switch.
    expect(screen.getByRole("heading", { name: "Agents found" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Ask me each time an agent wants to run a command" })).toBeInTheDocument();
  });

  // Simple: folders, not repos, and no test command to type.
  it("says folder and asks for no test command in Simple", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, experience: "simple" } });
    const { container } = renderPanel();
    expect(screen.getByRole("heading", { name: "Folders" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "+ Add a folder" })).toBeInTheDocument();
    expect(screen.getByText("my-app")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Test command")).not.toBeInTheDocument();
    expect(visibleText(container)).not.toMatch(/\brepos?\b|test command/i);
  });

  // Simple: the help under "ask me before commands" names no protocol, and
  // the folded command lines say first, in plain words, who they are for.
  it.each(["en", "zh-TW"] as const)("explains asking before commands without ACP in Simple (%s)", (language) => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, language, experience: "simple" } });
    const { container } = renderPanel();
    const relay = screen.getByRole("checkbox", { name: translate("local.permissionRelay", language) });
    const help = relay.closest("section")?.querySelector(".run-hint")?.textContent ?? "";
    expect(help).toMatch(/OpenCode/);
    expect(help).not.toMatch(/\bACP\b/);
    expect(container.textContent).not.toMatch(/\bACP\b/);
  });

  it.each(["en", "zh-TW"] as const)("says who the command lines are for in Simple (%s)", (language) => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, language, experience: "simple" } });
    const { container } = renderPanel();
    const advanced = container.querySelector("details.this-computer-advanced") as HTMLElement;
    const summary = advanced.querySelector("summary")?.textContent ?? "";
    expect(summary).toBe(translate("simple.local.advanced", language));
    expect(summary).toMatch(language === "en" ? /type commands/ : /指令/);
    // The lines stay, behind the fold, after a plain note that nothing there is needed.
    expect(advanced).toHaveTextContent(translate("simple.local.cliHint", language));
    expect(advanced).toHaveTextContent("nodevas runner set codexSandbox workspace-write");
    expect(advanced).not.toHaveAttribute("open");
  });

  it("keeps ACP and Advanced in full mode", () => {
    const { container } = renderPanel();
    expect(container.textContent).toMatch(/OpenCode in ACP mode/);
    expect(screen.getByText("Advanced", { selector: "summary" })).toBeInTheDocument();
    expect(container.textContent).toContain("The same settings from a command line:");
  });

  it("says no folder yet in Simple", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, experience: "simple" } });
    const base = localRunner();
    hold([{ ...base, info: { ...base.info!, repos: [] } }]);
    const { container } = renderPanel();
    expect(screen.getByText("No folder yet. Add the folder you want agents to work in.")).toBeInTheDocument();
    expect(visibleText(container)).not.toMatch(/\brepos?\b/i);
  });

  it("keeps repos and the test command in full mode", () => {
    renderPanel();
    expect(screen.getByRole("heading", { name: "Repos" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "+ Add repo" })).toBeInTheDocument();
    expect(screen.getByLabelText("Test command")).toBeInTheDocument();
  });

  it("shows nothing of this computer on a team hub", () => {
    renderPanel("team");
    expect(screen.queryByText("Agents found")).not.toBeInTheDocument();
  });
});

describe("ThisComputerCard", () => {
  it("sums this computer up in one line and opens the panel in place", () => {
    render(
      <EditionProvider edition="single">
        <ThisComputerCard />
      </EditionProvider>,
    );
    expect(screen.getByText("Claude, Codex · 1 repo · 2 at a time")).toBeInTheDocument();
    const button = screen.getByRole("button", { name: "Settings" });
    expect(button).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(button);
    expect(screen.getByRole("heading", { name: "Agents found" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Hide" })).toHaveAttribute("aria-expanded", "true");
  });

  it("says folders, not repos, in Simple", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, experience: "simple" } });
    render(
      <EditionProvider edition="single">
        <ThisComputerCard />
      </EditionProvider>,
    );
    expect(screen.getByText("Claude, Codex · 1 folder · 2 at a time")).toBeInTheDocument();
  });

  it("says it is starting, not that it is not running, until its runner connects", () => {
    hold([startingRunner()]);
    render(
      <EditionProvider edition="single">
        <ThisComputerCard />
      </EditionProvider>,
    );
    expect(screen.getByText("Starting…")).toBeInTheDocument();
    expect(screen.queryByText(UNAVAILABLE)).not.toBeInTheDocument();
    act(() => hold([{ ...localRunner(), online: false }]));
    expect(screen.getByText(UNAVAILABLE)).toBeInTheDocument();
  });
});

describe("helpers", () => {
  it("summarises an offline computer as unavailable", () => {
    expect(localSummary(t, { ...localRunner(), online: false })).toBe(t("local.unavailable"));
  });

  it("lists the command lines for each setting under Advanced", () => {
    expect(localCommands(localRunner())).toEqual([
      `nodevas runner set maxConcurrent 2 --config "${LOCAL_CONFIG}"`,
      `nodevas runner set permissionMode acceptEdits --config "${LOCAL_CONFIG}"`,
      `nodevas runner set codexSandbox workspace-write --config "${LOCAL_CONFIG}"`,
      `nodevas runner set autoPR false --config "${LOCAL_CONFIG}"`,
      `nodevas runner set prDraft false --config "${LOCAL_CONFIG}"`,
    ]);
  });
});
