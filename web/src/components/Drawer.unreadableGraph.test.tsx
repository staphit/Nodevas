import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Tab } from "../state/types";
import type { Graph } from "../types";
import { Drawer } from "./Drawer";
import { useNodeDetailView } from "./drawer/openNodeDetail";

// While graph.yaml cannot be read the hub refuses every write to a node's
// title, type, dependencies and references. The panel says it is read-only;
// its controls have to agree, or an edit bounces with the same refusal in
// three places and the title keeps text the file never took.

const UNREADABLE = "graph.yaml: line 41 has an error. Fix the file, then reload.";

const loaded: Tab = {
  id: "sync",
  pinned: false,
  content: "# Team sync\n",
  rev: "r1",
  dirty: false,
  loaded: true,
  diskChanged: false,
  draftPending: null,
  conflict: null,
};

let updateNodeMetadata: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "getNodeLinks").mockResolvedValue({ backlinks: [], outgoing: [] });
  vi.spyOn(api, "listNodePages").mockResolvedValue({ pages: [] } as never);
  vi.spyOn(api, "getHistory").mockResolvedValue({ versions: [] } as never);
  updateNodeMetadata = vi.fn(async () => ({ ok: true as const }));
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "daily",
    graph: {
      version: 1,
      nodes: [
        { id: "inbox", title: "Inbox triage" },
        { id: "sync", title: "Team sync", refs: ["notes"] },
        { id: "notes", title: "Notes", kind: "note" },
        { id: "spare", title: "Spare" },
      ],
      edges: [{ from: "inbox", to: "sync" }],
      ui: {},
    } as unknown as Graph,
    graphUnreadable: UNREADABLE,
    runState: { nodes: {}, history: [] },
    statuses: { sync: "ready" },
    stagedLifecycle: {},
    operations: {},
    tabs: [loaded],
    activeTab: "sync",
    requestLock: vi.fn(),
    releaseLock: vi.fn(),
    updateNodeMetadata: updateNodeMetadata as never,
  });
});

const titleField = () => screen.getByRole("textbox", { name: "Title" }) as HTMLInputElement;
const typeButton = () => screen.getByRole("button", { name: /^Type: / });

describe("the node panel while graph.yaml cannot be read", () => {
  it("does not let the title or the type be edited", async () => {
    render(<Drawer />);
    expect(screen.getByText(/^Read-only: /)).toBeInTheDocument();
    expect(titleField().readOnly).toBe(true);
    expect(typeButton()).toBeDisabled();

    fireEvent.change(titleField(), { target: { value: "Team sync X" } });
    fireEvent.blur(titleField());
    expect(updateNodeMetadata).not.toHaveBeenCalled();
  });

  it("offers no dependency or reference edits", async () => {
    render(<Drawer />);
    act(() => useNodeDetailView.getState().requestView("sync", "dependencies"));
    expect(screen.getByRole("button", { name: "Remove dependency on Inbox triage" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Choose a prerequisite" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Remove reference to Notes" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add reference" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Choose a reference" })).toBeDisabled();
    // Reading still works: a prerequisite still opens.
    expect(screen.getByRole("button", { name: "Inbox triage" })).toBeEnabled();
  });

  it("does not promise an auto-save that cannot happen", () => {
    const { container } = render(<Drawer />);
    expect(container.querySelector(".tab-footer-hint")).toBeNull();
  });

  it("gives every control back once the file reads again", async () => {
    render(<Drawer />);
    act(() => useApp.setState({ graphUnreadable: null }));
    expect(titleField().readOnly).toBe(false);
    expect(typeButton()).toBeEnabled();
    act(() => useNodeDetailView.getState().requestView("sync", "dependencies"));
    expect(screen.getByRole("button", { name: "Remove dependency on Inbox triage" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Add reference" })).toBeEnabled();
  });
});

describe("a title the hub refuses", () => {
  it("goes back to the title the file holds", async () => {
    useApp.setState({ graphUnreadable: null, error: null });
    updateNodeMetadata.mockResolvedValue({ ok: false, message: "refused" });
    const user = userEvent.setup();
    render(<Drawer />);
    await user.click(titleField());
    await user.type(titleField(), " X{Enter}");
    expect(updateNodeMetadata).toHaveBeenCalledWith("sync", { title: "Team sync X" });
    await waitFor(() => expect(titleField().value).toBe("Team sync"));
  });
});
