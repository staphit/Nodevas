import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { ProjectSection } from "./SettingsSections";

// This project's statuses, node types, office style, milestones and people
// live in graph.yaml. While that file cannot be read a change can only be
// refused, so the section says why instead of offering editors.

vi.mock("../components/SignIn", () => ({
  useViewer: () => null,
  useTeamServer: () => false,
  useSignOut: () => () => {},
}));

const HUB_PARSE_EN =
  "graph.yaml: line 2 has an error. Fix the file, then reload. (yaml: line 2: did not find expected ',' or ']')";

const sectionProps = {
  row: "nodeTypes" as const,
  toggleRow: () => undefined,
  go: () => undefined,
  leave: () => undefined,
};

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: null,
    graphUnreadable: HUB_PARSE_EN,
    activeProject: "Alpha",
  });
});

describe("This project while graph.yaml can't be read", () => {
  it("says why, with the line to fix, and offers no editor that would be refused", () => {
    render(<ProjectSection {...sectionProps} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(translate("settingsPage.graphUnreadable", "en"));
    expect(alert).toHaveTextContent("line 2 has an error. Fix the file, then reload.");
    expect(screen.queryByRole("button", { name: /Node types/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Lifecycle/ })).not.toBeInTheDocument();
  });

  it("keeps the editors for a graph that reads", () => {
    useApp.setState({ graphUnreadable: null, graph: { version: 1, nodes: [], edges: [], ui: {} } });
    render(<ProjectSection {...sectionProps} />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Node types/ })).toBeInTheDocument();
  });
});
