import { readFileSync } from "node:fs";
import { join } from "node:path";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useExplorer } from "./explorerStore";
import FilesPanel from "./FilesPanel";
import { repo, workspaceListing } from "./testFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useExplorer.setState({ view: null });
  vi.spyOn(explorerApi, "workspaceFiles").mockResolvedValue(workspaceListing);
  vi.spyOn(explorerApi, "repoTree").mockResolvedValue({
    path: "",
    entries: [
      { name: "src", type: "dir", size: 0 },
      { name: "README.md", type: "file", size: 20 },
    ],
    hidden: 2,
    truncated: false,
  });
});
afterEach(() => vi.restoreAllMocks());

// W-10: workspace files and the runners' repos.
describe("FilesPanel", () => {
  it("shows the workspace tree, folding and filtering it", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([]);
    render(<FilesPanel />);
    const workspace = await screen.findByRole("region", { name: /^Workspace/ });
    expect(within(workspace).getByText("login.md")).toBeInTheDocument();
    await userEvent.click(within(workspace).getByRole("button", { name: /docs/ }));
    expect(within(workspace).queryByText("notes.txt")).not.toBeInTheDocument();
    await userEvent.click(within(workspace).getByRole("button", { name: /docs/ }));
    expect(within(workspace).getByText("notes.txt")).toBeInTheDocument();

    await userEvent.type(screen.getByRole("searchbox", { name: "Search file names…" }), "graph");
    expect(within(workspace).getByText("graph.yaml")).toBeInTheDocument();
    expect(within(workspace).queryByText("login.md")).not.toBeInTheDocument();

    await userEvent.clear(screen.getByRole("searchbox", { name: "Search file names…" }));
    await userEvent.click(within(workspace).getByText("login.md"));
    expect(useExplorer.getState().view).toEqual({ kind: "file", source: "workspace", path: "nodes/login.md" });
    expect(screen.getByText("No repos: add one on your runner with nodevas runner repo add.")).toBeInTheDocument();
  });

  // A .txt file is prose, not code: it showed the {} icon (UX sweep).
  it("gives plain text its own icon, apart from code, Markdown and images", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([]);
    render(<FilesPanel />);
    const workspace = await screen.findByRole("region", { name: /^Workspace/ });
    const icon = (name: string) =>
      within(workspace).getByText(name).closest("button")!.querySelector(".ex-ico")!.textContent;
    expect(icon("notes.txt")).toBe("≡");
    expect(icon("graph.yaml")).toBe("{}");
    expect(icon("login.md")).toBe("¶");
  });

  // At 150% text the names were cut to "node-00…", five rows that could not
  // be told apart, and neither they, their titles nor the section's name
  // ("Workspace · ai-harness-engineering") said the rest on hover.
  it("gives every name, title and section name its whole text on hover", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([repo()]);
    const { container } = render(<FilesPanel />);
    await screen.findByRole("region", { name: /^Workspace/ });
    await screen.findByText("README.md");
    const cut = container.querySelectorAll(".ex-name, .ex-meta, .ex-sec-title");
    expect(cut.length).toBeGreaterThan(5);
    for (const element of cut) expect(element).toHaveAttribute("title", element.textContent ?? "");
    expect(screen.getByText("Login form")).toHaveAttribute("title", "Login form");
  });

  // The name is what tells rows apart; the title beside it gives way first.
  it("shrinks a row's title before its file name", () => {
    const css = readFileSync(join(__dirname, "explorer.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    const shrink = (selector: string) => {
      const rules: string[] = [];
      const pattern = /([^{}]+)\{([^}]*)\}/g;
      for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
        if (match[1].split(",").some((part) => part.trim() === selector)) rules.push(match[2]);
      }
      const flex = /flex:\s*([\d.]+)(?:\s+([\d.]+))?/.exec(rules.join(";"));
      return flex ? Number(flex[2] ?? 1) : 0;
    };
    expect(shrink(".ex-name")).toBeGreaterThan(0);
    expect(shrink(".ex-meta")).toBeGreaterThan(shrink(".ex-name") * 10);
  });

  it("browses an online repo read-only and says what is hidden", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([repo()]);
    render(<FilesPanel />);
    const section = await screen.findByRole("region", { name: "Repo · visual-editor" });
    expect(await within(section).findByText("README.md")).toBeInTheDocument();
    expect(within(section).getByText("Read-only, served by the runner. 2 sensitive files hidden.")).toBeInTheDocument();
    expect(explorerApi.repoTree).toHaveBeenCalledWith("rn1", "r1", "");
  });

  it("explains an offline runner, a runner with browsing off, and a cut-short list", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([
      repo({ repoId: "r2", name: "game", online: false, runnerName: "laptop" }),
      repo({ repoId: "r3", name: "site", browsable: false, reason: "REPO_BROWSE_OFF" }),
    ]);
    vi.spyOn(explorerApi, "workspaceFiles").mockResolvedValue({ ...workspaceListing, truncated: true });
    render(<FilesPanel />);
    const offline = await screen.findByRole("region", { name: "Repo · game" });
    expect(within(offline).getByText("offline")).toBeInTheDocument();
    expect(
      within(offline).getByText("Runner “laptop” is offline, so this repo can't be browsed right now."),
    ).toBeInTheDocument();
    const off = screen.getByRole("region", { name: "Repo · site" });
    expect(
      within(off).getByText("Repo browsing is off on this runner (nodevas runner set repoBrowse on)."),
    ).toBeInTheDocument();
    expect(screen.getByText("Showing the first 5 entries.")).toBeInTheDocument();
    expect(explorerApi.repoTree).not.toHaveBeenCalled();
  });
});
