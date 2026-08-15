/**
 * Document slice [A-05].
 *
 * Documents save themselves. The drawer schedules the writes (state/autosave.ts
 * owns the timing); this slice owns what a write means, and the difference
 * between one the user asked for and one that happened behind them:
 *
 * - a conflict is still a decision, never a silent resolution, but an automatic
 *   save that hits one stops trying and parks the text in `.vised/drafts`
 *   rather than interrupting whatever is being typed;
 * - a staged lifecycle status is *not* carried by an automatic save. See
 *   runSlice.ts for why that one is deliberately still manual;
 * - leaving is a save. Switching tabs, closing the drawer and closing a tab all
 *   flush the document being left before they do anything else, because the
 *   idle timer alone loses the last edit when a laptop lid closes.
 *
 * Unsaved text is still mirrored into `.vised/drafts` so a crash, a dead server
 * or a project switch never loses it.
 */

import { api, ConflictError } from "../api";
import { describeFailure, operationScope } from "./operations";
import { patchTab, queues } from "./internals";
import type { AppSlice, DocumentSlice } from "./types";

/**
 * The revision each tab most recently wrote itself.
 *
 * The server broadcasts `node-changed` to every client including the one that
 * caused it, so our own save comes straight back as "the file changed on disk".
 * With a manual save that cost one wasted GET an hour. With an automatic one it
 * is a GET per save, and worse: if the user kept typing, the echo of their own
 * write raises the "檔案在磁碟上被修改了" banner against them. Remembering what
 * we wrote lets `notifyDiskChange` recognise the echo and do nothing [C-03].
 */
const selfWrittenRevs = new Map<string, string>();

/**
 * Body size above which an unload save stops asking for `keepalive`. The spec's
 * budget is 64 KB across every keepalive request in flight; staying well under
 * it leaves room for the JSON envelope and for a second document being flushed
 * by the same unload.
 */
const UNLOAD_BODY_LIMIT = 48_000;

export const createDocumentSlice: AppSlice<DocumentSlice> = (set, get) => ({
  tabs: [],
  activeTab: null,
  pageDocs: {},

  setPageDoc: (key, next) => {
    set((state) => {
      const current = state.pageDocs[key] ?? null;
      const value = typeof next === "function" ? next(current) : next;
      if (value === current) return {};
      const pageDocs = { ...state.pageDocs };
      if (value) pageDocs[key] = value;
      else delete pageDocs[key];
      return { pageDocs };
    });
  },

  savePageDoc: async (key) => {
    const snapshot = get().pageDocs[key];
    if (!snapshot || !snapshot.dirty || snapshot.loading) return { ok: true };
    // A subpage save reports on the node's document scope, the same badge the
    // main document uses. The editor shows one document at a time, so one badge
    // is what a person is looking at — and a subpage that saved itself in
    // silence would be exactly the thing auto-save must not do.
    const scope = operationScope.document(snapshot.nodeId);
    get().beginOperation(scope, "document.save");
    try {
      const response = await api.putNodePage(
        snapshot.nodeId,
        snapshot.id,
        snapshot.content,
        snapshot.rev,
      );
      get().settleOperation(scope, { status: "saved", operation: "document.save" });
      set((state) => {
        const current = state.pageDocs[key];
        if (!current || current.id !== snapshot.id) return {};
        return {
          pageDocs: {
            ...state.pageDocs,
            [key]: {
              ...current,
              rev: response.rev,
              // Kept typing while the save was in flight: adopt the new rev
              // but stay dirty, so the next save is not a false conflict.
              dirty: current.content !== snapshot.content,
              conflict: null,
            },
          },
        };
      });
      return { ok: true };
    } catch (error) {
      if (error instanceof ConflictError) {
        set((state) => {
          const current = state.pageDocs[key];
          if (!current || current.id !== snapshot.id) return {};
          return {
            pageDocs: {
              ...state.pageDocs,
              [key]: {
                ...current,
                conflict: {
                  diskRev: error.diskRev,
                  diskContent: error.diskContent,
                },
              },
            },
          };
        });
        get().settleOperation(scope, {
          status: "conflict",
          operation: "document.save",
          message: "子頁已被外部修改；請選擇保留哪一版。",
        });
        return { ok: false };
      }
      const message = error instanceof Error ? error.message : "子頁儲存失敗";
      get().settleOperation(scope, {
        status: "error",
        operation: "document.save",
        message,
      });
      return { ok: false, error: message };
    }
  },

  openNodeLink: async ({ project, nodeId }) => {
    const state = get();
    if (project && project !== state.activeProject) {
      if (!state.projects.some((entry) => entry.name === project)) {
        throw new Error(`找不到專案「${project}」`);
      }
      await state.switchProject(project);
    }
    const graph = get().graph;
    if (graph && !(graph.nodes ?? []).some((node) => node.id === nodeId)) {
      throw new Error(`找不到節點「${nodeId}」`);
    }
    await get().openTab(nodeId);
    // Opening the document is only half the jump: the board still shows
    // wherever the reader was, so ask it to reveal the node too.
    set({ revealRequest: { nodeId, at: Date.now() } });
  },

  revealRequest: null,
  revealNode: (nodeId) => set({ revealRequest: { nodeId, at: Date.now() } }),
  clearRevealRequest: () => set({ revealRequest: null }),

  openTab: async (id) => {
    const { tabs } = get();
    // Whatever was on screen is being left. Save it before the next document
    // takes over, rather than hoping its idle timer fires first.
    get().flushActiveDocument(id);
    if (tabs.some((t) => t.id === id)) {
      set({ activeTab: id });
      return;
    }
    set({
      tabs: [
        ...tabs,
        {
          id,
          pinned: false,
          content: "",
          rev: "",
          dirty: false,
          loaded: false,
          diskChanged: false,
          draftPending: null,
          conflict: null,
        },
      ],
      activeTab: id,
    });
    try {
      const [file, draft] = await Promise.all([
        api.getNode(id),
        api.getDraft(id).catch(() => ({ exists: false as const })),
      ]);
      const draftPending =
        draft.exists && draft.content !== undefined && draft.content !== file.content
          ? draft.content
          : null;
      set((st) => ({
        tabs: patchTab(st.tabs, id, {
          content: file.content,
          rev: file.rev,
          loaded: true,
          draftPending,
        }),
      }));
    } catch (error) {
      set((state) => ({
        tabs: state.tabs.filter((tab) => tab.id !== id),
        activeTab: state.activeTab === id ? null : state.activeTab,
      }));
      throw error;
    }
  },

  closeTab: (id) => {
    // Closing is leaving, and leaving saves [C-03]. The write is queued rather
    // than awaited so the tab closes at the speed of a click; the queue in
    // internals.ts keeps it ordered, and drainWrites waits for it before a
    // project switch can cut it in half.
    void get().saveTab(id, { auto: true }).catch(() => undefined);
    set((st) => {
      const tabs = st.tabs.filter((t) => t.id !== id);
      let active = st.activeTab;
      if (active === id) active = tabs.length ? tabs[tabs.length - 1].id : null;
      return { tabs, activeTab: active };
    });
  },

  closeTabs: async (ids) => {
    const closing = new Set(ids);
    if (closing.size === 0) return;

    // Save for real first; the draft is the fallback for whatever the save
    // could not land — a conflict, or a server that is not answering. Disable
    // saveTab's usual fallback here: closing has to observe the
    // draft result before it can know that removing the editor is safe.
    const dirtyTabs = get().tabs.filter((tab) => closing.has(tab.id) && tab.dirty);
    await Promise.all(
      dirtyTabs.map((tab) =>
        get()
          .saveTab(tab.id, { auto: true, fallbackToDraft: false })
          .catch(() => undefined),
      ),
    );
    const stillDirty = get().tabs.filter((tab) => closing.has(tab.id) && tab.dirty);
    const draftedContent = new Map<string, string>();
    await Promise.all(
      stillDirty.map(async (tab) => {
        try {
          await api.putDraft(tab.id, tab.content);
          draftedContent.set(tab.id, tab.content);
        } catch {
          // The tab remains open below. The aggregate error tells the caller
          // which action is safe to retry without exposing document contents.
        }
      }),
    );

    // A tab can change while its save is in flight. Re-read immediately before
    // the synchronous state update and close it only if the current buffer is
    // clean or is byte-for-byte the snapshot that reached the draft store.
    const closeable = new Set<string>();
    const retained: string[] = [];
    for (const id of closing) {
      const current = get().tabs.find((tab) => tab.id === id);
      if (!current || !current.dirty || draftedContent.get(id) === current.content) {
        closeable.add(id);
      } else {
        retained.push(id);
      }
    }

    set((state) => {
      const tabs = state.tabs.filter((tab) => !closeable.has(tab.id));
      if (state.activeTab === null || !closeable.has(state.activeTab)) return { tabs };

      const activeIndex = state.tabs.findIndex((tab) => tab.id === state.activeTab);
      const right = state.tabs
        .slice(activeIndex + 1)
        .find((tab) => !closeable.has(tab.id));
      const left = state.tabs
        .slice(0, activeIndex)
        .reverse()
        .find((tab) => !closeable.has(tab.id));
      return { tabs, activeTab: right?.id ?? left?.id ?? null };
    });

    if (retained.length > 0) {
      const subject =
        retained.length === 1 ? `分頁「${retained[0]}」` : `${retained.length} 個分頁`;
      throw new Error(
        `無法安全關閉${subject}：最新內容尚未儲存，分頁與編輯內容已保留。請檢查連線或磁碟空間後重試。`,
      );
    }
  },

  setTabPinned: (id, pinned) => {
    set((state) => {
      const updated = state.tabs.map((tab) =>
        tab.id === id ? { ...tab, pinned } : tab,
      );
      return {
        tabs: [
          ...updated.filter((tab) => tab.pinned),
          ...updated.filter((tab) => !tab.pinned),
        ],
      };
    });
  },

  closeDrawer: () => {
    get().flushActiveDocument(null);
    set({ activeTab: null });
  },

  setActiveTab: (id) => {
    get().flushActiveDocument(id);
    set({ activeTab: id });
  },

  /**
   * Saves the document that is about to stop being visible.
   *
   * Every way out of a document funnels through here — switching tabs, opening
   * another node, closing the drawer — so none of them has to remember on its
   * own, and none of them has to wait: the per-document queue in internals.ts
   * orders the write behind anything already in flight, and `drainWrites`
   * blocks a project switch until it lands.
   */
  flushActiveDocument: (next) => {
    const { activeTab, tabs } = get();
    if (activeTab === null || activeTab === next) return;
    const leaving = tabs.find((tab) => tab.id === activeTab);
    if (!leaving?.dirty) return;
    void get().saveTab(activeTab, { auto: true }).catch(() => undefined);
  },

  setTabContent: (id, content) => {
    set((st) => ({ tabs: patchTab(st.tabs, id, { content, dirty: true }) }));
  },

  /**
   * Takes on a revision somebody else's session wrote [P2].
   *
   * In a live session every participant holds the same converged text, so the
   * leader's write is this tab's write too: adopting its revision is what stops
   * the `node-changed` that follows from being read as somebody editing the
   * file behind this window's back. The text is deliberately not replaced —
   * the CRDT already delivered it, and a reload here would fight the caret.
   */
  adoptDocumentRev: (id, rev, settled = true) => {
    const tab = get().tabs.find((current) => current.id === id);
    if (!tab) return;
    selfWrittenRevs.set(id, rev);
    set((st) => ({
      // `settled` is false when the revision came from a file this session has
      // merged rather than written: the text is now ahead of disk again, so
      // saying it is clean would leave the merge unsaved with nothing to
      // prompt the next write.
      tabs: patchTab(st.tabs, id, { rev, diskChanged: false, ...(settled ? { dirty: false } : {}) }),
    }));
  },

  // ws said the file changed on disk. Clean tab: reload silently.
  // Dirty tab: flag it — never clobber unsaved edits.
  notifyDiskChange: async (id) => {
    const tab = get().tabs.find((t) => t.id === id);
    if (!tab || !tab.loaded) return;
    // Our own save coming back around. Nothing changed that this tab does not
    // already have, so reading it again would be a request per save and would
    // flag the document as externally modified against its own author [C-03].
    if (selfWrittenRevs.get(id) === tab.rev) {
      selfWrittenRevs.delete(id);
      return;
    }
    if (tab.dirty) {
      set((st) => ({ tabs: patchTab(st.tabs, id, { diskChanged: true }) }));
      return;
    }
    const file = await api.getNode(id);
    set((st) => ({
      tabs: st.tabs.map((current) =>
        current.id !== id
          ? current
          : current.dirty
            ? { ...current, diskChanged: true }
            : { ...current, content: file.content, rev: file.rev, diskChanged: false },
      ),
    }));
  },

  saveTab: (id, options = {}) => {
    const auto = options.auto === true;
    const previous = queues.tabSave.get(id) ?? Promise.resolve();
    const operation = previous.catch(() => undefined).then(async () => {
      // A staged status change rides along with a save the *user* asked for, so
      // it goes out even when the text itself is untouched [B-04]. An automatic
      // save leaves it staged: the journal is append-only, so writing a status
      // nobody confirmed cannot be taken back. See runSlice.ts.
      if (!auto) await get().commitStagedLifecycle(id);
      const tab = get().tabs.find((item) => item.id === id);
      if (!tab || !tab.dirty) return;
      // An unresolved conflict is a question already on screen. Answering it by
      // retrying in the background would either keep failing or, worse, succeed
      // against a revision the user never agreed to.
      if (auto && tab.conflict) return;
      const savedContent = tab.content;
      const scope = operationScope.document(id);
      get().beginOperation(scope, "document.save");
      try {
        // `keepalive` lets the request survive the page, which is the only way
        // an unload save lands at all — but browsers cap a keepalive body at
        // 64 KB, and a request over the cap fails outright. Below the cap it is
        // the difference between saving and not; above it, an ordinary request
        // that the browser may still finish is strictly the better gamble.
        const written = await api.putNode(id, savedContent, tab.rev, {
          keepalive: options.unload === true && savedContent.length < UNLOAD_BODY_LIMIT,
        });
        // The store rewrites the frontmatter from the graph, so what landed is
        // not always what we sent; the PUT hands the composed file back rather
        // than making every save cost a GET as well [C-03].
        const content = written.content ?? savedContent;
        selfWrittenRevs.set(id, written.rev);
        set((state) => ({
          tabs: state.tabs.map((current) => {
            if (current.id !== id) return current;
            // Kept typing while the save was in flight: adopt the new rev but
            // stay dirty, so the next save is not a false conflict.
            if (current.content !== savedContent) {
              return {
                ...current,
                rev: written.rev,
                dirty: true,
                diskChanged: false,
                conflict: null,
              };
            }
            return {
              ...current,
              content,
              rev: written.rev,
              dirty: false,
              diskChanged: false,
              conflict: null,
            };
          }),
        }));
        get().settleOperation(scope, { status: "saved", operation: "document.save" });
      } catch (e) {
        if (e instanceof ConflictError) {
          set((state) => ({
            tabs: patchTab(state.tabs, id, {
              conflict: { diskRev: e.diskRev, diskContent: e.diskContent },
            }),
          }));
          // The editor buffer is untouched either way — a conflict never
          // rewrites what someone is typing. An automatic one additionally
          // parks the text server-side straight away, because the user did not
          // ask for this save and may not look at the banner for a while.
          const draftSaved =
            auto && options.fallbackToDraft !== false
              ? await api.putDraft(id, savedContent).then(
                  () => true,
                  () => false,
                )
              : false;
          get().settleOperation(scope, {
            status: "conflict",
            operation: "document.save",
            message: draftSaved
              ? "檔案已被外部修改；請選擇保留哪一版。你的內容仍在編輯器中，也已存為草稿。"
              : "檔案已被外部修改；請選擇保留哪一版。你的內容仍在編輯器中。",
          });
          return;
        }
        get().settleOperation(scope, {
          status: "error",
          operation: "document.save",
          message: describeFailure(e, "文件儲存失敗"),
        });
        // A failed automatic save also goes to a draft, so the work survives
        // the tab being closed before the retry succeeds.
        if (auto && options.fallbackToDraft !== false) {
          await api.putDraft(id, savedContent).catch(() => undefined);
        }
        throw e;
      }
    });
    queues.tabSave.set(id, operation);
    const cleanup = () => {
      if (queues.tabSave.get(id) === operation) queues.tabSave.delete(id);
    };
    void operation.then(cleanup, cleanup);
    return operation;
  },

  // Project-level save: every dirty document at once, so the explorer can
  // promise "the project is on disk" without walking the editors itself.
  saveAllTabs: async () => {
    const dirtyTabs = get().tabs.filter((tab) => tab.dirty);
    const dirtyPages = Object.entries(get().pageDocs).filter(
      ([, doc]) => doc.dirty && !doc.loading,
    );
    if (dirtyTabs.length === 0 && dirtyPages.length === 0) return 0;
    await Promise.all([
      ...dirtyTabs.map((tab) => get().saveTab(tab.id)),
      ...dirtyPages.map(([key]) => get().savePageDoc(key)),
    ]);
    // Conflicts settle without throwing; report only what actually landed.
    const stillDirtyTabs = new Set(
      get()
        .tabs.filter((tab) => tab.dirty)
        .map((tab) => tab.id),
    );
    const pages = get().pageDocs;
    return (
      dirtyTabs.filter((tab) => !stillDirtyTabs.has(tab.id)).length +
      dirtyPages.filter(([key]) => !pages[key]?.dirty).length
    );
  },

  saveDraft: async (id) => {
    const tab = get().tabs.find((t) => t.id === id);
    if (!tab || !tab.dirty) return;
    await api.putDraft(id, tab.content).catch(() => undefined);
  },

  applyDraft: async (id, use) => {
    const tab = get().tabs.find((t) => t.id === id);
    if (!tab || tab.draftPending === null) return;
    if (use) {
      set((st) => ({
        tabs: patchTab(st.tabs, id, {
          content: tab.draftPending!,
          dirty: true,
          draftPending: null,
        }),
      }));
    } else {
      await api.deleteDraft(id).catch(() => undefined);
      set((st) => ({ tabs: patchTab(st.tabs, id, { draftPending: null }) }));
    }
  },

  resolveConflict: async (id, resolution) => {
    const tab = get().tabs.find((t) => t.id === id);
    if (!tab || !tab.conflict) return;
    get().clearOperation(operationScope.document(id));
    if (resolution === "disk") {
      // take the disk version; my version stays recoverable in drafts
      await api.putDraft(id, tab.content).catch(() => undefined);
      set((st) => ({
        tabs: patchTab(st.tabs, id, {
          content: tab.conflict!.diskContent,
          rev: tab.conflict!.diskRev,
          dirty: false,
          diskChanged: false,
          conflict: null,
        }),
      }));
    } else {
      // overwrite disk with mine (disk version is snapshotted in history)
      const submitted = tab.content;
      const res = await api.putNode(id, submitted, tab.conflict.diskRev);
      set((st) => ({
        tabs: st.tabs.map((current) =>
          current.id !== id
            ? current
            : {
                ...current,
                rev: res.rev,
                dirty: current.content !== submitted,
                diskChanged: false,
                conflict: null,
              },
        ),
      }));
    }
  },
});
