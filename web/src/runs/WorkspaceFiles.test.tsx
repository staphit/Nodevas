import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { ExplorerError, type RepoListing } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { makeView, makeWorkspaceRun } from "./workspaceFixtures";
import { WorkspaceFiles } from "./WorkspaceFiles";

const run = makeWorkspaceRun();

const listings: Record<string, RepoListing> = {
  "": {
    path: "",
    entries: [
      { name: "README.md", type: "file", size: 20 },
      { name: "home", type: "symlink", size: 0, escape: true },
      { name: "src", type: "dir", size: 0 },
    ],
    hidden: 2,
    truncated: false,
  },
  src: {
    path: "src",
    entries: [{ name: "main.go", type: "file", size: 40 }],
    hidden: 0,
    truncated: true,
  },
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});
afterEach(() => vi.restoreAllMocks());

describe("WorkspaceFiles", () => {
  it("lazy-loads folders by path, dims a link out of the worktree, reads a file with line numbers", async () => {
    const tree = vi.spyOn(api, "workspaceTree").mockImplementation(async (_id, path) => listings[path]);
    vi.spyOn(api, "workspaceFile").mockResolvedValue({
      path: "src/main.go",
      size: 40,
      binary: false,
      truncated: false,
      content: "package main\n\nfunc main() {}\n",
    });
    render(<WorkspaceFiles run={run} view={makeView()} />);

    const list = await screen.findByRole("navigation", { name: "Files in the worktree" });
    // Folders first.
    const rows = within(list).getAllByRole("button");
    expect(rows.map((row) => row.querySelector(".run-ws-name")?.textContent)).toEqual(["src", "home", "README.md"]);
    expect(rows[1]).toHaveTextContent("Points outside the repo");
    expect(within(list).getByRole("button", { name: /home/ })).toBeDisabled();
    expect(screen.getByText("Read-only, served by the runner. 2 sensitive files hidden.")).toBeInTheDocument();
    expect(tree).toHaveBeenCalledWith("run7", "");
    expect(tree).not.toHaveBeenCalledWith("run7", "src");

    fireEvent.click(within(list).getByRole("button", { name: /src/ }));
    expect(await within(list).findByRole("button", { name: /main\.go/ })).toBeInTheDocument();
    expect(tree).toHaveBeenCalledWith("run7", "src");
    expect(screen.getByText(/Showing the first 1/)).toBeInTheDocument();

    fireEvent.click(within(list).getByRole("button", { name: /main\.go/ }));
    const file = await screen.findByRole("region", { name: "src/main.go" });
    expect(await within(file).findByText("package main")).toBeInTheDocument();
    const lines = file.querySelectorAll(".ln");
    expect(lines).toHaveLength(3);
    expect(lines[2].querySelector(".no")?.textContent).toBe("3");
    expect(api.workspaceFile).toHaveBeenCalledWith("run7", "src/main.go");
  });

  it("names a pipe or socket the agent left but never reads it", async () => {
    vi.spyOn(api, "workspaceTree").mockResolvedValue({
      path: "",
      entries: [{ name: "notes.md", type: "other", size: 0 }],
      hidden: 0,
      truncated: false,
    });
    const read = vi.spyOn(api, "workspaceFile");
    render(<WorkspaceFiles run={run} view={makeView()} />);
    const list = await screen.findByRole("navigation", { name: "Files in the worktree" });
    const row = within(list).getByRole("button", { name: /notes\.md/ });
    expect(row).toBeDisabled();
    expect(row).toHaveAttribute("title", "Not a regular file (a pipe, socket or device); not read");
    fireEvent.click(row);
    expect(read).not.toHaveBeenCalled();
  });

  it("says when a file is binary or cut short", async () => {
    vi.spyOn(api, "workspaceTree").mockResolvedValue(listings[""]);
    const read = vi.spyOn(api, "workspaceFile").mockResolvedValue({
      path: "README.md",
      size: 20,
      binary: true,
      truncated: false,
      content: "",
    });
    render(<WorkspaceFiles run={run} view={makeView()} />);
    fireEvent.click(await screen.findByRole("button", { name: /README\.md/ }));
    expect(await screen.findByText(/binary/i)).toBeInTheDocument();

    read.mockResolvedValue({ path: "README.md", size: 900000, binary: false, truncated: true, content: "a\n" });
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByText(/first 512 KB/)).toBeInTheDocument();
  });

  it("refetches the live worktree on Refresh", async () => {
    const tree = vi.spyOn(api, "workspaceTree").mockResolvedValue(listings[""]);
    render(<WorkspaceFiles run={run} view={makeView()} />);
    await screen.findByRole("button", { name: /README\.md/ });
    const before = tree.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(tree.mock.calls.length).toBeGreaterThan(before));
  });

  it("lists uncommitted changes with their marks", async () => {
    vi.spyOn(api, "workspaceTree").mockResolvedValue(listings[""]);
    vi.spyOn(api, "workspaceStatus").mockResolvedValue({
      branch: "agent/alpha-run7abcd",
      head: "c0ffee1",
      entries: [{ path: "src/new.go", mark: "??" }],
      hidden: 0,
      truncated: false,
    });
    render(<WorkspaceFiles run={run} view={makeView()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Uncommitted changes" }));
    const status = await screen.findByRole("list", { name: "Uncommitted changes" });
    expect(within(status).getByText("??")).toBeInTheDocument();
    expect(within(status).getByText("src/new.go")).toBeInTheDocument();
  });

  it.each([
    ["NO_WORKTREE", 404, "This run has no worktree any more"],
    ["RUNNER_OFFLINE", 503, "desktop is offline"],
    ["RUNNER_OUTDATED", 412, "Update nodevas on desktop to browse run workspaces"],
  ] as const)("shows one note when the runner answers %s", async (code, status, text) => {
    vi.spyOn(api, "workspaceTree").mockRejectedValue(new ExplorerError(status, code, "refused"));
    render(<WorkspaceFiles run={run} view={makeView()} />);
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  });

  it("asks nothing of the runner when the hub says Files is off", async () => {
    const tree = vi.spyOn(api, "workspaceTree");
    render(<WorkspaceFiles run={run} view={makeView({ files: { allowed: false, blocked: "offline" } })} />);
    expect(screen.getByText("desktop is offline")).toBeInTheDocument();
    expect(tree).not.toHaveBeenCalled();
  });
});
