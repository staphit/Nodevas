import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type AutoAcceptPolicy, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { formatClock } from "../stepup/StepUpDialog";
import { useStepUp } from "../stepup/stepUpStore";
import { RunnersModal } from "./RunnersModal";
import { useRunners } from "./runnerStore";

function runner(id: string, name: string, autoAccept?: AutoAcceptPolicy): Runner {
  return {
    id,
    name,
    ownerId: "u1",
    ownerName: "alice",
    createdAt: "2026-10-23T00:00:00Z",
    lastSeenAt: "2026-10-24T08:00:00Z",
    online: true,
    info: {
      version: "1.4.0",
      hostname: name,
      os: "linux",
      arch: "amd64",
      agents: [],
      repos: [
        { id: "repo1", name: "nodevas", path: "/src/nodevas" },
        { id: "repo2", name: "site", path: "/src/site" },
      ],
      autoAccept,
    },
  };
}

const strict = runner("r1", "desktop", {
  enabled: true,
  requireStepUp: true,
  repos: ["repo1", "gone"],
  agents: ["claude", "codex"],
  modes: ["", "interactive"],
});
const relaxed = runner("r2", "laptop", {
  enabled: true,
  requireStepUp: false,
  repos: [],
  agents: ["claude"],
});
const off = runner("r3", "build-box", { enabled: false, requireStepUp: true });
const old = runner("r4", "old-box");

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useStepUp.getState().reset();
});

afterEach(() => {
  useStepUp.getState().reset();
  vi.restoreAllMocks();
});

describe("RunnersModal auto-accept policies", () => {
  it("shows each runner's policy read-only, with how to change it", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [strict, relaxed, off, old] });
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false });

    render(<RunnersModal onClose={vi.fn()} />);

    const first = await screen.findByTestId("runner-policy-r1");
    expect(within(first).getByText("On")).toBeInTheDocument();
    // Repos by name; an id the runner no longer lists shows as itself.
    expect(within(first).getByText("nodevas")).toBeInTheDocument();
    expect(within(first).getByText("gone")).toBeInTheDocument();
    expect(within(first).getByText("claude")).toBeInTheDocument();
    expect(within(first).getByText("codex")).toBeInTheDocument();
    expect(within(first).getByText("Headless")).toBeInTheDocument();
    expect(within(first).getByText("Interactive")).toBeInTheDocument();
    expect(within(first).getByText(/Required: only from someone who verified/)).toBeInTheDocument();
    expect(within(first).getByText("nodevas runner autoaccept on|off|show")).toBeInTheDocument();
    expect(within(first).getByText("nodevas runner autoaccept step-up required|optional")).toBeInTheDocument();
    expect(within(first).getByText("nodevas runner autoaccept repo|agent|mode add|remove …")).toBeInTheDocument();
    expect(within(first).getByText(/Change it on the runner/)).toBeInTheDocument();

    const second = screen.getByTestId("runner-policy-r2");
    expect(within(second).getByText("None")).toBeInTheDocument();
    expect(within(second).getByText("Headless")).toBeInTheDocument();
    expect(within(second).getByText(/Not required: any signed-in dispatch/)).toBeInTheDocument();

    const third = screen.getByTestId("runner-policy-r3");
    expect(within(third).getByText("Off")).toBeInTheDocument();
    expect(within(third).getByText(/every run waits for someone at the runner/)).toBeInTheDocument();

    expect(within(screen.getByTestId("runner-policy-r4")).getByText(/predates auto-accept/)).toBeInTheDocument();
    // Read-only: nothing to edit here.
    expect(within(first).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(within(first).queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("offers to verify identity, and opens the step-up dialog", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [strict] });
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false });

    render(<RunnersModal onClose={vi.fn()} />);
    expect(await screen.findByText(/Not verified: runners that ask for it/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Verify identity" }));
    expect(await screen.findByRole("dialog", { name: "Verify it's you" })).toBeInTheDocument();
  });

  it("says until when this browser is verified", async () => {
    const until = new Date(Date.now() + 8 * 60 * 1000).toISOString();
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [strict] });
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: true, until });

    render(<RunnersModal onClose={vi.fn()} />);
    expect(await screen.findByText(`Verified until ${formatClock(until)}`)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Verify again" })).toBeInTheDocument();
  });

  it("hides everything step-up on a hub that does not require it", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [strict] });
    const status = vi.spyOn(api, "getStepUp").mockResolvedValue({ required: false, active: false });

    render(<RunnersModal onClose={vi.fn()} />);
    const policy = await screen.findByTestId("runner-policy-r1");
    await vi.waitFor(() => expect(status).toHaveBeenCalled());
    await vi.waitFor(() => expect(useStepUp.getState().status).toEqual({ required: false, active: false }));
    expect(screen.queryByRole("button", { name: "Verify identity" })).not.toBeInTheDocument();
    expect(within(policy).queryByText(/Required: only from someone/)).not.toBeInTheDocument();
    expect(within(policy).queryByText("Verify first")).not.toBeInTheDocument();
  });
});
