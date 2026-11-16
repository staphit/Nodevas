import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, GlobalRunError, type Runner, type RunnerLoad } from "../api";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { useGlobalAgents } from "./globalAgentsStore";
import { capacityBlock, RunnerCapacity } from "./RunnerCapacity";

function makeLoad(overrides: Partial<RunnerLoad> = {}): RunnerLoad {
  return {
    runnerId: "r1",
    name: "desktop",
    online: true,
    maxConcurrent: 2,
    ceiling: 4,
    settable: "",
    globalRuns: "",
    busy: 1,
    queued: 3,
    globalActive: 1,
    today: { runs: 2, tokens: 12345, costUsd: 0.042 },
    ...overrides,
  };
}

function runnerWith(maxConcurrent: number): Runner {
  return {
    id: "r1",
    name: "desktop",
    ownerId: "u1",
    ownerName: "alice",
    createdAt: "",
    lastSeenAt: "",
    online: true,
    info: { version: "2", hostname: "desk", os: "linux", arch: "amd64", agents: [], repos: [], maxConcurrent },
  };
}

function seed(...loads: RunnerLoad[]) {
  useGlobalAgents.setState({ runners: loads, loaded: true });
}

function stepper(): HTMLElement {
  return screen.getByRole("group", { name: "At once" });
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useGlobalAgents.getState().reset();
  // Nobody listed the runners: a saved change asks for no runner list.
  useRunners.setState({ runners: [], loaded: false });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("RunnerCapacity", () => {
  it("draws nothing without a runner", () => {
    const { container } = render(<RunnerCapacity />);
    expect(container).toBeEmptyDOMElement();
  });

  it("says each runner's load and today's use", () => {
    seed(makeLoad(), makeLoad({ runnerId: "r2", name: "laptop", online: false, busy: 0, queued: 0, today: { runs: 0, tokens: 0, costUsd: 0 }, settable: "offline" }));
    render(<RunnerCapacity />);
    expect(screen.getByRole("heading", { name: "Runners" })).toBeInTheDocument();
    expect(screen.getByText("1/2 at work · 3 queued · today $0.042 · 12.3K tokens")).toBeInTheDocument();
    expect(screen.getByText("0/2 at work · 0 queued · today $0 · 0 tokens")).toBeInTheDocument();
    expect(screen.getAllByRole("group", { name: "At once" })).toHaveLength(2);
    expect(screen.getByLabelText("2 at once on desktop")).toHaveTextContent("2");
  });

  it("keeps the stepper within 1 and the runner's ceiling", () => {
    seed(makeLoad({ maxConcurrent: 1 }));
    const { unmount } = render(<RunnerCapacity />);
    expect(screen.getByRole("button", { name: "Fewer at once on desktop" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "More at once on desktop" })).toBeEnabled();
    expect(screen.getByText("At most 4 from here (maxConcurrentCeiling on desktop)")).toBeInTheDocument();
    unmount();

    seed(makeLoad({ maxConcurrent: 4 }));
    render(<RunnerCapacity />);
    const more = screen.getByRole("button", { name: "More at once on desktop" });
    expect(more).toBeDisabled();
    expect(more).toHaveAttribute("title", "At most 4 from here (maxConcurrentCeiling on desktop)");
    expect(screen.getByRole("button", { name: "Fewer at once on desktop" })).toBeEnabled();
  });

  it.each([
    ["offline", 4, "desktop is offline"],
    ["outdated", 4, "Update nodevas on desktop to change this here"],
    ["settings_off", 4, "desktop's owner allows changing this only on that machine (remoteSettings)"],
    // No ceiling in its hello: an older runner, which cannot be set from here.
    ["", 0, "Update nodevas on desktop to change this here"],
  ])("disables the stepper and says why (settable %j, ceiling %d)", (settable, ceiling, why) => {
    seed(makeLoad({ settable, ceiling }));
    render(<RunnerCapacity />);
    expect(stepper()).toHaveAttribute("title", why);
    expect(screen.getByRole("button", { name: "Fewer at once on desktop" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "More at once on desktop" })).toBeDisabled();
    expect(screen.getByText(why)).toBeInTheDocument();
  });

  it("maps the hub's word to a block", () => {
    expect(capacityBlock({ settable: "", ceiling: 8 })).toBe("");
    expect(capacityBlock({ settable: "settings_off", ceiling: 8 })).toBe("settings_off");
    expect(capacityBlock({ settable: "something_new", ceiling: 8 })).toBe("outdated");
    expect(capacityBlock({ settable: "", ceiling: 0 })).toBe("outdated");
  });

  it("asks the runner for one more at once and says it was saved", async () => {
    seed(makeLoad());
    let answer!: (value: { runner: Runner; persisted: boolean }) => void;
    const put = vi.spyOn(api, "setRunnerSettings").mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );
    render(<RunnerCapacity />);

    fireEvent.click(screen.getByRole("button", { name: "More at once on desktop" }));
    expect(put).toHaveBeenCalledWith("r1", { maxConcurrent: 3 });
    // Waiting for the runner: the stepper holds still.
    expect(await screen.findByText("Saving…")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "More at once on desktop" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Fewer at once on desktop" })).toBeDisabled();

    answer({ runner: runnerWith(3), persisted: true });
    expect(await screen.findByText("desktop now runs 3 at once")).toHaveAttribute("role", "status");
    expect(screen.getByLabelText("3 at once on desktop")).toHaveTextContent("3");
    expect(screen.getByRole("button", { name: "More at once on desktop" })).toBeEnabled();
  });

  it("says a change that could not be written lasts until the runner restarts", async () => {
    seed(makeLoad());
    const put = vi.spyOn(api, "setRunnerSettings").mockResolvedValue({ runner: runnerWith(1), persisted: false });
    render(<RunnerCapacity />);
    fireEvent.click(screen.getByRole("button", { name: "Fewer at once on desktop" }));
    expect(put).toHaveBeenCalledWith("r1", { maxConcurrent: 1 });
    expect(
      await screen.findByText("Applied until desktop restarts: its settings file could not be saved"),
    ).toHaveAttribute("role", "status");
  });

  it.each([
    [new GlobalRunError(409, "FAILED_PRECONDITION", "settings_off", "", 0, "off"), "desktop's owner allows changing this only on that machine (remoteSettings)"],
    [new GlobalRunError(400, "INVALID_ARGUMENT", "", "", 4, "too many"), "At most 4 from here (maxConcurrentCeiling on desktop)"],
    [new GlobalRunError(412, "RUNNER_OUTDATED", "", "", 0, "old"), "Update nodevas on desktop to change this here"],
    [new GlobalRunError(503, "RUNNER_OFFLINE", "", "", 0, "offline"), "desktop is offline"],
    [new GlobalRunError(504, "TIMEOUT", "", "", 0, "slow"), "desktop did not answer in time"],
    [new GlobalRunError(403, "PERMISSION_DENIED", "", "", 0, "The runner refused"), "The runner refused"],
  ])("explains a refusal: %s", async (error, text) => {
    seed(makeLoad());
    vi.spyOn(api, "setRunnerSettings").mockRejectedValue(error);
    render(<RunnerCapacity />);
    fireEvent.click(screen.getByRole("button", { name: "More at once on desktop" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(text);
    // The limit stays what the runner said.
    await waitFor(() => expect(screen.getByRole("button", { name: "More at once on desktop" })).toBeEnabled());
    expect(within(stepper()).getByLabelText("2 at once on desktop")).toHaveTextContent("2");
  });
});

// Simple (single-user edition): this computer's load without a setting's
// name or token counts; what it cost stays.
describe("RunnerCapacity in Simple", () => {
  function showIn(experience: "simple" | "full") {
    useApp.setState({ preferences: { ...useApp.getState().preferences, experience } });
    seed(makeLoad({ name: "this machine", maxConcurrent: 4, ceiling: 4 }));
    return render(
      <EditionProvider edition="single">
        <RunnerCapacity />
      </EditionProvider>,
    );
  }

  it("hides the ceiling's setting name and the token count", () => {
    const { container } = showIn("simple");
    const text = container.textContent ?? "";
    expect(text).not.toMatch(/maxConcurrentCeiling|tokens|12\.3K|runner|this machine/i);
    expect(text).toMatch(/\$0\.042/);
    expect(text).toMatch(/This computer/);
    for (const element of container.querySelectorAll("[title], [aria-label]")) {
      const said = `${element.getAttribute("title") ?? ""} ${element.getAttribute("aria-label") ?? ""}`;
      expect(said).not.toMatch(/maxConcurrentCeiling|this machine/);
    }
  });

  it("keeps them in full mode", () => {
    const { container } = showIn("full");
    expect(container.textContent).toMatch(/maxConcurrentCeiling/);
    expect(container.textContent).toMatch(/tokens/);
  });
});
