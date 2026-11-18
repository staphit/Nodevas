import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { AgentScanStep } from "./AgentScanStep";
import { localRunner, startingRunner, visibleText } from "./localFixtures";

function hold(runners: Runner[]) {
  useRunners.setState({ loaded: true, loading: false, runners, error: null, creating: false, revoking: {} });
}

/** The built-in runner with nothing installed. */
function bareRunner(): Runner {
  const base = localRunner();
  return {
    ...base,
    info: {
      ...base.info!,
      agents: ["claude", "codex", "gemini", "opencode", "antigravity"].map((kind) => ({ kind, installed: false, models: [] })),
    },
  };
}

function renderStep(props: Parameters<typeof AgentScanStep>[0] = {}, edition: "single" | "team" = "single") {
  return render(
    <EditionProvider edition={edition}>
      <AgentScanStep {...props} />
    </EditionProvider>,
  );
}

function row(name: string): HTMLElement {
  return screen.getByText(name, { selector: "strong" }).closest("li") as HTMLElement;
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  hold([localRunner()]);
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("AgentScanStep", () => {
  it("lists all five agents, with the version of each one found", () => {
    renderStep();
    for (const name of ["Claude", "Codex", "Gemini", "OpenCode", "Antigravity"]) expect(row(name)).toBeInTheDocument();
    expect(row("Claude")).toHaveTextContent("Ready to use");
    expect(row("Claude")).toHaveTextContent("2.1.0");
    expect(row("Codex")).toHaveTextContent("0.40.0");
    expect(row("Gemini")).toHaveTextContent("Not on this computer yet");
    // An agent the runner did not even report is missing too.
    expect(row("OpenCode")).toHaveTextContent("Not on this computer yet");
    expect(screen.getByRole("status")).toHaveTextContent("Ready: Claude, Codex. You can go on.");
  });

  it("offers how to get a missing agent, and to show where it is", () => {
    renderStep();
    const gemini = row("Gemini");
    expect(within(row("Claude")).queryByRole("button", { name: /How to get/ })).not.toBeInTheDocument();
    expect(within(gemini).getByRole("button", { name: "I already have it" })).toBeInTheDocument();
    const how = within(gemini).getByRole("button", { name: "How to get Gemini" });
    expect(how).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(how);
    expect(how).toHaveAttribute("aria-expanded", "true");
    expect(gemini).toHaveTextContent("Open the official Gemini page.");
    expect(gemini).toHaveTextContent("Gemini comes from Google and may need a paid plan.");
    const link = within(gemini).getByRole("link", { name: "Open the official page" });
    expect(link).toHaveAttribute("href", "https://github.com/google-gemini/gemini-cli");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link.getAttribute("rel")?.split(" ").sort()).toEqual(["noopener", "noreferrer"]);
  });

  it("opens the file picker for a missing agent", async () => {
    vi.spyOn(api, "listDirs").mockResolvedValue({ path: "", parent: "", home: "", dirs: [] });
    renderStep();
    fireEvent.click(within(row("Gemini")).getByRole("button", { name: "I already have it" }));
    expect(await screen.findByRole("dialog", { name: "Show Nodevas where Gemini is" })).toBeInTheDocument();
  });

  it("looks again on Check again", async () => {
    const save = vi.spyOn(api, "setRunnerSettings").mockResolvedValue({ runner: localRunner(), persisted: true });
    renderStep();
    fireEvent.click(screen.getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith("local1", { redetect: true }));
    // Gemini is still not there: a program installed a moment ago may need a restart.
    expect(await screen.findByText(/sometimes only seen after Nodevas is closed completely/)).toBeInTheDocument();
  });

  it("reports whether an agent is ready, on mount and on every change", () => {
    const onReadyChange = vi.fn();
    renderStep({ onReadyChange });
    expect(onReadyChange).toHaveBeenLastCalledWith(true, ["claude", "codex"]);
    expect(onReadyChange).toHaveBeenCalledTimes(1);

    act(() => hold([bareRunner()]));
    expect(onReadyChange).toHaveBeenLastCalledWith(false, []);
    expect(screen.getByRole("status")).toHaveTextContent("No agent is ready yet.");

    // The same answer again is not news.
    act(() => hold([bareRunner()]));
    expect(onReadyChange).toHaveBeenCalledTimes(2);
  });

  it("waits for the runner before it reports anything", () => {
    const onReadyChange = vi.fn();
    hold([startingRunner()]);
    renderStep({ onReadyChange });
    expect(screen.getByTestId("local-not-ready")).toBeInTheDocument();
    expect(onReadyChange).not.toHaveBeenCalled();
    act(() => hold([localRunner()]));
    expect(onReadyChange).toHaveBeenCalledWith(true, ["claude", "codex"]);
  });

  it("marks a program added by hand, and stops using it on request", async () => {
    const base = localRunner();
    hold([{ ...base, info: { ...base.info!, local: { ...base.info!.local!, agentPaths: { claude: "C:\\tools\\claude.exe" } } } }]);
    const save = vi.spyOn(api, "setRunnerSettings").mockResolvedValue({ runner: localRunner(), persisted: true });
    renderStep();
    const claude = row("Claude");
    expect(claude).toHaveTextContent("Added by you");
    expect(claude).toHaveTextContent("C:\\tools\\claude.exe");
    expect(row("Codex")).not.toHaveTextContent("Added by you");
    fireEvent.click(within(claude).getByRole("button", { name: "Stop using this one" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith("local1", { agentPath: { claude: "" } }));
  });

  it("says a program added by hand is gone", () => {
    const base = localRunner();
    hold([
      {
        ...base,
        info: {
          ...base.info!,
          agents: [{ kind: "claude", installed: false, models: [], error: "claude: the program added for it is no longer there" }],
        },
      },
    ]);
    renderStep();
    expect(row("Claude")).toHaveTextContent("Found, but it can't be used yet");
    expect(row("Claude")).toHaveTextContent("The Claude program you added isn't there any more.");
  });

  // Antigravity installed without the permission rule, OpenCode of a version
  // Nodevas does not know: a plain sentence, the technical words (a file
  // path, a terminal command) folded under details.
  it.each([
    [
      "antigravity",
      "Antigravity",
      'antigravity: runs need "mcp(nodevas/*)" in permissions.allow of ~/.gemini/antigravity-cli/settings.json; run `nodevas setup-agent --runs-only --agents antigravity`',
      "needs one more setting",
    ],
    ["opencode", "OpenCode", "opencode: version 3 is not supported yet; runs need OpenCode 1.x or 2.x", "Nodevas can't use this version"],
  ])("says what %s needs in plain words, the details folded", (kind, name, error, plain) => {
    const base = localRunner();
    hold([{ ...base, info: { ...base.info!, agents: [{ kind, installed: false, models: [], error }] } }]);
    const { container } = renderStep({ variant: "step" });
    expect(row(name)).toHaveTextContent(plain);
    const shown = visibleText(container);
    expect(shown).not.toMatch(/permissions\.allow|setup-agent|settings\.json|`|runner/i);
    expect(within(row(name)).getByText("Show details", { selector: "summary" })).toBeInTheDocument();
    // Found: nothing to get or show, which would contradict "Found".
    expect(within(row(name)).queryByRole("button", { name: `How to get ${name}` })).not.toBeInTheDocument();
    expect(within(row(name)).queryByRole("button", { name: "I already have it" })).not.toBeInTheDocument();
  });

  // Claude Code installed but signed out: not ready, and how to sign in, in
  // plain words; nothing to get, and not the runner's English.
  it("says an agent that is here but signed out needs signing in", () => {
    const base = localRunner();
    hold([
      {
        ...base,
        info: {
          ...base.info!,
          agents: [
            {
              kind: "claude",
              installed: false,
              signedOut: true,
              version: "2.1.327 (Claude Code)",
              models: [],
              error: "claude: not signed in on this computer; run `claude` in a terminal here and log in",
            },
          ],
        },
      },
    ]);
    const onReadyChange = vi.fn();
    const { container } = renderStep({ variant: "step", onReadyChange });
    expect(row("Claude")).toHaveTextContent("Found, but not signed in yet");
    expect(row("Claude")).toHaveTextContent(
      "Open the Terminal app on this computer, type claude, press Enter and sign in. Then press Check again.",
    );
    expect(visibleText(container)).not.toMatch(/log in|`|runner/i);
    expect(within(row("Claude")).queryByRole("button", { name: "How to get Claude" })).not.toBeInTheDocument();
    expect(within(row("Claude")).queryByRole("button", { name: "I already have it" })).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("No agent is ready yet.");
    expect(onReadyChange).toHaveBeenLastCalledWith(false, []);
  });

  it("never says runner", () => {
    const { container } = renderStep();
    fireEvent.click(within(row("Gemini")).getByRole("button", { name: "How to get Gemini" }));
    expect(visibleText(container)).not.toMatch(/runner/i);
    hold([bareRunner()]);
  });

  it("in settings, sits under its own heading", () => {
    renderStep({ variant: "settings" });
    expect(screen.getByRole("heading", { name: "Agents found" })).toBeInTheDocument();
  });

  it("renders nothing on a team hub", () => {
    const onReadyChange = vi.fn();
    const { container } = renderStep({ onReadyChange }, "team");
    expect(container).toBeEmptyDOMElement();
    expect(onReadyChange).not.toHaveBeenCalled();
  });
});
