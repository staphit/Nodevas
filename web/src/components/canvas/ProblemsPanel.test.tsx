import { render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { analyzeGraph } from "../../analysis";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Issue } from "../../types";
import { ProblemsPanel, problemCount } from "./ProblemsPanel";

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
});
