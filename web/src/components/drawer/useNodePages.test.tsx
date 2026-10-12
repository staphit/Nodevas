import { act, renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import { drainWrites } from "../../state/internals";
import { useNodePages } from "./useNodePages";
import { useAutosave } from "./useAutosave";

vi.mock("../../api", async (original) => {
  const real = await original<typeof import("../../api")>();
  return { ...real, api: { ...real.api, listNodePages: vi.fn(), putNodePage: vi.fn() } };
});

function useEditor() {
  const pages = useNodePages({ nodeId: "a", initialPageID: "main", editorMode: "live", setEditorMode: () => {} });
  useAutosave({
    save: async () => { await pages.saveSubpage(); },
    dirty: pages.pageDoc?.dirty ?? false, blocked: false,
    content: pages.pageDoc?.content ?? "", documentKey: "a/notes",
  });
  return pages;
}

beforeEach(() => {
  vi.clearAllMocks();
  useApp.setState({ pageDocs: {}, tabs: [], activeProject: "p1" });
  vi.mocked(api.listNodePages).mockResolvedValue({ pages: [{ id: "notes", title: "Notes" }] });
  vi.mocked(api.putNodePage).mockResolvedValue({ ok: true, rev: "page-2" });
});

it.each([false, true])("keeps subpage text through unmount; save failure=%s", async (fails) => {
  if (fails) vi.mocked(api.putNodePage).mockRejectedValue(new Error("offline"));
  const editor = renderHook(useEditor);
  await act(async () => {});
  act(() => editor.result.current.setPageDoc({ nodeId: "a", id: "notes", content: "unsaved page", rev: "page-1", format: "md", dirty: true, loading: false, conflict: null }));
  editor.unmount();
  await act(async () => { await drainWrites(); });
  expect(api.putNodePage).toHaveBeenCalledWith("a", "notes", "unsaved page", "page-1");
  const reopened = renderHook(useEditor);
  await act(async () => {});
  if (fails) {
    expect(reopened.result.current.pageDoc).toMatchObject({ content: "unsaved page", dirty: true, rev: "page-1" });
    expect(reopened.result.current.activePageID).toBe("notes");
  } else {
    expect(reopened.result.current.pageDoc).toBeNull();
    expect(reopened.result.current.activePageID).toBeNull();
  }
  reopened.unmount();
  await act(async () => { await drainWrites(); });
});
