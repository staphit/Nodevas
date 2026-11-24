import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError, type Actor, type Run, type Runner, type RunnerAgent } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "./budgetStore";
import { useCoordStore } from "./coord/coordStore";
import { DispatchDialog } from "./DispatchDialog";
import {
  defaultDispatchMode,
  effectiveDispatchMode,
  followUpMode,
  isInteractiveDisabled,
  resolveDispatchMode,
  runnerSupportsInteractive,
  type DefaultDispatchModeContext,
} from "./RunModePicker";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const alice: Actor = { id: "u1", name: "alice", role: "admin" };
const bob: Actor = { id: "u2", name: "bob", role: "member" };

const interactiveAgent: RunnerAgent = {
  kind: "claude",
  installed: true,
  interactive: true,
  models: [{ id: "opus", default: true }],
};

const headlessOnly: Runner = {
  id: "r1",
  name: "laptop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.3.0",
    hostname: "laptop",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless"],
    agents: [interactiveAgent],
    repos: [{ id: "repo1", name: "nodevas" }],
  },
};

const desktop: Runner = {
  ...headlessOnly,
  id: "r2",
  name: "desktop",
  info: { ...headlessOnly.info!, hostname: "desktop", capabilities: ["headless", "interactive"] },
};

const created: Run = {
  id: "run1",
  project: "p",
  nodeId: "alpha",
  runnerId: "r2",
  runnerName: "desktop",
  agent: "claude",
  model: "opus",
  repoId: "repo1",
  repoName: "nodevas",
  base: "",
  branch: "",
  prompt: "",
  status: "queued",
  mode: "interactive",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-10-24T08:00:00Z",
};

const NOTE = /Runs the agent's own terminal on the runner/;

function group(name: string) {
  return screen.getByRole("radiogroup", { name });
}

beforeEach(() => {
  viewer.current = alice;
  useApp.setState({ preferences: { ...readPreferences(), language: "en", runMode: "auto" } });
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useRuns.getState().reset();
  useBudget.getState().reset();
  useCoordStore.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
    used: { runs: 0, costUsd: 0, tokens: 0 },
    exceeded: "",
  });
  vi.spyOn(api, "getRunBusy").mockResolvedValue({ holders: [], others: 0, choices: [] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("mode helpers", () => {
  it("allow interactive only where the runner has the capability", () => {
    expect(runnerSupportsInteractive(desktop)).toBe(true);
    expect(runnerSupportsInteractive(headlessOnly)).toBe(false);
    expect(runnerSupportsInteractive(undefined)).toBe(false);
    expect(effectiveDispatchMode("interactive", desktop)).toBe("interactive");
    expect(effectiveDispatchMode("interactive", headlessOnly)).toBe("headless");
    expect(effectiveDispatchMode("headless", desktop)).toBe("headless");
  });
});

describe("defaultDispatchMode decision matrix", () => {
  const base: DefaultDispatchModeContext = {
    runner: desktop,
    agent: interactiveAgent,
    viewer: alice,
    edition: "team",
  };

  it("defaults to interactive when all conditions hold", () => {
    expect(defaultDispatchMode(base)).toEqual({ mode: "interactive" });
  });

  it("falls back to background when runner lacks CapInteractive", () => {
    expect(defaultDispatchMode({ ...base, runner: headlessOnly })).toEqual({
      mode: "headless",
      reason: "capability",
    });
    expect(defaultDispatchMode({ ...base, runner: undefined })).toEqual({
      mode: "headless",
      reason: "capability",
    });
  });

  it("falls back to background when agent is missing or interactive flag is false/absent", () => {
    expect(defaultDispatchMode({ ...base, agent: undefined })).toEqual({
      mode: "headless",
      reason: "agent",
    });
    expect(defaultDispatchMode({ ...base, agent: { ...interactiveAgent, interactive: false } })).toEqual({
      mode: "headless",
      reason: "agent",
    });
    expect(defaultDispatchMode({ ...base, agent: { ...interactiveAgent, interactive: undefined } })).toEqual({
      mode: "headless",
      reason: "agent",
    });
  });

  it("falls back to background when agent is untrusted", () => {
    expect(defaultDispatchMode({ ...base, agent: { ...interactiveAgent, untrusted: true } })).toEqual({
      mode: "headless",
      reason: "untrusted",
    });
  });

  it("falls back to background on a teammate's runner on team hub, but allows single-user", () => {
    expect(defaultDispatchMode({ ...base, viewer: bob, edition: "team" })).toEqual({
      mode: "headless",
      reason: "teammate",
    });
    expect(defaultDispatchMode({ ...base, viewer: bob, edition: "single" })).toEqual({
      mode: "interactive",
    });
  });

  it("handles auto-accept policy modes and allowRemoteTyping", () => {
    // Auto-accept off -> interactive.
    const aaOff: Runner = {
      ...desktop,
      info: { ...desktop.info!, autoAccept: { enabled: false, requireStepUp: false } },
    };
    expect(defaultDispatchMode({ ...base, runner: aaOff })).toEqual({ mode: "interactive" });

    // Auto-accept on, modes empty (headless only) -> autoAccept fallback.
    const aaHeadless: Runner = {
      ...desktop,
      info: {
        ...desktop.info!,
        autoAccept: { enabled: true, requireStepUp: false, modes: [""], allowRemoteTyping: true },
      },
    };
    expect(defaultDispatchMode({ ...base, runner: aaHeadless })).toEqual({
      mode: "headless",
      reason: "autoAccept",
    });

    // Auto-accept includes interactive, but allowRemoteTyping is false -> noTyping fallback.
    const aaNoTyping: Runner = {
      ...desktop,
      info: {
        ...desktop.info!,
        autoAccept: { enabled: true, requireStepUp: false, modes: ["", "interactive"], allowRemoteTyping: false },
      },
    };
    expect(defaultDispatchMode({ ...base, runner: aaNoTyping })).toEqual({
      mode: "headless",
      reason: "noTyping",
    });

    // Auto-accept includes interactive AND allowRemoteTyping -> interactive.
    const aaInteractiveTyping: Runner = {
      ...desktop,
      info: {
        ...desktop.info!,
        autoAccept: { enabled: true, requireStepUp: false, modes: ["", "interactive"], allowRemoteTyping: true },
      },
    };
    expect(defaultDispatchMode({ ...base, runner: aaInteractiveTyping })).toEqual({ mode: "interactive" });
  });

  it("falls back to background for staged, pipeline, fallbacks, group, and contest", () => {
    expect(defaultDispatchMode({ ...base, staged: true })).toEqual({
      mode: "headless",
      reason: "staged",
    });
    expect(defaultDispatchMode({ ...base, pipeline: true })).toEqual({
      mode: "headless",
      reason: "pipeline",
    });
    expect(defaultDispatchMode({ ...base, fallbacks: true })).toEqual({
      mode: "headless",
      reason: "pipeline",
    });
    expect(defaultDispatchMode({ ...base, group: true })).toEqual({
      mode: "headless",
      reason: "group",
    });
    expect(defaultDispatchMode({ ...base, contest: true })).toEqual({
      mode: "headless",
      reason: "contest",
    });
  });
});

// A saved "interactive" preference (or a card picked earlier) must never
// start interactive where §8.1 says it cannot or must not.
describe("hard reasons win over the saved preference and a pick", () => {
  const base: DefaultDispatchModeContext = {
    runner: desktop,
    agent: interactiveAgent,
    viewer: alice,
    edition: "team",
  };
  const noTypingRunner: Runner = {
    ...desktop,
    info: {
      ...desktop.info!,
      autoAccept: { enabled: true, requireStepUp: false, modes: ["", "interactive"], allowRemoteTyping: false },
    },
  };
  const hard: Array<[string, DefaultDispatchModeContext]> = [
    ["untrusted", { ...base, agent: { ...interactiveAgent, untrusted: true } }],
    ["teammate", { ...base, viewer: bob }],
    ["noTyping", { ...base, runner: noTypingRunner }],
    ["staged", { ...base, staged: true }],
    ["pipeline", { ...base, pipeline: true }],
    ["fallbacks", { ...base, fallbacks: true }],
    ["group", { ...base, group: true }],
    ["contest", { ...base, contest: true }],
    ["capability", { ...base, runner: headlessOnly }],
    ["agent", { ...base, agent: { ...interactiveAgent, interactive: false } }],
  ];

  it.each(hard)("%s disables the card and resolves to background whatever was saved or picked", (_name, ctx) => {
    expect(isInteractiveDisabled(ctx)).toBe(true);
    const def = defaultDispatchMode(ctx);
    expect(resolveDispatchMode(null, "interactive", def, ctx)).toBe("headless");
    expect(resolveDispatchMode("interactive", "interactive", def, ctx)).toBe("headless");
    expect(followUpMode(ctx, "interactive")).toBe("");
  });

  it("lets a saved or picked interactive stand where auto-accept only covers background runs", () => {
    const aaHeadless: Runner = {
      ...desktop,
      info: {
        ...desktop.info!,
        autoAccept: { enabled: true, requireStepUp: false, modes: [""], allowRemoteTyping: true },
      },
    };
    const ctx = { ...base, runner: aaHeadless };
    expect(isInteractiveDisabled(ctx)).toBe(false);
    expect(defaultDispatchMode(ctx).mode).toBe("headless");
    expect(resolveDispatchMode(null, "interactive", defaultDispatchMode(ctx), ctx)).toBe("interactive");
  });

  it("gives a follow-up the first start's default, the saved background, and no mode for a queued reply", () => {
    expect(followUpMode(base)).toBe("interactive");
    expect(followUpMode(base, "background")).toBe("");
    expect(followUpMode(base, "auto", true)).toBe("");
    expect(followUpMode(base, "interactive", true)).toBe("");
  });
});

describe("DispatchDialog mode", () => {
  it("gates Talk with it on the selected runner's capability and defaults interactive when supported", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [headlessOnly, desktop] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    const runners = await screen.findByRole("radiogroup", { name: "Which computer" });

    expect(within(runners).getByRole("radio", { name: "laptop" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("radio", { name: "Background" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("radio", { name: "Talk with it" })).toBeDisabled();
    expect(screen.getByText(/Interactive needs a runner version/)).toBeInTheDocument();

    fireEvent.click(within(runners).getByRole("radio", { name: "desktop" }));
    const interactive = screen.getByRole("radio", { name: "Talk with it" });
    expect(interactive).toBeEnabled();
    expect(interactive).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText(NOTE)).toBeInTheDocument();

    // Back to a runner without it: falls back to background.
    fireEvent.click(within(runners).getByRole("radio", { name: "laptop" }));
    expect(screen.getByRole("radio", { name: "Background" })).toHaveAttribute("aria-checked", "true");
  });

  it("sends mode interactive by default when supported, and remembers runMode preference when picked", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue(created);
    const onCreated = vi.fn();
    const { unmount } = render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={onCreated} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    expect(within(group("Mode")).getByRole("radio", { name: "Talk with it" })).toHaveAttribute("aria-checked", "true");
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(create).toHaveBeenCalledWith({
      nodeId: "alpha",
      runnerId: "r2",
      agent: "claude",
      model: "opus",
      repoId: "repo1",
      mode: "interactive",
    });

    // Picking Background stores runMode="background" so the next dialog opens in background.
    fireEvent.click(within(group("Mode")).getByRole("radio", { name: "Background" }));
    expect(useApp.getState().preferences.runMode).toBe("background");
    unmount();

    create.mockClear();
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });
    expect(within(group("Mode")).getByRole("radio", { name: "Background" })).toHaveAttribute("aria-checked", "true");
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).not.toHaveProperty("mode");
  });

  it("sends no mode for a background run", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue({ ...created, mode: undefined });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    fireEvent.click(screen.getByRole("radio", { name: "Background" }));
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).not.toHaveProperty("mode");
  });

  it("explains a 409 for an interactive dispatch", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    vi.spyOn(api, "createRun").mockRejectedValue(
      new ConflictError("", "", "this runner cannot run interactive sessions", "FAILED_PRECONDITION"),
    );
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    fireEvent.click(screen.getByRole("radio", { name: "Talk with it" }));
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("this runner cannot run interactive sessions");
  });

  it("explains a 409 without a reason for an interactive dispatch", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    vi.spyOn(api, "createRun").mockRejectedValue(new ConflictError("", ""));
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("radiogroup", { name: "Which computer" });

    fireEvent.click(screen.getByRole("radio", { name: "Talk with it" }));
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/cannot run agents interactively/);
  });
});
