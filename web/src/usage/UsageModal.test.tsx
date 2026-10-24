import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError, type UsageBucket, type UsagePrices, type UsageReport } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { UsageModal } from "./UsageModal";
import { USAGE_EVENT_DELAY_MS, useUsage } from "./usageStore";

const openNodeRuns = vi.fn();
vi.mock("../runs/openRuns", () => ({
  openNodeRuns: (...args: unknown[]) => openNodeRuns(...args),
}));

function bucket(over: Partial<UsageBucket> = {}): UsageBucket {
  const b: UsageBucket = {
    inputTokens: 0,
    outputTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    totalTokens: 0,
    costUsd: 0,
    reportedCostUsd: 0,
    estimatedCostUsd: 0,
    unpricedTokens: 0,
    runs: 0,
    sessions: 0,
    ...over,
  };
  if (!over.totalTokens) {
    b.totalTokens = b.inputTokens + b.outputTokens + b.cacheReadTokens + b.cacheWriteTokens;
  }
  return b;
}

function report(over: Partial<UsageReport> = {}): UsageReport {
  return {
    days: 30,
    since: "2026-09-25T00:00:00Z",
    currency: "USD",
    pricesAsOf: "2026-10-06",
    totals: bucket({
      inputTokens: 1_200_000,
      outputTokens: 300_000,
      cacheReadTokens: 500_000,
      costUsd: 12.5,
      estimatedCostUsd: 10,
      reportedCostUsd: 2.5,
      unpricedTokens: 40_000,
      runs: 3,
      sessions: 2,
    }),
    byAgent: [
      { key: "claude", ...bucket({ inputTokens: 1_000_000, costUsd: 9, runs: 2, sessions: 1 }) },
      { key: "codex", ...bucket({ inputTokens: 200_000, costUsd: 3.5, unpricedTokens: 40_000 }) },
    ],
    byModel: [
      { key: "claude-opus-5-5", agent: "claude", ...bucket({ inputTokens: 1_000_000, costUsd: 9 }) },
      { key: "gpt-5.5", agent: "codex", ...bucket({ inputTokens: 40_000, unpricedTokens: 40_000 }) },
    ],
    byDay: [
      { key: "2026-10-23", ...bucket({ inputTokens: 100, outputTokens: 50, costUsd: 1 }) },
      { key: "2026-10-24", ...bucket({ inputTokens: 300, outputTokens: 20, cacheReadTokens: 10, costUsd: 2 }) },
    ],
    sessions: [
      {
        id: "claude:abc",
        source: "session",
        name: "visual-editor-claude",
        actorName: "patrick",
        agent: "claude",
        model: "claude-opus-5-5",
        models: ["claude-opus-5-5"],
        nodeId: "",
        runId: "",
        firstAt: "2026-10-24T01:00:00Z",
        lastAt: "2026-10-24T09:00:00Z",
        ...bucket({ inputTokens: 5000, costUsd: 0.5 }),
      },
      {
        id: "run-1",
        source: "run",
        name: "Add hello.txt",
        actorName: "",
        agent: "codex",
        model: "gpt-5.5",
        models: [],
        nodeId: "node-0001",
        runId: "run-1",
        firstAt: "2026-10-23T01:00:00Z",
        lastAt: "2026-10-23T02:00:00Z",
        ...bucket({ inputTokens: 90_000, costUsd: 4 }),
      },
    ],
    ...over,
  };
}

const prices: UsagePrices = {
  asOf: "2026-10-06",
  prices: [
    {
      model: "claude-opus-5-5",
      inputPerMTok: 4,
      outputPerMTok: 20,
      cacheReadPerMTok: 0.2,
      cacheWritePerMTok: 5,
      source: "builtin",
    },
  ],
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useUsage.setState({
    loaded: false,
    open: false,
    loading: false,
    days: 30,
    report: null,
    error: null,
    pricesLoaded: false,
    pricesLoading: false,
    prices: null,
    pricesError: null,
    saving: false,
    pricesForbidden: false,
  });
  openNodeRuns.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("UsageModal", () => {
  it("shows totals, the monthly projection and the tables", async () => {
    const get = vi.spyOn(api, "getUsage").mockResolvedValue(report());
    render(<UsageModal onClose={vi.fn()} />);

    expect(await screen.findByText("2M")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(30);
    expect(screen.getByText("Estimated cost").parentElement).toHaveTextContent("$12.50");
    // 12.50 over 30 days is 12.50 per 30 days, 0.42 a day.
    expect(screen.getByText("Per 30 days at this rate").parentElement).toHaveTextContent("$12.50");
    expect(screen.getByText("≈ $0.42 a day")).toBeInTheDocument();
    expect(screen.getByText("3 runs · 2 sessions")).toBeInTheDocument();
    expect(screen.getByText("3 · 2")).toBeInTheDocument();

    const agents = screen.getByRole("region", { name: "By agent" });
    expect(within(agents).getByText("claude")).toBeInTheDocument();
    expect(within(agents).getByText("72%")).toBeInTheDocument();
    const models = screen.getByRole("region", { name: "By model" });
    expect(within(models).getByText("claude-opus-5-5")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Daily usage chart/ })).toBeInTheDocument();
  });

  it("refetches when the range changes", async () => {
    const get = vi.spyOn(api, "getUsage").mockResolvedValue(report());
    render(<UsageModal onClose={vi.fn()} />);
    await screen.findByText("2M");

    fireEvent.click(screen.getByRole("button", { name: "7 days" }));
    await waitFor(() => expect(get).toHaveBeenLastCalledWith(7));
    expect(screen.getByRole("button", { name: "7 days" })).toHaveAttribute("aria-pressed", "true");
    expect(useUsage.getState().days).toBe(7);
  });

  it("shows the chart as a table", async () => {
    vi.spyOn(api, "getUsage").mockResolvedValue(report());
    render(<UsageModal onClose={vi.fn()} />);
    await screen.findByText("2M");

    fireEvent.click(screen.getByRole("button", { name: "Show as table" }));
    expect(screen.getByRole("columnheader", { name: "Day" })).toBeInTheDocument();
    expect(screen.getByText("300")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cost" }));
    expect(screen.getByText("$2.00")).toBeInTheDocument();
  });

  it("warns about unpriced tokens and opens Prices with the model ready", async () => {
    vi.spyOn(api, "getUsage").mockResolvedValue(report());
    vi.spyOn(api, "getUsagePrices").mockResolvedValue(prices);
    render(<UsageModal onClose={vi.fn()} />);

    expect(await screen.findByText(/40K tokens have no price \(gpt-5\.5\)/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add prices" }));

    expect(await screen.findByDisplayValue("gpt-5.5")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Prices" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText(/built-in as of 2026-10-06/)).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Built-in prices" })).toHaveTextContent("claude-opus-5-5");
  });

  it("hides the warning when everything is priced", async () => {
    vi.spyOn(api, "getUsage").mockResolvedValue(
      report({ totals: bucket({ inputTokens: 10, costUsd: 1 }) }),
    );
    render(<UsageModal onClose={vi.fn()} />);
    expect((await screen.findByText("Estimated cost")).parentElement).toHaveTextContent("$1.00");
    expect(screen.queryByText(/have no price/)).not.toBeInTheDocument();
  });

  it("saves custom prices and refetches", async () => {
    const get = vi.spyOn(api, "getUsage").mockResolvedValue(report());
    const load = vi.spyOn(api, "getUsagePrices").mockResolvedValue(prices);
    const put = vi.spyOn(api, "putUsagePrices").mockResolvedValue(prices);
    render(<UsageModal onClose={vi.fn()} />);
    await screen.findByText("2M");

    fireEvent.click(screen.getByRole("tab", { name: "Prices" }));
    await screen.findByText("No custom prices.");
    fireEvent.click(screen.getByRole("button", { name: "Add price" }));
    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "gpt-5.5" } });
    fireEvent.change(screen.getByLabelText("Input — gpt-5.5"), { target: { value: "1.25" } });
    fireEvent.change(screen.getByLabelText("Output — gpt-5.5"), { target: { value: "10" } });
    fireEvent.click(screen.getByRole("button", { name: "Save prices" }));

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith([
        { model: "gpt-5.5", inputPerMTok: 1.25, outputPerMTok: 10, cacheReadPerMTok: 0, cacheWritePerMTok: 0 },
      ]),
    );
    expect(await screen.findByText(/Prices saved/)).toBeInTheDocument();
    expect(load.mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(get.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("refuses a price that is not a number", async () => {
    vi.spyOn(api, "getUsage").mockResolvedValue(report());
    vi.spyOn(api, "getUsagePrices").mockResolvedValue(prices);
    const put = vi.spyOn(api, "putUsagePrices").mockResolvedValue(prices);
    render(<UsageModal onClose={vi.fn()} />);
    await screen.findByText("2M");

    fireEvent.click(screen.getByRole("tab", { name: "Prices" }));
    await screen.findByText("No custom prices.");
    fireEvent.click(screen.getByRole("button", { name: "Add price" }));
    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "x" } });
    fireEvent.change(screen.getByLabelText("Input — x"), { target: { value: "-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Save prices" }));

    expect(await screen.findByText(/must be numbers/)).toBeInTheDocument();
    expect(put).not.toHaveBeenCalled();
  });

  it("says so when only an admin may change prices", async () => {
    vi.spyOn(api, "getUsage").mockResolvedValue(report());
    vi.spyOn(api, "getUsagePrices").mockResolvedValue({
      ...prices,
      prices: [...prices.prices, { ...prices.prices[0], model: "mine", source: "custom" }],
    });
    vi.spyOn(api, "putUsagePrices").mockRejectedValue(new AuthError(403, "forbidden"));
    render(<UsageModal onClose={vi.fn()} />);
    await screen.findByText("2M");

    fireEvent.click(screen.getByRole("tab", { name: "Prices" }));
    expect(await screen.findByDisplayValue("mine")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save prices" }));
    expect(await screen.findByText("Only an admin can change prices.")).toBeInTheDocument();
  });

  it("lists sessions and runs, sorts them, and opens a run", async () => {
    vi.spyOn(api, "getUsage").mockResolvedValue(report());
    const onClose = vi.fn();
    render(<UsageModal onClose={onClose} />);
    await screen.findByText("2M");

    fireEvent.click(screen.getByRole("tab", { name: "Sessions" }));
    const rows = () => screen.getAllByTestId(/^usage-session-/).map((r) => r.dataset.testid);
    // Most recently active first.
    expect(rows()).toEqual(["usage-session-claude:abc", "usage-session-run-1"]);
    fireEvent.click(screen.getByRole("button", { name: /Cost/ }));
    expect(rows()).toEqual(["usage-session-run-1", "usage-session-claude:abc"]);

    expect(within(screen.getByTestId("usage-session-claude:abc")).getByText("Session")).toBeInTheDocument();
    expect(screen.getByText("patrick")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add hello.txt" }));
    expect(onClose).toHaveBeenCalled();
    expect(openNodeRuns).toHaveBeenCalledWith("node-0001", "run-1");
  });

  it("shows the error and an empty state", async () => {
    vi.spyOn(api, "getUsage").mockRejectedValue(new Error("boom"));
    render(<UsageModal onClose={vi.fn()} />);
    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.getByText(/No token use recorded/)).toBeInTheDocument();
  });

  it("closes on Escape", async () => {
    vi.spyOn(api, "getUsage").mockResolvedValue(report());
    const onClose = vi.fn();
    render(<UsageModal onClose={onClose} />);
    await screen.findByText("2M");
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  it("refetches on realtime events only while open, once per burst", async () => {
    vi.useFakeTimers();
    try {
      const get = vi.spyOn(api, "getUsage").mockResolvedValue(report());
      useUsage.getState().onUsageEvent();
      await vi.advanceTimersByTimeAsync(USAGE_EVENT_DELAY_MS + 10);
      expect(get).not.toHaveBeenCalled();

      const view = render(<UsageModal onClose={vi.fn()} />);
      await vi.advanceTimersByTimeAsync(0);
      expect(get).toHaveBeenCalledTimes(1);
      expect(useUsage.getState().open).toBe(true);
      useUsage.getState().onUsageEvent();
      useUsage.getState().onUsageEvent();
      useUsage.getState().onUsageEvent();
      await vi.advanceTimersByTimeAsync(USAGE_EVENT_DELAY_MS + 10);
      expect(get).toHaveBeenCalledTimes(2);

      view.unmount();
      expect(useUsage.getState().open).toBe(false);
      useUsage.getState().onUsageEvent();
      await vi.advanceTimersByTimeAsync(USAGE_EVENT_DELAY_MS + 10);
      expect(get).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("refreshes on usage-changed only once loaded", async () => {
    const get = vi.spyOn(api, "getUsage").mockResolvedValue(report());
    await useUsage.getState().refreshIfLoaded();
    expect(get).not.toHaveBeenCalled();
    await useUsage.getState().refresh();
    await useUsage.getState().refreshIfLoaded();
    expect(get).toHaveBeenCalledTimes(2);
  });
});
