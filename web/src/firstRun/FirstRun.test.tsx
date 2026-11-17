import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type Runner } from "../api";
import { localRunner } from "../local/localFixtures";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useStation } from "../station/stationStore";
import { useApp, type AppState } from "../store";
import type { Graph } from "../types";
import { FIRST_RUN_WAIT_MS, FirstRun, OPEN_FIRST_RUN_EVENT } from "./FirstRun";
import { firstRunHolding } from "./gate";

// How the first load of the account's choices goes, per test.
const prefsLoad = vi.hoisted(() => ({ result: Promise.resolve(true) as Promise<boolean> }));
vi.mock("../preferences/sync", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../preferences/sync")>()),
  firstPreferenceLoad: () => prefsLoad.result,
}));

// The agent step has tests of its own; here only what it reports.
const scan = vi.hoisted(() => ({
  onReadyChange: undefined as ((ready: boolean, kinds: string[]) => void) | undefined,
}));
vi.mock("../local/AgentScanStep", () => ({
  AgentScanStep: ({ onReadyChange }: { onReadyChange?: (ready: boolean, kinds: string[]) => void }) => {
    scan.onReadyChange = onReadyChange;
    return <div data-testid="agent-scan" />;
  },
}));
vi.mock("../station/SimpleAgentBox", () => ({ SimpleAgentBox: () => null }));

const experience = vi.hoisted(() => ({ calls: [] as string[] }));
vi.mock("../simple/simpleMode", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../simple/simpleMode")>();
  return {
    ...actual,
    setExperience: (value: "full" | "simple") => {
      experience.calls.push(value);
      actual.setExperience(value);
    },
  };
});

function fakeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function graph(nodes: Graph["nodes"]): Graph {
  return { version: 1, nodes, edges: [], ui: {} };
}

/** A workspace the server just made: one project, "main", with nothing in it. */
function seedFreshWorkspace() {
  useApp.setState({
    graph: graph([]),
    projects: [{ name: "main", label: "main", depth: 0, path: "/ws/main", nodes: 0 }],
    activeProject: "main",
  });
}

function prefs(): AppState["preferences"] {
  return { ...useApp.getState().preferences };
}

function single(children: ReactNode) {
  return <EditionProvider edition="single">{children}</EditionProvider>;
}

function modalCount(): number {
  return document.querySelectorAll('[role="dialog"][aria-modal="true"]').length;
}

const QUESTIONS = { name: "A few quick questions" };
const WELCOME = { name: "Welcome to Nodevas" };

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: null,
    projects: [],
  });
  useRunners.setState({ runners: [], loaded: false });
  useStation.getState().setTab("project");
  prefsLoad.result = Promise.resolve(true);
  scan.onReadyChange = undefined;
  experience.calls = [];
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("FirstRun deciding", () => {
  it("asks a new person, and the start screen waits", async () => {
    seedFreshWorkspace();
    render(single(<FirstRun />));

    expect(await screen.findByRole("dialog", QUESTIONS)).toBeInTheDocument();
    expect(screen.getByText("What would you like to do here?")).toBeInTheDocument();
    expect(screen.queryByRole("dialog", WELCOME)).not.toBeInTheDocument();
    expect(firstRunHolding()).toBe(true);
  });

  it("shows nothing and writes nothing until the board, the projects and the choices are in", async () => {
    let loaded!: (ok: boolean) => void;
    prefsLoad.result = new Promise((resolve) => {
      loaded = resolve;
    });
    const before = prefs();
    const { container } = render(single(<FirstRun />));
    expect(container).toBeEmptyDOMElement();

    await act(async () => loaded(true));
    expect(container).toBeEmptyDOMElement();
    expect(prefs()).toEqual(before);

    act(() => seedFreshWorkspace());
    expect(await screen.findByRole("dialog", QUESTIONS)).toBeInTheDocument();
    expect(prefs().firstRun).toBe("pending");
  });

  it("asks nothing and writes nothing when the choices could not be loaded", async () => {
    seedFreshWorkspace();
    prefsLoad.result = Promise.resolve(false);
    render(single(<FirstRun />));

    expect(await screen.findByRole("dialog", WELCOME)).toBeInTheDocument();
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();
    expect(prefs().firstRun).toBe("pending");
    expect(firstRunHolding()).toBe(false);
  });

  it("gives up after the wait, leaving the questions for next time", async () => {
    vi.useFakeTimers();
    seedFreshWorkspace();
    prefsLoad.result = new Promise(() => {});
    const { container } = render(single(<FirstRun />));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(FIRST_RUN_WAIT_MS - 1);
    });
    expect(container).toBeEmptyDOMElement();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(screen.getByRole("dialog", WELCOME)).toBeInTheDocument();
    expect(prefs().firstRun).toBe("pending");
  });

  it.each<[string, () => void]>([
    ["work in the workspace", () => useApp.setState({ graph: graph([{ id: "a", title: "A" }]) })],
    ["the start screen answered", () => useApp.getState().updateUIPreference("startScreenDismissed", true)],
    ["the tour answered", () => useApp.getState().updateUIPreference("tourPrompt", "declined")],
    ["a tour chapter done", () => useApp.getState().updateUIPreference("tourChaptersDone", ["shell"])],
    ["the workstation on", () => useApp.getState().updateUIPreference("agentWorkstation", true)],
    ["the connect tip closed", () => useApp.getState().updateUIPreference("graphConnectTipDismissed", true)],
    ["Simple already chosen", () => useApp.getState().updateUIPreference("experience", "simple")],
    [
      "a runner in use",
      () => useRunners.setState({ runners: [{ id: "r1", name: "laptop", online: true } as Runner], loaded: true }),
    ],
  ])("marks someone with %s as existing, and changes nothing else", async (_, signal) => {
    seedFreshWorkspace();
    signal();
    const before = prefs();
    render(single(<FirstRun />));

    await vi.waitFor(() => expect(prefs().firstRun).toBe("existing"));
    expect({ ...prefs(), firstRun: before.firstRun }).toEqual(before);
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();
  });

  // serve --repo: the built-in runner already has a folder, though nothing
  // loaded the runner list yet.
  it("waits for the runner list, and marks someone whose built-in runner has a folder as existing", async () => {
    seedFreshWorkspace();
    let answer!: (value: { runners: Runner[] }) => void;
    vi.mocked(api.listRunners).mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );
    const before = prefs();
    const { container } = render(single(<FirstRun />));
    await act(async () => {});
    expect(container).toBeEmptyDOMElement();
    expect(prefs()).toEqual(before);

    await act(async () => answer({ runners: [localRunner()] }));
    await vi.waitFor(() => expect(prefs().firstRun).toBe("existing"));
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();
  });

  it("still asks when the runner list cannot be read", async () => {
    seedFreshWorkspace();
    vi.mocked(api.listRunners).mockRejectedValue(new Error("offline"));
    render(single(<FirstRun />));
    expect(await screen.findByRole("dialog", QUESTIONS)).toBeInTheDocument();
  });

  // The built-in runner's hello can land while the questions are up: Plan
  // could not keep agents that are in use hidden, so it is not offered.
  it("offers no Plan once agents turn out to be in use while asking", async () => {
    seedFreshWorkspace();
    render(single(<FirstRun />));
    await screen.findByRole("dialog", QUESTIONS);
    expect(screen.getByRole("button", { name: /Plan my own work/ })).toBeEnabled();
    act(() => useRunners.setState({ runners: [localRunner()], loaded: true }));
    expect(screen.getByRole("button", { name: /Plan my own work/ })).toBeDisabled();
  });

  it("never asks on a team hub and writes nothing", async () => {
    seedFreshWorkspace();
    const before = prefs();
    render(<FirstRun />);

    expect(await screen.findByRole("dialog", WELCOME)).toBeInTheDocument();
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();
    expect(prefs()).toEqual(before);
    expect(firstRunHolding()).toBe(false);
  });
});

describe("FirstRun answers", () => {
  async function ask() {
    seedFreshWorkspace();
    const user = userEvent.setup();
    const view = render(single(<FirstRun />));
    await screen.findByRole("dialog", QUESTIONS);
    return { user, view };
  }

  it("Plan keeps agents out of sight and goes on to the start screen", async () => {
    const { user } = await ask();
    await user.click(screen.getByRole("button", { name: /Plan my own work/ }));

    expect(prefs().agentWorkstation).toBe(false);
    expect(prefs().firstRun).toBe("done");
    expect(experience.calls).toEqual([]);
    expect(screen.getByRole("dialog", WELCOME)).toBeInTheDocument();
    expect(modalCount()).toBe(1);
  });

  it("Make, then Keep it simple, sets things up and lands on the global agents", async () => {
    const { user } = await ask();
    const before = prefs();
    await user.click(screen.getByRole("button", { name: /Have AI build things for me/ }));
    // Nothing is written until the last answer.
    expect(prefs()).toEqual(before);
    expect(screen.getByText("How do you want to work with the AI?")).toBeInTheDocument();
    expect(modalCount()).toBe(1);

    await user.click(screen.getByRole("button", { name: /Keep it simple/ }));
    expect(prefs().agentWorkstation).toBe(true);
    expect(prefs().experience).toBe("simple");
    expect(prefs().firstRun).toBe("done");
    expect(screen.getByTestId("agent-scan")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Agents on this computer" })).toBeInTheDocument();
    expect(modalCount()).toBe(1);

    act(() => scan.onReadyChange?.(true, ["claude"]));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(prefs().startScreenDismissed).toBe(true);
    expect(prefs().projectMode).toBe("station");
    expect(useStation.getState().tab).toBe("global");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(firstRunHolding()).toBe(false);
  });

  it("Both with everything stays on the board, where the start screen follows", async () => {
    const { user } = await ask();
    await user.click(screen.getByRole("button", { name: /Both/ }));
    await user.click(screen.getByRole("button", { name: /Show me everything/ }));
    expect(prefs().experience).toBe("full");
    expect(experience.calls).toEqual(["full"]);
    expect(prefs().agentWorkstation).toBe(true);

    await user.click(screen.getByRole("button", { name: "Do this later" }));
    expect(prefs().projectMode).toBe("info");
    expect(prefs().startScreenDismissed).toBe(false);
    expect(screen.getByRole("dialog", WELCOME)).toBeInTheDocument();
    expect(modalCount()).toBe(1);
  });

  it("offers Do this later until an agent is ready, then Continue", async () => {
    const { user } = await ask();
    await user.click(screen.getByRole("button", { name: /Both/ }));
    await user.click(screen.getByRole("button", { name: /Keep it simple/ }));
    expect(screen.getByRole("button", { name: "Do this later" })).toBeInTheDocument();

    act(() => scan.onReadyChange?.(true, ["codex"]));
    expect(screen.getByRole("button", { name: "Continue" })).toBeInTheDocument();
    act(() => scan.onReadyChange?.(false, []));
    expect(screen.getByRole("button", { name: "Do this later" })).toBeInTheDocument();
  });

  it("Skip on the first question writes only that it was skipped", async () => {
    const { user } = await ask();
    const before = prefs();
    await user.click(screen.getByRole("button", { name: "Skip the questions" }));

    expect(prefs()).toEqual({ ...before, firstRun: "skipped" });
    expect(screen.getByRole("dialog", WELCOME)).toBeInTheDocument();
  });

  it("Skip on the second question keeps the first answer and leaves the mode alone", async () => {
    const { user } = await ask();
    const before = prefs();
    await user.click(screen.getByRole("button", { name: /Have AI build things for me/ }));
    await user.click(screen.getByRole("button", { name: "Skip the questions" }));

    expect(prefs()).toEqual({ ...before, agentWorkstation: true, firstRun: "skipped" });
    expect(experience.calls).toEqual([]);
  });

  it("Escape means Skip", async () => {
    const { user } = await ask();
    await user.keyboard("{Escape}");
    expect(prefs().firstRun).toBe("skipped");
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();
  });

  it("writes nothing when it goes away on the second question", async () => {
    const { user, view } = await ask();
    const before = prefs();
    await user.click(screen.getByRole("button", { name: /Both/ }));
    view.unmount();
    expect(prefs()).toEqual(before);
    expect(firstRunHolding()).toBe(false);
  });

  it("Back goes from the second question to the first", async () => {
    const { user } = await ask();
    await user.click(screen.getByRole("button", { name: /Have AI build things for me/ }));
    await user.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("What would you like to do here?")).toBeInTheDocument();
    // The first answer is taken back with it.
    await user.click(screen.getByRole("button", { name: "Skip the questions" }));
    expect(prefs().agentWorkstation).toBe(false);
  });
});

describe("FirstRun asked again", () => {
  async function answered() {
    useApp.setState({
      graph: graph([{ id: "a", title: "A" }]),
      projects: [{ name: "main", label: "main", depth: 0, path: "/ws/main", nodes: 1 }],
    });
    useApp.getState().updateUIPreference("firstRun", "done");
    const user = userEvent.setup();
    return user;
  }

  it("asks without the agent step, keeps where you are, and says it saved", async () => {
    const user = await answered();
    useApp.getState().updateUIPreference("projectMode", "settings");
    render(single(<FirstRun />));
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();

    act(() => {
      window.dispatchEvent(new Event(OPEN_FIRST_RUN_EVENT));
    });
    expect(screen.getByRole("dialog", QUESTIONS)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Have AI build things for me/ }));
    await user.click(screen.getByRole("button", { name: /Keep it simple/ }));

    expect(screen.queryByTestId("agent-scan")).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();
    expect(prefs().experience).toBe("simple");
    expect(prefs().agentWorkstation).toBe(true);
    expect(prefs().projectMode).toBe("settings");
    expect(prefs().startScreenDismissed).toBe(false);
    expect(screen.getByRole("status")).toHaveTextContent("Saved. Nodevas now follows your new answers.");
  });

  it("cannot hide the agents while they are in use", async () => {
    await answered();
    useRunners.setState({ runners: [{ id: "r1", name: "laptop", online: true } as Runner], loaded: true });
    render(single(<FirstRun />));
    act(() => {
      window.dispatchEvent(new Event(OPEN_FIRST_RUN_EVENT));
    });
    const plan = screen.getByRole("button", { name: /Plan my own work/ });
    expect(plan).toBeDisabled();
    // Says why, in plain words, right under it.
    const why = "Not available now: AI is already working here, so it stays on screen.";
    expect(screen.getByText(why)).toBeVisible();
    expect(plan).toHaveAccessibleDescription(why);
  });

  it("gives no reason while Plan can be chosen", async () => {
    await answered();
    render(single(<FirstRun />));
    act(() => {
      window.dispatchEvent(new Event(OPEN_FIRST_RUN_EVENT));
    });
    expect(screen.getByRole("button", { name: /Plan my own work/ })).toBeEnabled();
    expect(screen.queryByText(/Not available now/)).not.toBeInTheDocument();
  });

  it("is not asked again on a team hub", async () => {
    await answered();
    render(<FirstRun />);
    act(() => {
      window.dispatchEvent(new Event(OPEN_FIRST_RUN_EVENT));
    });
    expect(screen.queryByRole("dialog", QUESTIONS)).not.toBeInTheDocument();
  });
});
