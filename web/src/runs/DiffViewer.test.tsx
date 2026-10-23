import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run, type RunChanges } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { DiffTable, INITIAL_LINES, RunDiffDialog, readDiffLayout } from "./DiffViewer";
import { NodeRuns } from "./NodeRuns";
import { parsePatch } from "./patch";
import { RunCardBadge, RunChangesSummary } from "./RunChanges";
import { runBadgeKind } from "./runFormat";
import { useRuns } from "./runStore";

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run1",
    project: "p",
    nodeId: "alpha",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "sonnet",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/alpha-run1",
    prompt: "",
    status: "succeeded",
    exitCode: 0,
    commit: "c0ffee",
    filesChanged: 5,
    additions: 120,
    deletions: 8,
    prUrl: "https://github.com/o/r/pull/42",
    prNumber: 42,
    prState: "open",
    ciStatus: "success",
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-24T08:00:00Z",
    finishedAt: "2026-10-24T08:10:00Z",
    ...overrides,
  };
}

const PATCH = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 package a
-var x = 1
+var x = 2
 // end
diff --git a/img.png b/img.png
new file mode 100644
index 0000000..3333333
Binary files /dev/null and b/img.png differ
diff --git a/old.go b/new.go
similarity index 95%
rename from old.go
rename to new.go
--- a/old.go
+++ b/new.go
@@ -1 +1 @@
-package old
\\ No newline at end of file
+package new
\\ No newline at end of file
`;

const CHANGES: RunChanges = {
  commit: "c0ffee",
  base: "main",
  files: [
    { path: "a.go", status: "modified", additions: 1, deletions: 1 },
    { path: "img.png", status: "added", additions: 0, deletions: 0, binary: true },
    { path: "new.go", status: "renamed", oldPath: "old.go", additions: 1, deletions: 1 },
  ],
  additions: 2,
  deletions: 2,
  patch: PATCH,
};

function setWide(wide: boolean) {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: wide && query.includes("min-width"),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }),
  });
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
  try {
    window.localStorage.clear();
  } catch {
    // ignore
  }
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("run changes summary", () => {
  it("shows line counts, files, a PR link with its state and the CI result", () => {
    render(<RunChangesSummary run={makeRun()} />);
    expect(screen.getByText("+120")).toBeInTheDocument();
    expect(screen.getByText("−8")).toBeInTheDocument();
    expect(screen.getByText("5 files")).toBeInTheDocument();
    const pr = screen.getByRole("link", { name: /PR #42/ });
    expect(pr).toHaveAttribute("href", "https://github.com/o/r/pull/42");
    expect(pr).toHaveAttribute("target", "_blank");
    expect(pr.getAttribute("rel")).toContain("noopener");
    expect(pr).toHaveClass("run-pr-open");
    expect(screen.getByRole("img", { name: "CI passed" })).toHaveTextContent("CI ✓");
  });

  it("styles merged and failing PRs and shows why no PR was opened", () => {
    const { rerender } = render(
      <RunChangesSummary run={makeRun({ prState: "merged", ciStatus: "failure" })} />,
    );
    expect(screen.getByRole("link", { name: /PR #42/ })).toHaveClass("run-pr-merged");
    expect(screen.getByRole("img", { name: "CI failed" })).toHaveTextContent("CI ✗");

    rerender(
      <RunChangesSummary
        run={makeRun({ prUrl: undefined, prNumber: undefined, prState: undefined, ciStatus: undefined, prError: "gh is not signed in" })}
      />,
    );
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByText("No PR: gh is not signed in")).toBeInTheDocument();
  });

  it("refuses a non-http PR link", () => {
    render(<RunChangesSummary run={makeRun({ prUrl: "javascript:alert(1)" })} />);
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("puts the summary on the node's run rows, outside the row button", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({
      runs: [makeRun(), makeRun({ id: "run0", status: "failed", prUrl: undefined, prError: "push refused", filesChanged: 1, additions: 3, deletions: 0 })],
    });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
    const getChanges = vi.spyOn(api, "getRunChanges").mockResolvedValue({ changes: CHANGES });

    render(<NodeRuns id="alpha" />);
    const list = await screen.findByRole("list", { name: "Runs" });
    const items = within(list).getAllByRole("listitem");
    expect(within(items[0]).getByText("#2")).toBeInTheDocument();
    const pr = within(items[0]).getByRole("link", { name: /PR #42/ });
    expect(pr.closest("button")).toBeNull();
    expect(within(items[0]).getByRole("img", { name: "CI passed" })).toBeInTheDocument();
    expect(within(items[1]).getByText("No PR: push refused")).toBeInTheDocument();
    expect(within(items[1]).getByText("1 file")).toBeInTheDocument();

    // The diff is fetched only when someone opens it.
    expect(getChanges).not.toHaveBeenCalled();
    setWide(true);
    fireEvent.click(screen.getByRole("button", { name: "View changes" }));
    const dialog = await screen.findByRole("dialog", { name: "Changes" });
    expect(getChanges).toHaveBeenCalledWith("run1");
    await within(dialog).findByRole("navigation", { name: "Changed files" });
  });
});

describe("RunDiffDialog", () => {
  it("lists files, shows hunks side by side on a wide screen and remembers unified", async () => {
    setWide(true);
    vi.spyOn(api, "getRunChanges").mockResolvedValue({ changes: CHANGES });
    render(<RunDiffDialog run={makeRun()} onClose={() => {}} />);

    const nav = await screen.findByRole("navigation", { name: "Changed files" });
    const files = within(nav).getAllByRole("button");
    expect(files.map((button) => button.textContent)).toEqual([
      expect.stringContaining("Ma.go"),
      expect.stringContaining("Aimg.png"),
      expect.stringContaining("Rnew.go"),
    ]);
    expect(files[0]).toHaveAttribute("aria-current", "true");

    const table = screen.getByRole("table", { name: "a.go" });
    expect(table).toHaveClass("is-split");
    expect(within(table).getByText("var x = 1").closest("td")).toHaveClass("is-del");
    expect(within(table).getByText("var x = 2").closest("td")).toHaveClass("is-add");

    fireEvent.click(screen.getByRole("button", { name: "Unified" }));
    expect(screen.getByRole("table", { name: "a.go" })).toHaveClass("is-unified");
    expect(readDiffLayout()).toBe("unified");

    fireEvent.click(files[1]);
    expect(screen.getByText("Binary files differ")).toBeInTheDocument();

    fireEvent.click(files[2]);
    expect(screen.getByText("renamed from old.go")).toBeInTheDocument();
    expect(screen.getAllByText(/No newline at end of file/)).toHaveLength(2);
  });

  it("is unified with a back button on a phone, with no layout toggle", async () => {
    setWide(false);
    vi.spyOn(api, "getRunChanges").mockResolvedValue({ changes: CHANGES });
    render(<RunDiffDialog run={makeRun()} onClose={() => {}} />);

    const nav = await screen.findByRole("navigation", { name: "Changed files" });
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("button", { name: "Side by side" })).toBeNull();
    fireEvent.click(within(nav).getAllByRole("button")[0]);
    expect(screen.getByRole("table", { name: "a.go" })).toHaveClass("is-unified");
    fireEvent.click(screen.getByRole("button", { name: /All files/ }));
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("points at the PR when the patch was too large", async () => {
    setWide(true);
    vi.spyOn(api, "getRunChanges").mockResolvedValue({
      changes: { ...CHANGES, patch: undefined, patchTruncated: true },
    });
    render(<RunDiffDialog run={makeRun()} onClose={() => {}} />);
    expect(await screen.findByText(/Diff too large to show; open the PR/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open PR #42" })).toHaveAttribute(
      "href",
      "https://github.com/o/r/pull/42",
    );
  });

  it("closes on Escape without reaching the drawer", async () => {
    setWide(true);
    vi.spyOn(api, "getRunChanges").mockResolvedValue({ changes: null });
    const onClose = vi.fn();
    const drawerEscape = vi.fn();
    window.addEventListener("keydown", drawerEscape);
    render(<RunDiffDialog run={makeRun()} onClose={onClose} />);
    expect(await screen.findByText("No changes reported for this run yet.")).toBeInTheDocument();
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
    expect(drawerEscape).not.toHaveBeenCalled();
    window.removeEventListener("keydown", drawerEscape);
  });
});

describe("DiffTable", () => {
  it("caps rendered lines and shows more on request", () => {
    const body = Array.from({ length: 1000 }, (_, i) => `+line ${i}`).join("\n");
    const [file] = parsePatch(
      `diff --git a/big.txt b/big.txt\nnew file mode 100644\n--- /dev/null\n+++ b/big.txt\n@@ -0,0 +1,1000 @@\n${body}\n`,
    );
    const { container } = render(<DiffTable file={file} layout="unified" />);
    // The hunk header row counts toward the budget.
    expect(container.querySelectorAll("tr.run-diff-line")).toHaveLength(INITIAL_LINES - 1);
    fireEvent.click(screen.getByRole("button", { name: "Show 601 more lines" }));
    expect(container.querySelectorAll("tr.run-diff-line")).toHaveLength(1000);
    expect(screen.queryByRole("button", { name: /more lines/ })).toBeNull();
  });
});

describe("graph card badge", () => {
  it("picks the run's live state first, then its PR", () => {
    expect(runBadgeKind(makeRun({ status: "running" }))).toBe("running");
    expect(runBadgeKind(makeRun({ status: "offered" }))).toBe("waiting");
    expect(runBadgeKind(makeRun({ ciStatus: "failure" }))).toBe("ci-failure");
    expect(runBadgeKind(makeRun({ prState: "merged" }))).toBe("pr-merged");
    expect(runBadgeKind(makeRun())).toBe("pr-open");
    expect(runBadgeKind(makeRun({ prUrl: undefined }))).toBe("succeeded");
    expect(runBadgeKind(makeRun({ prUrl: undefined, status: "failed" }))).toBe("failed");
    expect(runBadgeKind(makeRun({ prUrl: undefined, status: "cancelled" }))).toBeNull();
  });

  it("renders from the store without fetching", () => {
    const list = vi.spyOn(api, "listRuns");
    const { container, rerender } = render(<RunCardBadge nodeId="alpha" />);
    expect(container).toBeEmptyDOMElement();
    useRuns.setState({ latestByNode: { alpha: makeRun({ ciStatus: "failure" }) } });
    rerender(<RunCardBadge nodeId="alpha" />);
    const badge = container.querySelector(".col-card-run")!;
    expect(badge).toHaveClass("col-card-run-ci-failure");
    expect(badge).toHaveTextContent("PR #42 · CI ✗");
    expect(badge).toHaveAttribute("title", "CI failed on PR #42 (claude · sonnet)");
    expect(list).not.toHaveBeenCalled();
  });
});
