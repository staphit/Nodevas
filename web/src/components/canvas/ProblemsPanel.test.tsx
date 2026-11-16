import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { analyzeGraph, type GraphAnalysis } from "../../analysis";
import { translate } from "../../i18n";
import { localizeIssue } from "../../issueMessages";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Issue } from "../../types";
import { ProblemsPanel, hasUrgentProblem, problemCount } from "./ProblemsPanel";

const empty = analyzeGraph(null, {});

const hint: Issue = {
  severity: "info",
  nodeId: "a",
  field: "kind",
  msg: '3 node(s) use types this project does not define ("bug" ×3); they behave as Task.',
};
const warning: Issue = { severity: "warning", nodeId: "b", msg: "unknown flag" };

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
});

// Two new cards nobody had connected yet turned the badge failure-red. Only
// something actually wrong earns that colour; warnings alone are counted in a
// calmer one.
describe("hasUrgentProblem", () => {
  it("is false for warnings and hints alone", () => {
    expect(hasUrgentProblem(empty, [])).toBe(false);
    expect(hasUrgentProblem(empty, [hint, warning])).toBe(false);
  });

  it("is true for an error, an overdue node or a broken dependency", () => {
    expect(hasUrgentProblem(empty, [warning, { severity: "error", msg: "cycle" }])).toBe(true);
    const overdue: GraphAnalysis = {
      ...empty,
      overdue: new Map([["a", [{ kind: "deadline", date: "2026-01-01" }]]]),
    };
    expect(hasUrgentProblem(overdue, [])).toBe(true);
    const broken: GraphAnalysis = {
      ...empty,
      violations: [{ nodeId: "a", sourceIds: ["b"], kind: "actual", reason: { kind: "requires-false" } }],
    };
    expect(hasUrgentProblem(broken, [])).toBe(true);
  });
});

// "Not connected to anything" is not put right in the node's document, which
// is where picking it used to land. The row says where it goes instead.
describe("ProblemsPanel's not-connected row", () => {
  const disconnected: Issue = {
    severity: "warning",
    nodeId: "b",
    msg: "node is not connected to anything",
  };

  function panel(onPick = vi.fn()) {
    render(
      <ProblemsPanel
        analysis={empty}
        issues={[localizeIssue(disconnected, "zh-TW"), localizeIssue(warning, "zh-TW")]}
        nodeTitle={(id) => id ?? ""}
        onPick={onPick}
        onClose={() => undefined}
      />,
    );
    return onPick;
  }

  it("opens the node on its Dependencies, and says so on the row", async () => {
    const user = userEvent.setup();
    const onPick = panel();
    const label = translate("board.problemsConnect", "zh-TW");
    const row = screen.getByRole("button", { name: new RegExp(label) });
    await user.click(row);
    expect(onPick).toHaveBeenCalledWith("b", "dependencies");
  });

  it("leaves every other row opening the node as before", async () => {
    const user = userEvent.setup();
    const onPick = panel();
    const label = translate("board.problemsConnect", "zh-TW");
    const other = screen.getByRole("button", { name: /unknown flag/ });
    expect(other).not.toHaveTextContent(label);
    await user.click(other);
    expect(onPick).toHaveBeenCalledWith("b", undefined);
  });
});

describe("ProblemsPanel", () => {
  it("never counts a hint as a problem", () => {
    expect(problemCount(empty, [hint])).toBe(0);
    expect(problemCount(empty, [hint, warning])).toBe(1);
  });

  it("lists hints below, even when there is nothing to fix", () => {
    const onPick = vi.fn();
    render(
      <ProblemsPanel
        analysis={empty}
        issues={[hint]}
        nodeTitle={(id) => id ?? ""}
        onPick={onPick}
        onClose={() => undefined}
      />,
    );
    expect(screen.getByText("目前沒有需要處理的問題。")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "驗證警告" })).toBeNull();
    const hints = screen.getByRole("region", { name: "提示" });
    within(hints).getByRole("button").click();
    expect(onPick).toHaveBeenCalledWith("a");
    expect(screen.getByRole("heading", { level: 3 })).toHaveTextContent("0");
  });

  // An issue about the graph as a whole has no node to name, and its row read
  // a bare English "graph" in either language.
  it("names a graph-wide issue in the reader's language", () => {
    const graphWide: Issue = { severity: "error", field: "edges", msg: "cycle" };
    const panel = () =>
      render(
        <ProblemsPanel
          analysis={empty}
          issues={[graphWide]}
          nodeTitle={(id) => id ?? ""}
          onPick={() => undefined}
          onClose={() => undefined}
        />,
      );
    const first = panel();
    const warnings = screen.getByRole("region", { name: "驗證警告" });
    expect(within(warnings).getByText("整張關係圖")).toBeInTheDocument();
    expect(within(warnings).queryByText("graph")).toBeNull();
    first.unmount();

    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    panel();
    const english = screen.getByRole("region", { name: "Validation warnings" });
    expect(within(english).getByText("Whole graph")).toBeInTheDocument();
  });

  // A row named the part of the node at fault by the file's own key —
  // "· requires", "· edges" — in English whatever the language.
  it("names the part at fault in the reader's language", () => {
    const issues: Issue[] = [
      { severity: "error", nodeId: "a", field: "requires", msg: "未知的節點" },
      { severity: "error", nodeId: "b", field: "edges", msg: "循環依賴" },
      { severity: "error", nodeId: "c", field: "effects[2]", msg: "壞掉的效果" },
      { severity: "warning", nodeId: "d", field: "ui.plans", msg: "日期順序" },
    ];
    const panel = () =>
      render(
        <ProblemsPanel
          analysis={empty}
          issues={issues}
          nodeTitle={(id) => id ?? ""}
          onPick={() => undefined}
          onClose={() => undefined}
        />,
      );
    const rowText = (region: HTMLElement) =>
      within(region)
        .getAllByRole("button")
        .map((row) => row.textContent ?? "");

    const first = panel();
    const rows = rowText(screen.getByRole("region", { name: "驗證警告" }));
    expect(rows[0]).toContain("· 前置條件");
    expect(rows[1]).toContain("· 相依關係");
    expect(rows[2]).toContain("· 效果");
    expect(rows[3]).toContain("· 計畫");
    for (const raw of ["requires", "edges", "effects", "ui.plans"]) {
      expect(rows.join(" ")).not.toContain(raw);
    }
    first.unmount();

    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    panel();
    const english = rowText(screen.getByRole("region", { name: "Validation warnings" }));
    expect(english[0]).toContain("· Prerequisites");
    expect(english[1]).toContain("· Dependencies");
  });

  // The node's name was display:block, so " · 前置條件" wrapped onto a line
  // of its own that began with the dot. The name and the part at fault are
  // one heading line; jsdom applies no CSS, so the stylesheet is read too.
  it("keeps the part at fault on the node's line, not a new line starting with ·", () => {
    render(
      <ProblemsPanel
        analysis={empty}
        issues={[{ severity: "error", nodeId: "b", field: "requires", msg: "未知的節點" }]}
        nodeTitle={() => "Bravo"}
        onPick={() => undefined}
        onClose={() => undefined}
      />,
    );
    const name = screen.getByText("Bravo");
    const heading = name.parentElement!;
    expect(heading).toHaveClass("problems-issue-title");
    expect(heading.textContent?.replace(/ /g, " ")).toBe("Bravo · 前置條件");

    const css = readFileSync(resolve(process.cwd(), "src/styles/10a-canvas-tools.css"), "utf8").replace(
      /\/\*[\s\S]*?\*\//g,
      "",
    );
    const blocks = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)];
    const rulesFor = (selector: string) =>
      blocks
        .filter(([, head]) => head.split(",").some((part) => part.trim() === selector))
        .map(([, , body]) => body)
        .join(";");
    expect(rulesFor(".problems-issue b")).not.toMatch(/display:\s*block/);
    expect(rulesFor(".problems-issue-title")).toMatch(/display:\s*block/);
  });

  // One block was not enough: a name that ends near the panel's edge still
  // broke at the space before the dot (a 29-character name at 320px), and
  // the field's line began "· 前置條件". The dot holds on to the name.
  it("never starts a line with the dot when a long name wraps", () => {
    render(
      <ProblemsPanel
        analysis={empty}
        issues={[{ severity: "error", nodeId: "b", field: "requires", msg: "未知的節點" }]}
        nodeTitle={() => "Focus block architecture revi"}
        onPick={() => undefined}
        onClose={() => undefined}
      />,
    );
    const heading = screen.getByText("Focus block architecture revi").parentElement!;
    const text = heading.textContent ?? "";
    const dot = text.indexOf("·");
    expect(dot).toBeGreaterThan(0);
    // No break opportunity right before the dot.
    expect(text[dot - 1]).toBe(" ");
  });

  it("says one day, not one days, for a one-day critical path", () => {
    const panel = (criticalDays: number) =>
      render(
        <ProblemsPanel
          analysis={{ ...empty, criticalDays }}
          issues={[]}
          nodeTitle={(id) => id ?? ""}
          onPick={() => undefined}
          onClose={() => undefined}
        />,
      );
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const one = panel(1);
    expect(screen.getByText("Critical path: 1 day")).toBeInTheDocument();
    one.unmount();
    const two = panel(2);
    expect(screen.getByText("Critical path: 2 days")).toBeInTheDocument();
    two.unmount();

    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    panel(1);
    expect(screen.getByText("關鍵路徑：1 天")).toBeInTheDocument();
  });
});
