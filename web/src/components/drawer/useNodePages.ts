import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type Dispatch,
  type RefObject,
  type SetStateAction,
} from "react";

import { api, type NodePageInfo, type PageFormat } from "../../api";
import type { PageDoc, PageSaveResult } from "../../state/types";
import { useApp } from "../../store";
import { confirmAction } from "../ConfirmDialog";
import type { EditorMode } from "./editorExtensions";

/** What the editor passes around: the node id is filled in on the way to the store. */
type PageDocInput = PageDoc | Omit<PageDoc, "nodeId"> | null;

function withNode(value: PageDocInput, nodeId: string): PageDoc | null {
  return value ? { ...value, nodeId } : null;
}

export type NodePages = {
  pages: NodePageInfo[];
  pagesLoading: boolean;
  activePageID: string | null;
  /**
   * The main document is always Markdown; a subpage is whatever it was
   * created as, and that decides how the editor behaves.
   */
  activeFormat: PageFormat;
  pageDoc: PageDoc | null;
  setPageDoc: (
    next: PageDocInput | ((current: PageDocInput) => PageDocInput),
  ) => void;
  pageError: string | null;
  setPageError: Dispatch<SetStateAction<string | null>>;
  pageCreateOpen: boolean;
  setPageCreateOpen: Dispatch<SetStateAction<boolean>>;
  pageTitle: string;
  setPageTitle: Dispatch<SetStateAction<string>>;
  pageFormat: PageFormat;
  setPageFormat: Dispatch<SetStateAction<PageFormat>>;
  pageBusy: boolean;
  pageRename: string;
  setPageRename: Dispatch<SetStateAction<string>>;
  pageImportInputRef: RefObject<HTMLInputElement>;
  saveSubpage: () => Promise<PageSaveResult>;
  selectPage: (pageID: string | null) => Promise<void>;
  createPage: () => Promise<void>;
  importPage: (files: FileList | null) => Promise<void>;
  convertPage: (format: PageFormat) => Promise<void>;
  renamePage: () => Promise<void>;
  movePage: (offset: number) => Promise<void>;
  removePage: () => Promise<void>;
};

/** The node's subpages: which one is open, and every edit to the set of them. */
export function useNodePages({
  nodeId: id,
  initialPageID,
  editorMode,
  setEditorMode,
  getCurrentPageContent,
  replaceCurrentPageContent,
}: {
  nodeId: string;
  initialPageID: string;
  editorMode: EditorMode;
  setEditorMode: (mode: EditorMode) => void;
  /** Read the mounted editor so actions cannot race one render behind it. */
  getCurrentPageContent?: () => string | undefined;
  /** Update the mounted editor before replacing its backing page state. */
  replaceCurrentPageContent?: (content: string) => void;
}): NodePages {
  const refreshTrash = useApp((s) => s.refreshTrash);
  const [pages, setPages] = useState<NodePageInfo[]>([]);
  const [pagesLoading, setPagesLoading] = useState(true);
  const [activePageID, setActivePageID] = useState<string | null>(null);
  const initialPageApplied = useRef(false);
  // The open subpage lives in the store, keyed by this editor instance, so a
  // project-wide save reaches it too. Unmount drops the entry.
  const editorKey = useId();
  const pageDoc = useApp((s) => s.pageDocs[editorKey] ?? null);
  const setPageDocInStore = useApp((s) => s.setPageDoc);
  const savePageDoc = useApp((s) => s.savePageDoc);
  const setPageDoc = useCallback(
    (next: PageDocInput | ((current: PageDocInput) => PageDocInput)) => {
      setPageDocInStore(
        editorKey,
        typeof next === "function"
          ? (current) => withNode(next(current), id)
          : withNode(next, id),
      );
    },
    [editorKey, id, setPageDocInStore],
  );
  useEffect(
    () => () => setPageDocInStore(editorKey, null),
    [editorKey, setPageDocInStore],
  );
  const [pageError, setPageError] = useState<string | null>(null);
  const [pageCreateOpen, setPageCreateOpen] = useState(false);
  const [pageTitle, setPageTitle] = useState("");
  const [pageFormat, setPageFormat] = useState<PageFormat>("md");
  const pageImportInputRef = useRef<HTMLInputElement>(null);
  const [pageRename, setPageRename] = useState("");
  const [pageBusy, setPageBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setPagesLoading(true);
    void api
      .listNodePages(id)
      .then((response) => {
        if (!cancelled) setPages(response.pages ?? []);
      })
      .catch((error) => {
        if (!cancelled) {
          setPageError(error instanceof Error ? error.message : "無法讀取子頁");
        }
      })
      .finally(() => {
        if (!cancelled) setPagesLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [id]);
  useEffect(() => {
    setPageRename(
      activePageID
        ? pages.find((page) => page.id === activePageID)?.title ?? ""
        : "",
    );
  }, [activePageID, pages]);

  const saveSubpage = useCallback(async () => {
    setPageError(null);
    const result = await savePageDoc(editorKey);
    if (result.error) setPageError(result.error);
    return result;
  }, [editorKey, savePageDoc]);

  const selectPage = useCallback(
    async (pageID: string | null) => {
      if (pageID === activePageID) return;
      if (!(await saveSubpage()).ok) return;
      if (editorMode === "preview") setEditorMode("live");
      setPageError(null);
      setActivePageID(pageID);
      if (pageID === null) {
        setPageDoc(null);
        return;
      }
      setPageDoc({
        id: pageID,
        content: "",
        rev: "",
        format: pages.find((page) => page.id === pageID)?.format ?? "md",
        dirty: false,
        loading: true,
        conflict: null,
      });
      try {
        const response = await api.getNodePage(id, pageID);
        setPageDoc({
          id: pageID,
          content: response.content,
          rev: response.rev,
          format: response.format ?? "md",
          dirty: false,
          loading: false,
          conflict: null,
        });
      } catch (error) {
        setPageDoc(null);
        setActivePageID(null);
        setPageError(error instanceof Error ? error.message : "無法開啟子頁");
      }
    },
    [activePageID, id, pages, saveSubpage],
  );

  useEffect(() => {
    if (initialPageApplied.current || pagesLoading) return;
    initialPageApplied.current = true;
    if (
      initialPageID !== "main" &&
      pages.some((page) => page.id === initialPageID)
    ) {
      void selectPage(initialPageID);
    }
  }, [initialPageID, pages, pagesLoading, selectPage]);

  const createPage = useCallback(async () => {
    const title = pageTitle.trim();
    if (!title || pageBusy) return;
    setPageBusy(true);
    setPageError(null);
    if (!(await saveSubpage()).ok) {
      setPageBusy(false);
      return;
    }
    try {
      const response = await api.createNodePage(id, title, pageFormat);
      setPages((current) => [...current, response.page]);
      setActivePageID(response.page.id);
      setPageDoc({
        id: response.page.id,
        content: response.content,
        rev: response.rev,
        format: response.page.format ?? pageFormat,
        dirty: false,
        loading: false,
        conflict: null,
      });
      setPageTitle("");
      setPageCreateOpen(false);
      if (editorMode === "preview") setEditorMode("live");
    } catch (error) {
      setPageError(error instanceof Error ? error.message : "無法建立子頁");
    } finally {
      setPageBusy(false);
    }
  }, [id, pageBusy, pageFormat, pageTitle, saveSubpage]);

  // An existing file becomes a subpage byte for byte; nothing is converted
  // until the page is saved.
  const importPage = useCallback(
    async (files: FileList | null) => {
      const file = files?.[0];
      if (!file || pageBusy) return;
      setPageBusy(true);
      setPageError(null);
      if (!(await saveSubpage()).ok) {
        setPageBusy(false);
        return;
      }
      try {
        const response = await api.importNodePage(id, file);
        setPages((current) => [...current, response.page]);
        setActivePageID(response.page.id);
        setPageDoc({
          id: response.page.id,
          content: response.content,
          rev: response.rev,
          format: response.page.format ?? "md",
          dirty: false,
          loading: false,
          conflict: null,
        });
        setPageCreateOpen(false);
        if (editorMode === "preview") setEditorMode("live");
      } catch (error) {
        setPageError(error instanceof Error ? error.message : "無法匯入檔案");
      } finally {
        setPageBusy(false);
        if (pageImportInputRef.current) pageImportInputRef.current.value = "";
      }
    },
    [editorMode, id, pageBusy, saveSubpage],
  );

  const convertPage = useCallback(
    async (format: PageFormat) => {
      if (!activePageID || pageBusy) return;
      const current = pages.find((page) => page.id === activePageID);
      if (!current || (current.format ?? "md") === format) return;
      const confirmed = await confirmAction({
        title: `把「${current.title}」轉成 ${format.toUpperCase()}？`,
        description:
          "檔案會改寫成新格式，原檔留在版本歷史中。" +
          "轉換只保留標題、清單、表格、程式碼、引用與連結這些共通結構，" +
          "其他排版會流失。",
        confirmLabel: "轉換格式",
      });
      if (!confirmed) return;
      const currentEditorContent = getCurrentPageContent?.();
      if (
        currentEditorContent !== undefined &&
        currentEditorContent !== pageDoc?.content
      ) {
        setPageDoc((current) =>
          current?.id === activePageID
            ? {
                ...current,
                content: currentEditorContent,
                dirty: true,
                conflict: null,
              }
            : current,
        );
      }
      if (!(await saveSubpage()).ok) return;
      setPageBusy(true);
      setPageError(null);
      try {
        const response = await api.convertNodePage(id, activePageID, format);
        replaceCurrentPageContent?.(response.content);
        setPages(response.pages);
        setPageDoc({
          id: activePageID,
          content: response.content,
          rev: response.rev,
          format,
          dirty: false,
          loading: false,
          conflict: null,
        });
      } catch (error) {
        setPageError(error instanceof Error ? error.message : "格式轉換失敗");
      } finally {
        setPageBusy(false);
      }
    },
    [
      activePageID,
      getCurrentPageContent,
      id,
      pageBusy,
      pageDoc,
      pages,
      replaceCurrentPageContent,
      saveSubpage,
    ],
  );

  const renamePage = useCallback(async () => {
    if (!activePageID || pageBusy) return;
    const current = pages.find((page) => page.id === activePageID);
    const title = pageRename.trim();
    if (!current || !title || title === current.title) return;
    setPageBusy(true);
    setPageError(null);
    try {
      const response = await api.updateNodePage(id, activePageID, title);
      setPages(response.pages);
    } catch (error) {
      setPageError(error instanceof Error ? error.message : "子頁重新命名失敗");
    } finally {
      setPageBusy(false);
    }
  }, [activePageID, id, pageBusy, pageRename, pages]);

  const movePage = useCallback(async (offset: number) => {
    if (!activePageID || pageBusy) return;
    const currentIndex = pages.findIndex((page) => page.id === activePageID);
    if (currentIndex < 0) return;
    const targetIndex = Math.max(0, Math.min(pages.length - 1, currentIndex + offset));
    if (targetIndex === currentIndex) return;
    const current = pages[currentIndex];
    setPageBusy(true);
    setPageError(null);
    try {
      const response = await api.updateNodePage(
        id,
        activePageID,
        current.title,
        targetIndex,
      );
      setPages(response.pages);
    } catch (error) {
      setPageError(error instanceof Error ? error.message : "子頁排序失敗");
    } finally {
      setPageBusy(false);
    }
  }, [activePageID, id, pageBusy, pages]);

  const removePage = useCallback(async () => {
    if (!activePageID || pageBusy) return;
    const current = pages.find((page) => page.id === activePageID);
    if (!current) return;
    const confirmed = await confirmAction({
      title: `刪除子頁「${current.title}」？`,
      description: "子頁會移到垃圾桶，可稍後還原。",
      confirmLabel: "移到垃圾桶",
      tone: "danger",
    });
    if (!confirmed || !(await saveSubpage()).ok) return;
    setPageBusy(true);
    setPageError(null);
    try {
      await api.deleteNodePage(id, activePageID);
      setPages((items) => items.filter((page) => page.id !== activePageID));
      setActivePageID(null);
      setPageDoc(null);
      setPageRename("");
      await refreshTrash();
    } catch (error) {
      setPageError(error instanceof Error ? error.message : "子頁刪除失敗");
    } finally {
      setPageBusy(false);
    }
  }, [activePageID, id, pageBusy, pages, refreshTrash, saveSubpage]);

  const activeFormat: PageFormat = activePageID
    ? pageDoc?.format ??
      pages.find((page) => page.id === activePageID)?.format ??
      "md"
    : "md";

  return {
    pages,
    pagesLoading,
    activePageID,
    activeFormat,
    pageDoc,
    setPageDoc,
    pageError,
    setPageError,
    pageCreateOpen,
    setPageCreateOpen,
    pageTitle,
    setPageTitle,
    pageFormat,
    setPageFormat,
    pageBusy,
    pageRename,
    setPageRename,
    pageImportInputRef,
    saveSubpage,
    selectPage,
    createPage,
    importPage,
    convertPage,
    renamePage,
    movePage,
    removePage,
  };
}
