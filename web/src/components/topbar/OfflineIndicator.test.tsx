import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Runner } from "../../api";
import { markServerReachable, markServerUnreachable } from "../../api/connectivity";
import { readPreferences } from "../../preferences";
import { useRunners } from "../../runners/runnerStore";
import { useBudget } from "../../runs/budgetStore";
import { useApp } from "../../store";
import { OfflineIndicator } from "./OfflineIndicator";
import { RunnerPill } from "./RunnerPill";
import { TopBar } from "./TopBar";

vi.mock("../SignIn", () => ({ useViewer: () => null, useSignOut: () => vi.fn() }));

const runner = (over: Partial<Runner>): Runner =>
  ({ id: "r", name: "mac", ownerId: "ann", ownerName: "ann", createdAt: "", lastSeenAt: "", online: true, ...over }) as Runner;

beforeEach(() => {
  markServerReachable();
  useApp.setState({ preferences: { ...readPreferences(), language: "en", agentWorkstation: true } });
  useRunners.setState({ loaded: true, runners: [runner({ id: "a" })], refresh: vi.fn(async () => {}) });
  useBudget.setState({ status: null, load: vi.fn(async () => {}) });
});

describe("offline indicator", () => {
  it("is not drawn while the server answers", () => {
    const { container } = render(<OfflineIndicator />);
    expect(container).toBeEmptyDOMElement();
  });

  it("stays up while the server is out of reach, and clears on reconnect", () => {
    markServerUnreachable();
    const { rerender } = render(<OfflineIndicator />);
    expect(screen.getByRole("status")).toHaveTextContent("Offline: can't reach the server");
    markServerReachable();
    rerender(<OfflineIndicator />);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("speaks Traditional Chinese", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, language: "zh-TW" } });
    markServerUnreachable();
    render(<OfflineIndicator />);
    expect(screen.getByRole("status")).toHaveTextContent("離線：無法連線到伺服器");
  });

  it("sits on the top bar", () => {
    markServerUnreachable();
    render(<TopBar />);
    expect(screen.getByText("Offline: can't reach the server")).toBeInTheDocument();
  });
});

describe("runner pill while offline", () => {
  it("is green while the server answers", () => {
    render(<RunnerPill />);
    expect(screen.getByRole("button", { name: "1 runner online" }).querySelector(".runner-dot.online")).not.toBeNull();
  });

  it("goes grey and says the runners' state is unknown", () => {
    markServerUnreachable();
    render(<RunnerPill />);
    const pill = screen.getByRole("button", { name: "Runner status unknown (offline)" });
    expect(pill.querySelector(".runner-dot.online")).toBeNull();
    expect(pill.querySelector(".runner-dot.unknown")).not.toBeNull();
    expect(pill.textContent).toBe("?");
  });
});
