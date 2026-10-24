import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  type Actor,
  type AutoAcceptPolicy,
  type RunBudgetStatus,
  type Runner,
  type StepUpStatus,
} from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import { formatClock } from "../stepup/StepUpDialog";
import { useStepUp } from "../stepup/stepUpStore";
import { autoAcceptOutcome, modeAllowed } from "./autoAccept";
import { useBudget } from "./budgetStore";
import { DispatchDialog } from "./DispatchDialog";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const NOW = Date.parse("2026-10-24T14:00:00Z");
const later = "2026-10-24T14:05:00Z";
const earlier = "2026-10-24T13:55:00Z";

const policy: AutoAcceptPolicy = {
  enabled: true,
  requireStepUp: true,
  repos: ["repo1"],
  agents: ["claude"],
};
const job = { repoId: "repo1", agent: "claude", mode: "" };
const notStepped: StepUpStatus = { required: true, active: false };
const stepped: StepUpStatus = { required: true, active: true, until: later };
const loopback: StepUpStatus = { required: false, active: false };

describe("autoAcceptOutcome", () => {
  it("auto-accepts an allowed job while the dispatcher is stepped up", () => {
    expect(autoAcceptOutcome(policy, stepped, job, NOW)).toEqual({
      kind: "auto",
      stepUp: "active",
      until: later,
    });
  });

  it("asks to verify when the policy wants a step-up nobody did", () => {
    expect(autoAcceptOutcome(policy, notStepped, job, NOW)).toEqual({ kind: "verify" });
    // A step-up whose window ran out counts as none.
    expect(
      autoAcceptOutcome(policy, { required: true, active: true, until: earlier }, job, NOW),
    ).toEqual({ kind: "verify" });
  });

  it("needs no step-up when the policy does not ask for one", () => {
    const relaxed = { ...policy, requireStepUp: false };
    expect(autoAcceptOutcome(relaxed, notStepped, job, NOW)).toEqual({
      kind: "auto",
      stepUp: "notRequired",
    });
    // Not even an answer from the hub is needed.
    expect(autoAcceptOutcome(relaxed, undefined, job, NOW).kind).toBe("auto");
  });

  it("treats a hub without step-up (loopback) as vouching for the dispatch", () => {
    expect(autoAcceptOutcome(policy, loopback, job, NOW)).toEqual({
      kind: "auto",
      stepUp: "notNeeded",
    });
  });

  it("waits for the step-up answer, and falls back to manual without one", () => {
    expect(autoAcceptOutcome(policy, undefined, job, NOW)).toEqual({ kind: "pending" });
    expect(autoAcceptOutcome(policy, null, job, NOW)).toEqual({
      kind: "manual",
      reason: "stepUpUnavailable",
    });
  });

  it("names why someone at the runner must accept", () => {
    expect(autoAcceptOutcome(undefined, stepped, job, NOW)).toEqual({ kind: "manual", reason: "off" });
    expect(autoAcceptOutcome({ ...policy, enabled: false }, stepped, job, NOW)).toEqual({
      kind: "manual",
      reason: "off",
    });
    expect(autoAcceptOutcome(policy, stepped, { ...job, repoId: "repo2" }, NOW)).toEqual({
      kind: "manual",
      reason: "repo",
    });
    expect(autoAcceptOutcome(policy, stepped, { ...job, agent: "codex" }, NOW)).toEqual({
      kind: "manual",
      reason: "agent",
    });
    expect(autoAcceptOutcome(policy, stepped, { ...job, mode: "interactive" }, NOW)).toEqual({
      kind: "manual",
      reason: "mode",
    });
    // Empty lists mean none, not all.
    expect(
      autoAcceptOutcome({ ...policy, repos: [], agents: [] }, stepped, job, NOW),
    ).toEqual({ kind: "manual", reason: "repo" });
  });

  it("reads empty modes as headless only", () => {
    expect(modeAllowed(policy, "")).toBe(true);
    expect(modeAllowed(policy, "interactive")).toBe(false);
    expect(modeAllowed({ ...policy, modes: ["interactive"] }, "")).toBe(false);
    expect(modeAllowed({ ...policy, modes: ["", "interactive"] }, "interactive")).toBe(true);
    expect(modeAllowed({ ...policy, modes: ["headless"] }, "")).toBe(true);
  });
});

const alice: Actor = { id: "u1", name: "alice", role: "admin" };

function runnerWith(autoAccept: AutoAcceptPolicy | undefined): Runner {
  return {
    id: "r1",
    name: "desktop",
    ownerId: "u1",
    ownerName: "alice",
    createdAt: "2026-10-23T00:00:00Z",
    lastSeenAt: "2026-10-24T08:00:00Z",
    online: true,
    info: {
      version: "1.4.0",
      hostname: "desktop",
      os: "windows",
      arch: "amd64",
      capabilities: ["headless"],
      agents: [
        { kind: "claude", installed: true, models: [{ id: "sonnet", default: true }] },
        { kind: "codex", installed: true, models: [{ id: "gpt-5" }] },
      ],
      repos: [
        { id: "repo1", name: "nodevas" },
        { id: "repo2", name: "site" },
      ],
      autoAccept,
    },
  };
}

const withinBudget: RunBudgetStatus = {
  budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
  used: { runs: 0, costUsd: 0, tokens: 0 },
  exceeded: "",
};

async function openDialog(runner: Runner, stepUp: StepUpStatus | Error) {
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [runner] });
  if (stepUp instanceof Error) vi.spyOn(api, "getStepUp").mockRejectedValue(stepUp);
  else vi.spyOn(api, "getStepUp").mockResolvedValue(stepUp);
  render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
  await screen.findByRole("combobox", { name: "Runner" });
  const note = await screen.findByTestId("dispatch-auto-accept");
  await waitFor(() => expect(note.dataset.outcome).not.toBe("pending"));
  return note;
}

describe("DispatchDialog auto-accept explanation", () => {
  beforeEach(() => {
    viewer.current = alice;
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
    useRuns.getState().reset();
    useBudget.getState().reset();
    useStepUp.getState().reset();
    vi.spyOn(api, "getRunBudget").mockResolvedValue(withinBudget);
  });

  afterEach(() => {
    useStepUp.getState().reset();
    vi.restoreAllMocks();
  });

  it("says it auto-accepts, until when the step-up lasts", async () => {
    const until = new Date(Date.now() + 5 * 60 * 1000).toISOString();
    const note = await openDialog(runnerWith(policy), { required: true, active: true, until });
    expect(note.dataset.outcome).toBe("auto");
    expect(note).toHaveTextContent(
      `Auto-accepts: nodevas is allowed on desktop, and you're verified until ${formatClock(until)}.`,
    );
  });

  it("offers to verify, and opens the step-up dialog", async () => {
    const note = await openDialog(runnerWith(policy), { required: true, active: false });
    expect(note.dataset.outcome).toBe("verify");
    expect(note).toHaveTextContent("Will auto-accept after you verify it's you.");
    fireEvent.click(screen.getByRole("button", { name: "Verify" }));
    expect(await screen.findByRole("dialog", { name: "Verify it's you" })).toBeInTheDocument();
    // The dispatch dialog stays open underneath.
    expect(screen.getByRole("dialog", { name: "Dispatch to agent" })).toBeInTheDocument();
  });

  it("follows the chosen repo and agent", async () => {
    const note = await openDialog(runnerWith({ ...policy, requireStepUp: false }), {
      required: true,
      active: false,
    });
    expect(note.dataset.outcome).toBe("auto");
    expect(note).toHaveTextContent("which doesn't ask you to verify");

    fireEvent.change(screen.getByRole("combobox", { name: "Repository" }), { target: { value: "repo2" } });
    expect(screen.getByTestId("dispatch-auto-accept").dataset.outcome).toBe("manual");
    expect(screen.getByTestId("dispatch-auto-accept")).toHaveTextContent(
      "site isn't on desktop's auto-accept list.",
    );

    fireEvent.change(screen.getByRole("combobox", { name: "Repository" }), { target: { value: "repo1" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Agent" }), { target: { value: "codex" } });
    expect(screen.getByTestId("dispatch-auto-accept")).toHaveTextContent(
      "codex isn't on desktop's auto-accept list.",
    );
  });

  it("says someone must accept when auto-accept is off or unknown", async () => {
    const off = await openDialog(runnerWith({ ...policy, enabled: false }), stepped);
    expect(off.dataset.outcome).toBe("manual");
    expect(off).toHaveTextContent("waits until someone at the runner accepts it");
    expect(off).toHaveTextContent("desktop has auto-accept off.");
  });

  it("shows only the plain note for a runner that reports no policy", async () => {
    const note = await openDialog(runnerWith(undefined), notStepped);
    expect(note.dataset.outcome).toBe("manual");
    expect(note.textContent).toBe("Nothing starts yet: the run waits until someone at the runner accepts it.");
  });

  it("hides every step-up word on a hub that does not use it", async () => {
    const note = await openDialog(runnerWith(policy), loopback);
    expect(note.dataset.outcome).toBe("auto");
    expect(note.textContent).toBe("Auto-accepts: nodevas is allowed on desktop.");
    expect(screen.queryByRole("button", { name: "Verify" })).not.toBeInTheDocument();
  });

  it("falls back to manual when the hub cannot answer about step-up", async () => {
    const note = await openDialog(runnerWith(policy), new Error("404"));
    expect(note.dataset.outcome).toBe("manual");
    expect(note).toHaveTextContent("which this server can't check");
  });
});
