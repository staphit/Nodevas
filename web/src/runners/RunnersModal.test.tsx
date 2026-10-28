import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Runner } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useBudget } from "../runs/budgetStore";
import { RunnersPanel } from "./RunnersModal";
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

describe("RunnersPanel", () => {
  it("lists each runner in one line, with its details folded away", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop, laptop] });

    render(<RunnersPanel />);

    expect(await screen.findByText("desktop")).toBeInTheDocument();
    expect(screen.getByText("laptop")).toBeInTheDocument();
    // Installed agents only; the missing gemini is a detail.
    expect(screen.getByText("Online · claude · 1 repo")).toBeInTheDocument();
    expect(screen.getByText("Never connected")).toBeInTheDocument();
    expect(screen.queryByText("CLI default ★")).not.toBeInTheDocument();
    expect(screen.queryByText("github.com/staphit/Nodevas")).not.toBeInTheDocument();
    // One primary button: Add runner.
    expect(screen.getAllByRole("button").filter((button) => button.classList.contains("primary"))).toHaveLength(1);

    const details = screen.getByRole("button", { name: "Details for desktop" });
    expect(details).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(details);
    expect(details).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("PC-01 · windows/amd64 · nodevas 1.2.3")).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.getByText("2.1.0 (Claude Code)")).toBeInTheDocument();
    expect(screen.getByText("CLI default ★")).toBeInTheDocument();
    expect(screen.getByText("opus")).toBeInTheDocument();
    expect(screen.getByText("Not installed")).toBeInTheDocument();
    expect(screen.getByText("github.com/staphit/Nodevas")).toBeInTheDocument();

    fireEvent.click(details);
    expect(screen.queryByText("CLI default ★")).not.toBeInTheDocument();
  });

  // At 150% text on a phone the line is cut ("…1 個 r…"); whole on hover.
  it("gives a runner's line its whole text on hover", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    render(<RunnersPanel />);
    expect(await screen.findByText("Online · claude · 1 repo")).toHaveAttribute(
      "title",
      "Online · claude · 1 repo",
    );
  });

  // A repo's path was cut with "…" and no tooltip: whole on hover, and it
  // wraps rather than cuts, so the full path can be read and selected.
  it("shows a repo's whole path, wrapped and with a tooltip", async () => {
    const path = "D:/work/clients/a-very-long-customer-name/monorepo/packages/server";
    const local: Runner = {
      ...desktop,
      info: { ...desktop.info!, repos: [{ id: "repo2", name: "server", path }] },
    };
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [local] });
    render(<RunnersPanel />);
    fireEvent.click(await screen.findByRole("button", { name: "Details for desktop" }));
    const shown = screen.getByText(path);
    expect(shown).toHaveAttribute("title", path);
    expect(shown).toHaveClass("runner-repo-path");
    expect(screen.getByText("desktop")).toHaveAttribute("title", "desktop");
  });

  it("names the hub's own runner in the reader's language", async () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, language: "zh-TW" } });
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [{ ...desktop, name: "this machine" }] });
    render(<RunnersPanel />);
    expect(await screen.findByText("本機")).toBeInTheDocument();
    expect(screen.queryByText("this machine")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /本機/ })).toHaveAttribute("aria-expanded", "false");
  });

  it("shows a runner's details in zh-TW without the runner's English", async () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, language: "zh-TW" } });
    const blocked: Runner = {
      ...desktop,
      info: {
        ...desktop.info!,
        agents: [
          ...(desktop.info!.agents ?? []),
          {
            kind: "antigravity",
            installed: false,
            version: "",
            models: [{ id: "default", label: "CLI default", default: true }],
            error:
              'antigravity: runs need "mcp(nodevas/*)" in permissions.allow of ~/.gemini/antigravity-cli/settings.json; ' +
              "run `nodevas setup-agent --runs-only --agents antigravity`",
          },
          {
            kind: "opencode",
            installed: false,
            models: [],
            error: "opencode: version 3 is not supported yet; runs need OpenCode 1.x or 2.x",
          },
        ],
      },
    };
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [blocked] });

    render(<RunnersPanel />);
    await screen.findByText("desktop");
    fireEvent.click(screen.getByRole("button", { name: /desktop/ }));

    const details = document.querySelector(".runner-details") as HTMLElement;
    const text = details.textContent ?? "";
    for (const english of ["CLI default", "runs need", "is not supported yet", "Not installed", "Installed"]) {
      expect(text).not.toContain(english);
    }
    expect(screen.getByText("CLI 預設 ★")).toBeInTheDocument();
    // The fix is still there to copy: the rule and the command stay as they are.
    expect(text).toContain("mcp(nodevas/*)");
    expect(text).toContain("nodevas setup-agent --runs-only --agents antigravity");
    expect(text).toContain("OpenCode 1.x");
  });

  it("opens the add form when asked from outside, once", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
    const opened = vi.fn();

    const view = render(<RunnersPanel addRequested onAddOpened={opened} />);
    expect(await screen.findByLabelText("Runner name")).toBeInTheDocument();
    expect(opened).toHaveBeenCalledTimes(1);

    // Drawn again with the request answered, it starts closed.
    view.unmount();
    render(<RunnersPanel addRequested={false} onAddOpened={opened} />);
    await screen.findByText(/No runners yet/);
    expect(screen.queryByLabelText("Runner name")).not.toBeInTheDocument();
  });

  it("leaves auto-accept and notifications to their own places", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });

    render(<RunnersPanel />);
    await screen.findByText("desktop");
    fireEvent.click(screen.getByRole("button", { name: "Details for desktop" }));
    expect(screen.queryByText("Auto-accept")).not.toBeInTheDocument();
    expect(screen.queryByText(/System notifications|toasts/)).not.toBeInTheDocument();
  });

  it("shows an empty state", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: null });
    render(<RunnersPanel />);
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

    render(<RunnersPanel />);
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
        <RunnersPanel />
        <ConfirmDialogHost />
      </>,
    );
    await screen.findByText("desktop");

    // Revoke waits inside the runner's details.
    expect(screen.queryByRole("button", { name: "Revoke runner desktop" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Details for desktop" }));
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

  it("leaves the budget to its own Settings part", async () => {
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [desktop] });
    const budget = vi.spyOn(api, "getRunBudget");
    useBudget.getState().reset();

    render(<RunnersPanel />);
    await screen.findByText("desktop");
    expect(screen.queryByText("Daily budget")).not.toBeInTheDocument();
    expect(screen.queryByText("Routing rules")).not.toBeInTheDocument();
    expect(budget).not.toHaveBeenCalled();
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
