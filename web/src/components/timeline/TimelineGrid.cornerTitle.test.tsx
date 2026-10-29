/**
 * The horizontal timeline's "Nodes / dates" corner beside an open node drawer
 * at 150% text is 98px wide: its label wraps to a second line, and a third is
 * cut (timelineCornerLabelWrap.test.ts). Whatever is cut, hovering the corner
 * says the whole label.
 */
import { render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../../api";
import { translate } from "../../i18n";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { installPointerShims, manualFrames, resetBoardStore } from "../canvas/boardTestKit";
import { LaneView } from "../LaneView";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      putGraph: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      createNode: vi.fn(),
    },
  };
});

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null as string | null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore([]);
  manualFrames();
});

afterEach(() => {
  useApp.getState().updateUIPreference("timelineOrientation", "vertical");
});

function renderHorizontal() {
  const graph: Graph = {
    version: 1,
    nodes: [{ id: "a", title: "A" }],
    edges: [],
    ui: { timelineOrder: ["a"] },
  };
  useApp.setState({ graph });
  useApp.getState().updateUIPreference("timelineOrientation", "horizontal");
  return render(<LaneView variant="timeline" {...paneProps} />);
}

it.each(["en", "zh-TW"] as const)("names the whole corner label on hover (%s)", (language) => {
  useApp.getState().updateUIPreference("language", language);
  const { container } = renderHorizontal();
  const corner = container.querySelector<HTMLElement>(".horizontal-corner")!;
  const label = corner.querySelector<HTMLElement>(".horizontal-corner-label")!;
  const full = translate("timeline.corner", language);
  expect(label.textContent).toBe(full);
  // On the corner, so the whole 98px box answers the hover, not only the text.
  expect(corner.title).toBe(full);
});
