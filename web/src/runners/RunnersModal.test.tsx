import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Runner } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useBudget } from "../runs/budgetStore";
import { RunnersModal } from "./RunnersModal";
import { useRunners } from "./runnerStore";

const desktop: Runner = {
  id: "r1",
  name: "desktop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-23T08:00:00Z",
  online: true,
  info: {
    version: "1.2.3",
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    agents: [
      {
        kind: "claude",
        installed: true,
        version: "2.1.0 (Claude Code)",
        models: [
          { id: "default", label: "CLI default", default: true },
          { id: "opus" },
        ],
      },
      { kind: "gemini", installed: false, models: [] },
    ],
    repos: [{ id: "repo1", name: "nodevas", remote: "github.com/staphit/Nodevas" }],
  },
};

const laptop: Runner = {
  id: "r2",
  name: "laptop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "",
  online: false,
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({
    loaded: false,
    loading: false,
    runners: [],
    error: null,
    creating: false,
    revoking: {},
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("RunnersModal", () => {
  it("lists runners with their agents, models and repos", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop, laptop] });

    render(<RunnersModal onClose={vi.fn()} />);

    expect(await screen.findByText("desktop")).toBeInTheDocument();
    expect(screen.getByText("laptop")).toBeInTheDocument();
    expect(screen.getByText(/Online/)).toBeInTheDocument();
    expect(screen.getByText(/Never connected/)).toBeInTheDocument();
    expect(screen.getByText("PC-01 · windows/amd64 · nodevas 1.2.3")).toBeInTheDocument();
    expect(screen.getByText("2.1.0 (Claude Code)")).toBeInTheDocument();
    expect(screen.getByText("CLI default ★")).toBeInTheDocument();
    expect(screen.getByText("opus")).toBeInTheDocument();
    expect(screen.getByText("Not installed")).toBeInTheDocument();
    expect(screen.getByText("github.com/staphit/Nodevas")).toBeInTheDocument();
  });

  it("shows an empty state", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: null });
    render(<RunnersModal onClose={vi.fn()} />);
    expect(await screen.findByText(/No runners yet/)).toBeInTheDocument();
  });

  it("shows the token once after creating a runner", async () => {
    const list = vi
      .spyOn(api, "listRunners")
      .mockResolvedValueOnce({ runners: [] })
      .mockResolvedValue({ runners: [laptop] });
    const create = vi
      .spyOn(api, "createRunner")
      .mockResolvedValue({ runner: laptop, token: "nvr_secret-token" });

    render(<RunnersModal onClose={vi.fn()} />);
    await screen.findByText(/No runners yet/);

    fireEvent.click(screen.getByRole("button", { name: "Add runner" }));
    fireEvent.change(screen.getByLabelText("Runner name"), { target: { value: " laptop " } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    const token = await screen.findByLabelText("Runner token", { selector: "input" });
    expect(create).toHaveBeenCalledWith("laptop");
    expect(token).toHaveValue("nvr_secret-token");
    expect(screen.getByText(/shown only once/)).toBeInTheDocument();
    expect(screen.getByText(/nodevas runner login --hub/)).toBeInTheDocument();
    expect(list).toHaveBeenCalledTimes(2);

    fireEvent.click(screen.getByRole("button", { name: "I saved the token" }));
    expect(screen.queryByDisplayValue("nvr_secret-token")).not.toBeInTheDocument();
  });

  it("revokes a runner only after confirmation", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const revoke = vi.spyOn(api, "revokeRunner").mockResolvedValue(undefined);

    render(
      <>
        <RunnersModal onClose={vi.fn()} />
        <ConfirmDialogHost />
      </>,
    );
    await screen.findByText("desktop");

    fireEvent.click(screen.getByRole("button", { name: "Revoke runner desktop" }));
    expect(await screen.findByRole("alertdialog")).toBeInTheDocument();
    expect(revoke).not.toHaveBeenCalled();

    const confirm = screen
      .getAllByRole("button", { name: "Revoke" })
      .find((button) => button.closest(".confirm-dialog.danger"));
    expect(confirm).toBeDefined();
    fireEvent.click(confirm!);

    await waitFor(() => expect(revoke).toHaveBeenCalledWith("r1"));
  });

  it("fetches the budget only once its section is opened", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const budget = vi.spyOn(api, "getRunBudget").mockResolvedValue({
      budget: { dailyRuns: 5, dailyCostUsd: 0, dailyTokens: 0 },
      used: { runs: 1, costUsd: 0, tokens: 0 },
      exceeded: "",
    });
    useBudget.getState().reset();

    render(<RunnersModal onClose={vi.fn()} />);
    await screen.findByText("desktop");
    expect(budget).not.toHaveBeenCalled();

    const section = screen.getByText("Daily budget").closest("details")!;
    section.open = true;
    fireEvent(section, new Event("toggle"));
    expect(await screen.findByText("1 / 5")).toBeInTheDocument();
    expect(budget).toHaveBeenCalledTimes(1);
  });

  it("opens on the budget when a dispatch dialog sent us there", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
    vi.spyOn(api, "getRunBudget").mockResolvedValue({
      budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
      used: { runs: 0, costUsd: 0, tokens: 0 },
      exceeded: "",
    });
    useBudget.getState().reset();
    useBudget.getState().requestShow(true);

    render(<RunnersModal onClose={vi.fn()} />);
    expect(await screen.findByRole("spinbutton", { name: "Runs per day" })).toBeInTheDocument();
    expect(useBudget.getState().showRequested).toBe(false);
  });

  it("refreshes on runners-changed only once loaded", async () => {
    const list = vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });

    await act(() => useRunners.getState().refreshIfLoaded());
    expect(list).not.toHaveBeenCalled();

    await act(() => useRunners.getState().refresh());
    await act(() => useRunners.getState().refreshIfLoaded());
    expect(list).toHaveBeenCalledTimes(2);
    expect(useRunners.getState().runners).toEqual([desktop]);
  });
});
