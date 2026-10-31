import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import { useNodePages } from "./useNodePages";

// The subpage tabs are read once when the drawer opens. A page added from the
// pop-out, the main window or another person reaches this window only as the
// node's `node-changed`; without re-reading the list on it, each window kept
// the tabs it opened with until the drawer was closed and reopened.

vi.mock("../../api", async (original) => {
  const real = await original<typeof import("../../api")>();
  return { ...real, api: { ...real.api, listNodePages: vi.fn(), createNodePage: vi.fn() } };
});

const notes = { id: "notes", title: "Notes" };
const world = { id: "world", title: "World" };

function useEditor(nodeId = "a") {
  return useNodePages({ nodeId, initialPageID: "main", editorMode: "live", setEditorMode: () => {} });
}

beforeEach(() => {
  vi.clearAllMocks();
  useApp.setState({ pageDocs: {}, tabs: [], activeProject: "p1" });
  vi.mocked(api.listNodePages).mockResolvedValue({ pages: [notes] });
});

it("re-reads the subpage list when another window changes the node", async () => {
  const editor = renderHook(() => useEditor());
  await waitFor(() => expect(editor.result.current.pages).toEqual([notes]));
  vi.mocked(api.listNodePages).mockResolvedValue({ pages: [notes, world] });
  await act(async () => { await useApp.getState().notifyDiskChange("a"); });
  await waitFor(() => expect(editor.result.current.pages).toEqual([notes, world]));
  expect(editor.result.current.pagesLoading).toBe(false);
  editor.unmount();
});

it("leaves the list alone when a different node changes", async () => {
  const editor = renderHook(() => useEditor());
  await waitFor(() => expect(editor.result.current.pages).toEqual([notes]));
  await act(async () => { await useApp.getState().notifyDiskChange("b"); });
  expect(api.listNodePages).toHaveBeenCalledTimes(1);
  editor.unmount();
});

it("re-reads the list when the window comes back into focus", async () => {
  const editor = renderHook(() => useEditor());
  await waitFor(() => expect(editor.result.current.pages).toEqual([notes]));
  vi.mocked(api.listNodePages).mockResolvedValue({ pages: [notes, world] });
  act(() => { window.dispatchEvent(new Event("focus")); });
  await waitFor(() => expect(editor.result.current.pages).toEqual([notes, world]));
  editor.unmount();
});

it("does not let a read that started before a local create drop the new page", async () => {
  const editor = renderHook(() => useEditor());
  await waitFor(() => expect(editor.result.current.pages).toEqual([notes]));
  let answerStale: ((value: { pages: (typeof notes)[] }) => void) | undefined;
  vi.mocked(api.listNodePages).mockImplementationOnce(
    () => new Promise((resolve) => { answerStale = resolve; }),
  );
  act(() => { window.dispatchEvent(new Event("focus")); });
  vi.mocked(api.createNodePage).mockResolvedValue({
    page: world, content: "", rev: "r1",
  } as Awaited<ReturnType<typeof api.createNodePage>>);
  act(() => editor.result.current.setPageTitle("World"));
  await act(async () => { await editor.result.current.createPage(); });
  await act(async () => { answerStale?.({ pages: [notes] }); });
  expect(editor.result.current.pages).toEqual([notes, world]);
  editor.unmount();
});
