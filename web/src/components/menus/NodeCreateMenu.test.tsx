import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import {
  configurePreferenceStorage,
  readPreferences,
  type PreferenceStorage,
} from "../../preferences";
import { useApp } from "../../store";
import type { Graph, NodeKind, RunState, Status, StatusDefinition } from "../../types";
import { useNodeCreation } from "../canvas/useNodeCreation";
import type { LaneContextMenu } from "../LaneView";
import { NodeCreateMenu } from "./NodeCreateMenu";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      createNode: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      putGraph: vi.fn(),
      setStatus: vi.fn(),
      getNodeFolders: vi.fn(),
      getTrash: vi.fn(),
    },
  };
});

const CUSTOM_STATUS: StatusDefinition = {
  id: "custom-status-review",
  label: "外審中",
  color: "#8fd3ff",
  shape: "diamond",
};

const EMPTY_RUN: RunState = { nodes: {}, history: [] };

const MENU: Extract<LaneContextMenu, { kind: "node-create" }> = {
  kind: "node-create",
  x: 40,
  y: 60,
  col: 3,
  row: 5,
};

/**
 * A stand-in for the server, not just for the client call. What the form
 * chooses only counts if it survives the round trip, so every write lands in
 * `served` and the assertions read the node back out of it rather than out of
 * the optimistic store state, which would pass even if nothing was ever sent.
 */
let served: { graph: Graph; statuses: Record<string, Status> };

function memoryPreferences(): PreferenceStorage {
  const map = new Map<string, string>();
  return {
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  };
}

let preferenceStorage: PreferenceStorage;

beforeEach(() => {
  served = {
    graph: {
      version: 1,
      nodes: [],
      edges: [],
      ui: { customStatuses: [CUSTOM_STATUS] },
    },
    statuses: {},
  };

  vi.mocked(api.createNode).mockImplementation(async (node) => {
    const id = node.id ?? "n1";
    served.graph.nodes = [
      ...(served.graph.nodes ?? []),
      { id, title: node.title ?? "", kind: node.kind as NodeKind },
    ];
    return { ok: true, id };
  });
  vi.mocked(api.getGraph).mockImplementation(async () => ({
    graph: structuredClone(served.graph),
    rev: "rev-1",
    statuses: { ...served.statuses },
    issues: null,
  }));
  vi.mocked(api.putGraph).mockImplementation(async (graph) => {
    served.graph = structuredClone(graph);
    return { ok: true, rev: "rev-2", issues: null };
  });
  vi.mocked(api.getState).mockImplementation(async () => ({
    state: EMPTY_RUN,
    statuses: { ...served.statuses },
  }));
  vi.mocked(api.setStatus).mockImplementation(async (id, status) => {
    served.statuses[id] = status;
    return { ok: true, state: EMPTY_RUN, statuses: { ...served.statuses } };
  });
  vi.mocked(api.getNodeFolders).mockResolvedValue({ folders: [], nodes: {} });

  preferenceStorage = memoryPreferences();
  configurePreferenceStorage(preferenceStorage);

  useApp.setState({
    graph: structuredClone(served.graph),
    graphRev: "rev-1",
    runState: EMPTY_RUN,
    statuses: {},
    issues: [],
    operations: {},
    error: null,
    tabs: [],
    activeTab: null,
    preferences: { ...readPreferences(), language: "zh-TW" },
  });
  useApp.getState().updateUIPreference("language", "zh-TW");
});

afterEach(() => {
  configurePreferenceStorage(null);
  vi.unstubAllGlobals();
});

/**
 * The form as the board mounts it: the real hook against the real store, inside
 * the same scrolling panel the context menu layer provides, so nothing about
 * how a choice reaches the server is faked away.
 */
function CreateForm() {
  const graph = useApp((state) => state.graph);
  const createNode = useApp((state) => state.createNode);
  const setLifecycleStatus = useApp((state) => state.setLifecycleStatus);
  const planStatusDefinitions = graph?.ui?.planStatuses ?? [];
  const creation = useNodeCreation({
    planStatusDefinitions,
    createNode,
    setLifecycleStatus,
    setGraphNotice: () => undefined,
    setContextMenu: () => undefined,
  });
  return (
    <div className="lane-context-menu node-create-menu">
      <NodeCreateMenu
        {...creation}
        contextMenu={MENU}
        planStatusDefinitions={planStatusDefinitions}
        customStatuses={graph?.ui?.customStatuses ?? []}
        setContextMenu={() => undefined}
        dragHandleProps={{ onPointerDown: () => undefined }}
      />
    </div>
  );
}

/** The created node's card style, as the server ended up holding it. */
function servedStyle(id = "n1") {
  return served.graph.ui?.nodeStyles?.[id];
}

async function openAdvanced(user: ReturnType<typeof userEvent.setup>) {
  const toggle = screen.getByRole("button", { name: /外觀與狀態/ });
  if (toggle.getAttribute("aria-expanded") === "false") await user.click(toggle);
  return toggle;
}

describe("NodeCreateMenu", () => {
  // The whole point of the fold: the common case is a title and a keystroke,
  // and it must not have grown a single extra step.
  it("creates a node from a title and Enter alone, with no appearance chosen", async () => {
    const user = userEvent.setup();
    render(<CreateForm />);

    await user.type(screen.getByLabelText("節點標題"), "第一章{Enter}");

    await waitFor(() => expect(api.createNode).toHaveBeenCalledTimes(1));
    expect(api.createNode).toHaveBeenCalledWith(
      expect.objectContaining({ title: "第一章" }),
    );
    // No style key at all, rather than one full of defaults: an untouched node
    // must keep following the theme when the theme changes.
    await waitFor(() => expect(served.graph.ui?.positions?.n1).toEqual({ x: 3, y: 5 }));
    expect(servedStyle()).toBeUndefined();
    expect(api.setStatus).not.toHaveBeenCalled();
  });

  // Folded is only acceptable if it is also closed; an accordion that starts
  // open is just the long form with an extra click available.
  it("keeps the advanced section folded until it is asked for", () => {
    render(<CreateForm />);

    const toggle = screen.getByRole("button", { name: /外觀與狀態/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    // `hidden`, not merely off-screen, so the controls are out of the tab order
    // and Tab from the title still reaches the create button.
    expect(screen.queryByRole("combobox", { name: "實際狀態" })).toBeNull();
    expect(
      screen.getByRole("combobox", { name: "實際狀態", hidden: true }),
    ).not.toBeVisible();
  });

  // Statuses are a workspace-wide list. A picker that only knew the seven
  // built-ins would quietly be missing whatever this project actually uses.
  it("offers the workspace's custom statuses alongside the built-in ones", async () => {
    const user = userEvent.setup();
    render(<CreateForm />);
    await openAdvanced(user);

    const select = screen.getByRole("combobox", { name: "實際狀態" });
    expect(within(select).getByRole("option", { name: "進行中" })).toBeInTheDocument();
    expect(within(select).getByRole("option", { name: "外審中" })).toBeInTheDocument();
  });

  it("writes the chosen status to the node it just created", async () => {
    const user = userEvent.setup();
    render(<CreateForm />);
    await openAdvanced(user);

    await user.selectOptions(
      screen.getByRole("combobox", { name: "實際狀態" }),
      "custom-status-review",
    );
    await user.type(screen.getByLabelText("節點標題"), "第二章");
    await user.click(screen.getByRole("button", { name: "建立節點" }));

    // Read back from the server's copy: the status lives in the journal, not in
    // graph.yaml, so it can only have got there by the second call.
    await waitFor(() => expect(served.statuses.n1).toBe("custom-status-review"));
    expect(api.setStatus).toHaveBeenCalledWith("n1", "custom-status-review", "建立時設定");
  });

  it("writes the chosen appearance to the node it just created", async () => {
    const user = userEvent.setup();
    render(<CreateForm />);
    await openAdvanced(user);

    await user.click(screen.getByRole("button", { name: "六角形" }));
    await user.click(screen.getByRole("button", { name: "底色 #2b3a2a" }));
    await user.click(screen.getByRole("button", { name: "寬版 240×68" }));
    await user.type(screen.getByLabelText("節點標題"), "第三章");
    await user.click(screen.getByRole("button", { name: "建立節點" }));

    await waitFor(() => expect(servedStyle()).toBeDefined());
    expect(servedStyle()).toMatchObject({
      shape: "hexagon",
      color: "#2b3a2a",
      width: 240,
      height: 68,
    });
  });

  // A second node should not silently inherit the first one's colours: the
  // form is reused in place, so the draft has to be cleared with it.
  it("starts the next node from the defaults again", async () => {
    const user = userEvent.setup();
    render(<CreateForm />);
    await openAdvanced(user);

    await user.click(screen.getByRole("button", { name: "膠囊" }));
    await user.type(screen.getByLabelText("節點標題"), "第四章");
    await user.click(screen.getByRole("button", { name: "建立節點" }));
    await waitFor(() => expect(servedStyle()?.shape).toBe("pill"));

    // Waited for, not asserted once. The draft is cleared after createNode
    // resolves, so the write landing in `served` — which is what the line
    // above waits for — happens strictly before the reset renders. Reading the
    // button immediately is a race the test loses whenever that render has not
    // flushed yet.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "矩形" })).toHaveAttribute(
        "aria-pressed",
        "true",
      ),
    );
  });

  // Someone who sets looks on one node usually wants to on the next, and the
  // fold is a preference like every other panel state — not per-dialog state
  // that resets the moment the menu closes.
  it("remembers that the advanced section was left open", async () => {
    const user = userEvent.setup();
    const first = render(<CreateForm />);
    await openAdvanced(user);

    expect(preferenceStorage.getItem("vised-node-create-advanced")).toBe("1");

    // Remount from storage the way a fresh window would, rather than trusting
    // the store copy that is already in memory.
    first.unmount();
    useApp.setState({ preferences: readPreferences() });
    render(<CreateForm />);

    expect(screen.getByRole("button", { name: /外觀與狀態/ })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getByRole("combobox", { name: "實際狀態" })).toBeVisible();
  });

  // A 390px phone has no second column to put anything in, so the extra
  // controls have to live inside the one scrolling panel the menu already is —
  // not in a popover of their own that would open off the side of the screen.
  it("keeps every control inside the one scrollable panel at phone width", async () => {
    vi.stubGlobal("innerWidth", 390);
    vi.stubGlobal("innerHeight", 664);
    const user = userEvent.setup();
    const { container } = render(<CreateForm />);

    const panel = container.querySelector(".node-create-menu");
    expect(panel).not.toBeNull();
    // Folded, the phone sees four things: the title, the dates, the fold and
    // the two buttons. Nothing has to be scrolled past to reach 建立節點.
    expect(panel).toContainElement(screen.getByLabelText("節點標題"));
    expect(panel).toContainElement(screen.getByRole("button", { name: "建立節點" }));

    await openAdvanced(user);

    for (const name of ["實際狀態"]) {
      expect(panel).toContainElement(screen.getByRole("combobox", { name }));
    }
    for (const name of ["六角形", "重設為預設外觀", "建立節點"]) {
      expect(panel).toContainElement(screen.getByRole("button", { name }));
    }
  });
});
