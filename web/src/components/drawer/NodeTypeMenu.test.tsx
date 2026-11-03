import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { NodeTypeMenu } from "./NodeTypeMenu";

function graph(kind: string): Graph {
  return { version: 1, nodes: [{ id: "a", title: "A", kind }], edges: [] } as Graph;
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
});

describe("NodeTypeMenu", () => {
  it("says why a legacy free-text kind behaves as Task", async () => {
    useApp.setState({ graph: graph("bug") });
    const user = userEvent.setup();
    render(<NodeTypeMenu id="a" />);
    await user.click(screen.getByRole("button"));
    expect(screen.getByText("「bug」不是這個專案的節點類型，會當成任務處理。")).toBeInTheDocument();
  });

  it("stays quiet for a known type", async () => {
    useApp.setState({ graph: graph("scene") });
    const user = userEvent.setup();
    render(<NodeTypeMenu id="a" />);
    await user.click(screen.getByRole("button"));
    expect(screen.queryByText(/不是這個專案的節點類型/)).toBeNull();
  });
});
