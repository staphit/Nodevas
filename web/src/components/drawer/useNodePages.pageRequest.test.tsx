import { act, renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import { useNodeDetailView } from "./openNodeDetail";
import { useNodePages } from "./useNodePages";

// A search hit that only a subpage holds asks the node's panel for that page:
// the panel showing the node opens it, whether it was already open or mounts
// for the jump.

vi.mock("../../api", async (original) => {
  const real = await original<typeof import("../../api")>();
  return { ...real, api: { ...real.api, listNodePages: vi.fn(), getNodePage: vi.fn() } };
});

function usePages(nodeId: string) {
  return useNodePages({ nodeId, initialPageID: "main", editorMode: "live", setEditorMode: () => {} });
}

beforeEach(() => {
  vi.clearAllMocks();
  useApp.setState({ pageDocs: {}, tabs: [], activeProject: "p1" });
  useNodeDetailView.setState({ pageRequest: null });
  vi.mocked(api.listNodePages).mockResolvedValue({
    pages: [
      { id: "notes", title: "Notes" },
      { id: "cast", title: "Cast" },
    ],
  });
  vi.mocked(api.getNodePage).mockImplementation(async (nodeId, pageId) => ({
    nodeId, pageId, format: "md", content: `# ${pageId}`, rev: "r1",
  }));
});

it("opens the requested page once the pages are listed", async () => {
  act(() => useNodeDetailView.getState().requestPage("a", "cast"));
  const pages = renderHook(() => usePages("a"));
  await act(async () => {});
  expect(pages.result.current.activePageID).toBe("cast");
  expect(useNodeDetailView.getState().pageRequest).toBeNull();
});

it("opens the requested page on a panel that already shows the node", async () => {
  const pages = renderHook(() => usePages("a"));
  await act(async () => {});
  expect(pages.result.current.activePageID).toBeNull();
  await act(async () => useNodeDetailView.getState().requestPage("a", "notes"));
  expect(pages.result.current.activePageID).toBe("notes");
});

it("returns to the main page when the request names main", async () => {
  const pages = renderHook(() => usePages("a"));
  await act(async () => {});
  await act(async () => useNodeDetailView.getState().requestPage("a", "notes"));
  expect(pages.result.current.activePageID).toBe("notes");
  await act(async () => useNodeDetailView.getState().requestPage("a", "main"));
  expect(pages.result.current.activePageID).toBeNull();
  expect(pages.result.current.pageDoc).toBeNull();
  expect(useNodeDetailView.getState().pageRequest).toBeNull();
});

it("leaves a request for another node alone", async () => {
  act(() => useNodeDetailView.getState().requestPage("b", "cast"));
  const pages = renderHook(() => usePages("a"));
  await act(async () => {});
  expect(pages.result.current.activePageID).toBeNull();
  expect(useNodeDetailView.getState().pageRequest).toMatchObject({ nodeId: "b", pageId: "cast" });
});
