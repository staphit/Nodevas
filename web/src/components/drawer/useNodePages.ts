import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type RefObject,
  type SetStateAction,
} from "react";

import { api, type NodePageInfo, type PageFormat } from "../../api";
import { useI18n } from "../../i18n";
import type { PageDoc, PageSaveResult } from "../../state/types";
import { useApp } from "../../store";
import { confirmAction } from "../ConfirmDialog";
import type { EditorMode } from "./editorExtensions";
import { useNodeDetailView } from "./openNodeDetail";

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

/** A subpage's title, as the page menu names it; blank when the node has no such page. */
export async function readNodePageTitle(nodeID: string, pageID: string): Promise<string> {
  const { pages } = await api.listNodePages(nodeID);
  return pages.find((page) => page.id === pageID)?.title ?? "";
}

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
  const { t } = useI18n();
  const refreshTrash = useApp((s) => s.refreshTrash);
  // Keep each node's open subpage in the project store. Unmount must not erase
  // it before autosave reads it; failed saves are recovered when it is reopened.
  // Workspace switches save these buffers before clearing them.
  const editorKey = id;
  const pageDoc = useApp((s) => s.pageDocs[editorKey] ?? null);
  const [pages, setPages] = useState<NodePageInfo[]>([]);
  const [pagesLoading, setPagesLoading] = useState(true);
  const [activePageID, setActivePageID] = useState<string | null>(() => pageDoc?.dirty ? pageDoc.id : null);
  const initialPageApplied = useRef(Boolean(pageDoc?.dirty));
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
  useEffect(() => {
    // A clean buffer is not a cache: reopening must read current disk content.
    // A dirty one stays available to the unmount save and to recovery on reopen.
    const dropCleanBuffer = () => {
      if (!useApp.getState().pageDocs[editorKey]?.dirty) {
        setPageDocInStore(editorKey, null);
      }
    };
    dropCleanBuffer();
    return dropCleanBuffer;
  }, [editorKey, setPageDocInStore]);
  const [pageError, setPageError] = useState<string | null>(null);
  const [pageCreateOpen, setPageCreateOpen] = useState(false);
  const [pageTitle, setPageTitle] = useState("");
  const [pageFormat, setPageFormat] = useState<PageFormat>("md");
  const pageImportInputRef = useRef<HTMLInputElement>(null);
  const [pageRename, setPageRename] = useState("");
  const [pageBusy, setPageBusy] = useState(false);

  // Every local change to the list bumps this, so a read that was already on
  // its way cannot put back the list as it was before the change.
  const listGeneration = useRef(0);
  const setPagesHere = useCallback((next: SetStateAction<NodePageInfo[]>) => {
    listGeneration.current += 1;
    setPages(next);
  }, []);
  const diskChanges = useApp((s) => s.nodeDiskChanges[id] ?? 0);
  // Coming back to the window re-reads too: an event missed while the socket
  // was down would otherwise leave the tabs stale until the drawer reopens.
  const [focusReads, setFocusReads] = useState(0);
  useEffect(() => {
    const reread = () => {
      if (document.visibilityState !== "hidden") setFocusReads((count) => count + 1);
    };
    window.addEventListener("focus", reread);
    document.addEventListener("visibilitychange", reread);
    return () => {
      window.removeEventListener("focus", reread);
      document.removeEventListener("visibilitychange", reread);
    };
  }, []);
  const listedNode = useRef<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    // Only a different node shows the loading state; a re-read of the same
    // one keeps its tabs on screen and swaps them when the answer arrives.
    const firstRead = listedNode.current !== id;
    if (firstRead) setPagesLoading(true);
    const generation = listGeneration.current;
    void api
      .listNodePages(id)
      .then((response) => {
        if (!cancelled && generation === listGeneration.current) {
          setPagesHere(response.pages ?? []);
        }
      })
      .catch((error) => {
        if (!cancelled && firstRead) {
          setPageError(error instanceof Error ? error.message : t("subpage.loadFailed"));
        }
      })
      .finally(() => {
        if (cancelled) return;
        listedNode.current = id;
        setPagesLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [id, diskChanges, focusReads]);
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
        setPageError(error instanceof Error ? error.message : t("subpage.openFailed"));
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

  // A search jump asks for the subpage that holds its match; the panel
  // showing the node opens it once its pages are listed.
  const pageRequest = useNodeDetailView((s) => s.pageRequest);
  useEffect(() => {
    if (pageRequest?.nodeId !== id || pagesLoading) return;
    useNodeDetailView.getState().clearPageRequest();
    if (pages.some((page) => page.id === pageRequest.pageId)) void selectPage(pageRequest.pageId);
  }, [pageRequest, id, pages, pagesLoading, selectPage]);

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
      setPagesHere((current) => [...current, response.page]);
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
      setPageError(error instanceof Error ? error.message : t("subpage.createFailed"));
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
        setPagesHere((current) => [...current, response.page]);
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
        setPageError(error instanceof Error ? error.message : t("subpage.importFailed"));
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
        title: t("subpage.convertTitle", { title: current.title, format: format.toUpperCase() }),
        description: t("subpage.convertDescription"),
        confirmLabel: t("subpage.convert"),
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
        setPagesHere(response.pages);
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
        setPageError(error instanceof Error ? error.message : t("subpage.convertFailed"));
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
      setPagesHere(response.pages);
    } catch (error) {
      setPageError(error instanceof Error ? error.message : t("subpage.renameFailed"));
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
      setPagesHere(response.pages);
    } catch (error) {
      setPageError(error instanceof Error ? error.message : t("subpage.reorderFailed"));
    } finally {
      setPageBusy(false);
    }
  }, [activePageID, id, pageBusy, pages]);

  const removePage = useCallback(async () => {
    if (!activePageID || pageBusy) return;
    const current = pages.find((page) => page.id === activePageID);
    if (!current) return;
    const confirmed = await confirmAction({
      title: t("subpage.deleteTitle", { title: current.title }),
      description: t("subpage.deleteDescription"),
      confirmLabel: t("canvasOps.moveToTrash"),
      tone: "danger",
    });
    if (!confirmed || !(await saveSubpage()).ok) return;
    setPageBusy(true);
    setPageError(null);
    try {
      await api.deleteNodePage(id, activePageID);
      setPagesHere((items) => items.filter((page) => page.id !== activePageID));
      setActivePageID(null);
      setPageDoc(null);
      setPageRename("");
      await refreshTrash();
    } catch (error) {
      setPageError(error instanceof Error ? error.message : t("subpage.deleteFailed"));
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
