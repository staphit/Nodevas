import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError } from "../api";
import { useApp } from "../store";
import { drainWrites } from "./internals";
import type { Tab } from "./types";

/**
 * The save-on-leave paths queue their write rather than awaiting it, so that
 * closing a tab happens at the speed of a click. Tests have to wait for the
 * same queue a project switch waits for.
 */
const drainSaves = () => drainWrites();

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getNode: vi.fn(),
      getDraft: vi.fn(),
      putDraft: vi.fn(),
      putNode: vi.fn(),
      putNodePage: vi.fn(),
    },
  };
});

function tab(id: string, options: Partial<Tab> = {}): Tab {
  return {
    id,
    pinned: false,
    content: `${id} content`,
    rev: "rev-1",
    dirty: false,
    loaded: true,
    diskChanged: false,
    draftPending: null,
    conflict: null,
    ...options,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.putDraft).mockResolvedValue({ ok: true });
  useApp.setState({ tabs: [], activeTab: null, pageDocs: {} });
});

describe("project-wide save", () => {
  it("writes dirty documents and dirty subpages, and counts what landed", async () => {
    vi.mocked(api.putNode).mockResolvedValue({ ok: true, rev: "rev-2" });
    vi.mocked(api.getNode).mockResolvedValue({
      id: "a",
      content: "draft a",
      rev: "rev-2",
    });
    vi.mocked(api.putNodePage).mockResolvedValue({ ok: true, rev: "page-rev-2" });
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" }), tab("b")],
      pageDocs: {
        "editor-1": {
          nodeId: "a",
          id: "notes",
          content: "draft page",
          rev: "page-rev-1",
          format: "md",
          dirty: true,
          loading: false,
          conflict: null,
        },
        // Clean page: no request, not counted.
        "editor-2": {
          nodeId: "b",
          id: "spec",
          content: "spec",
          rev: "page-rev-1",
          format: "md",
          dirty: false,
          loading: false,
          conflict: null,
        },
      },
    });

    const saved = await useApp.getState().saveAllTabs();

    expect(api.putNode).toHaveBeenCalledWith("a", "draft a", "rev-1", {
      keepalive: false,
    });
    expect(api.putNodePage).toHaveBeenCalledTimes(1);
    expect(api.putNodePage).toHaveBeenCalledWith(
      "a",
      "notes",
      "draft page",
      "page-rev-1",
    );
    expect(saved).toBe(2);
    expect(useApp.getState().pageDocs["editor-1"]).toMatchObject({
      dirty: false,
      rev: "page-rev-2",
    });
  });

  it("reports nothing to save when every document is clean", async () => {
    useApp.setState({ tabs: [tab("a")], pageDocs: {} });

    expect(await useApp.getState().saveAllTabs()).toBe(0);
    expect(api.putNode).not.toHaveBeenCalled();
    expect(api.putNodePage).not.toHaveBeenCalled();
  });
});

describe("document tabs", () => {
  it("keeps pinned tabs at the leading edge with stable ordering", () => {
    useApp.setState({ tabs: [tab("a"), tab("b"), tab("c")], activeTab: "b" });

    useApp.getState().setTabPinned("b", true);
    expect(useApp.getState().tabs.map(({ id, pinned }) => ({ id, pinned }))).toEqual([
      { id: "b", pinned: true },
      { id: "a", pinned: false },
      { id: "c", pinned: false },
    ]);

    useApp.getState().setTabPinned("b", false);
    expect(useApp.getState().tabs.map((item) => item.id)).toEqual(["b", "a", "c"]);
  });

  // Closing is one of the moments a person expects their work to be safe, so a
  // batch close writes the files rather than only parking drafts.
  it("saves dirty documents for real before closing a batch", async () => {
    vi.mocked(api.putNode).mockResolvedValue({
      ok: true,
      rev: "rev-2",
      content: "draft a",
    });
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" }), tab("b"), tab("c")],
      activeTab: "a",
    });

    await useApp.getState().closeTabs(["a", "b"]);

    expect(api.putNode).toHaveBeenCalledWith("a", "draft a", "rev-1", {
      keepalive: false,
    });
    // Nothing was left outstanding, so there is no reason to write a draft too.
    expect(api.putDraft).not.toHaveBeenCalled();
    expect(useApp.getState().tabs.map((item) => item.id)).toEqual(["c"]);
    expect(useApp.getState().activeTab).toBe("c");
  });

  // The draft is the fallback, not the plan: what the save could not land still
  // has to survive the tab going away.
  it("parks a document the close could not save as a draft", async () => {
    vi.mocked(api.putNode).mockRejectedValue(new Error("伺服器沒有回應"));
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" }), tab("b")],
      activeTab: "a",
    });

    await useApp.getState().closeTabs(["a"]);

    expect(api.putDraft).toHaveBeenCalledWith("a", "draft a");
    expect(useApp.getState().tabs.map((item) => item.id)).toEqual(["b"]);
  });

  it("keeps the tab and its latest content when both file and draft saves fail", async () => {
    vi.mocked(api.putNode).mockRejectedValue(new Error("伺服器沒有回應"));
    vi.mocked(api.putDraft).mockRejectedValue(new Error("草稿磁碟已滿"));
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "唯一尚未落盤的內容" }), tab("b")],
      activeTab: "a",
    });

    await expect(useApp.getState().closeTabs(["a"])).rejects.toThrow(
      "最新內容尚未儲存，分頁與編輯內容已保留",
    );

    expect(api.putDraft).toHaveBeenCalledTimes(1);
    expect(api.putDraft).toHaveBeenCalledWith("a", "唯一尚未落盤的內容");
    expect(useApp.getState().tabs).toEqual([
      expect.objectContaining({
        id: "a",
        content: "唯一尚未落盤的內容",
        dirty: true,
      }),
      expect.objectContaining({ id: "b" }),
    ]);
    expect(useApp.getState().activeTab).toBe("a");
  });

  it("closes the safe members of a batch while retaining failed dirty tabs", async () => {
    vi.mocked(api.putNode).mockImplementation(async (id) => {
      if (id === "a") throw new Error("伺服器沒有回應");
      return { ok: true, rev: "rev-2", content: `${id} saved` };
    });
    vi.mocked(api.putDraft).mockRejectedValue(new Error("草稿磁碟已滿"));
    useApp.setState({
      tabs: [
        tab("a", { dirty: true, content: "keep a" }),
        tab("b", { dirty: true, content: "save b" }),
        tab("c"),
        tab("d"),
      ],
      activeTab: "b",
    });

    await expect(useApp.getState().closeTabs(["a", "b", "c"])).rejects.toThrow(
      "分頁「a」",
    );

    expect(useApp.getState().tabs.map((item) => item.id)).toEqual(["a", "d"]);
    expect(useApp.getState().tabs[0]).toMatchObject({
      content: "keep a",
      dirty: true,
    });
    expect(useApp.getState().activeTab).toBe("d");
  });

  it("retains edits made while a close-time draft is in flight", async () => {
    vi.mocked(api.putNode).mockRejectedValue(new Error("伺服器沒有回應"));
    let finishDraft!: () => void;
    vi.mocked(api.putDraft).mockImplementation(
      () => new Promise((resolve) => {
        finishDraft = () => resolve({ ok: true });
      }),
    );
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "first version" }), tab("b")],
      activeTab: "a",
    });

    const closing = useApp.getState().closeTabs(["a"]);
    await vi.waitFor(() => expect(api.putDraft).toHaveBeenCalledWith("a", "first version"));
    useApp.getState().setTabContent("a", "newest version");
    finishDraft();

    await expect(closing).rejects.toThrow("最新內容尚未儲存");
    expect(useApp.getState().tabs[0]).toMatchObject({
      id: "a",
      content: "newest version",
      dirty: true,
    });
    expect(useApp.getState().activeTab).toBe("a");
  });

  it("selects the nearest left tab when no right tab survives", async () => {
    useApp.setState({ tabs: [tab("a"), tab("b"), tab("c")], activeTab: "c" });

    await useApp.getState().closeTabs(["b", "c"]);

    expect(useApp.getState().activeTab).toBe("a");
  });
});

describe("automatic saving", () => {
  beforeEach(() => {
    vi.mocked(api.putNode).mockResolvedValue({
      ok: true,
      rev: "rev-2",
      content: "draft a",
    });
  });

  // The idle timer belongs to the editor, but the moment a document stops being
  // the one on screen belongs here — every route out of a document goes through
  // the store, and none of them may leave the last paragraph behind.
  it("saves the document being left before another one takes over", async () => {
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" }), tab("b")],
      activeTab: "a",
    });

    useApp.getState().setActiveTab("b");
    await drainSaves();

    expect(api.putNode).toHaveBeenCalledWith("a", "draft a", "rev-1", {
      keepalive: false,
    });
    expect(useApp.getState().activeTab).toBe("b");
    expect(useApp.getState().tabs.find((item) => item.id === "a")?.dirty).toBe(false);
  });

  it("saves the document being left when the drawer closes", async () => {
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" })],
      activeTab: "a",
    });

    useApp.getState().closeDrawer();
    await drainSaves();

    expect(api.putNode).toHaveBeenCalledTimes(1);
    expect(useApp.getState().activeTab).toBeNull();
  });

  it("does not write anything when the document being left is clean", async () => {
    useApp.setState({ tabs: [tab("a"), tab("b")], activeTab: "a" });

    useApp.getState().setActiveTab("b");
    await drainSaves();

    expect(api.putNode).not.toHaveBeenCalled();
  });

  // A status is an append to run/journal.jsonl and cannot be retracted, so it
  // waits for somebody to say so. Text is revisable and has snapshots behind it,
  // so it does not. This is the line between the two.
  it("leaves a staged lifecycle status alone, and an explicit save takes it", async () => {
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" })],
      activeTab: "a",
      stagedLifecycle: { a: { status: "in_progress", note: "開工" } },
    });
    const commit = vi.fn(async () => null);
    useApp.setState({ commitStagedLifecycle: commit });

    await useApp.getState().saveTab("a", { auto: true });
    expect(commit).not.toHaveBeenCalled();

    useApp.setState({ tabs: [tab("a", { dirty: true, content: "draft a2" })] });
    await useApp.getState().saveTab("a");
    expect(commit).toHaveBeenCalledWith("a");
  });

  // Losing somebody's paragraph to a conflict they did not expect is the worst
  // outcome this feature can produce, so the editor buffer is never touched and
  // the text is parked server-side the moment the conflict appears.
  it("keeps the text and parks a draft when a background save conflicts", async () => {
    vi.mocked(api.putNode).mockRejectedValue(new ConflictError("rev-9", "別人的版本"));
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "我的段落" })],
      activeTab: "a",
      operations: {},
    });

    await useApp.getState().saveTab("a", { auto: true });

    const saved = useApp.getState().tabs[0];
    expect(saved.content).toBe("我的段落");
    expect(saved.dirty).toBe(true);
    expect(saved.conflict).toEqual({ diskRev: "rev-9", diskContent: "別人的版本" });
    expect(api.putDraft).toHaveBeenCalledWith("a", "我的段落");
    expect(useApp.getState().operations["document:a"]).toMatchObject({
      status: "conflict",
    });
  });

  // And it stops trying. Retrying a conflict in the background either keeps
  // failing or, worse, wins against a revision nobody agreed to.
  it("stops auto-saving a document that is waiting on a conflict decision", async () => {
    useApp.setState({
      tabs: [
        tab("a", {
          dirty: true,
          content: "我的段落",
          conflict: { diskRev: "rev-9", diskContent: "別人的版本" },
        }),
      ],
      activeTab: "a",
    });

    await useApp.getState().saveTab("a", { auto: true });
    expect(api.putNode).not.toHaveBeenCalled();

    // The user pressing Ctrl + S is a different matter: that is a decision.
    await useApp.getState().saveTab("a");
    expect(api.putNode).toHaveBeenCalledTimes(1);
  });

  // A failed save leaves the work outstanding rather than pretending it landed,
  // which is what lets the editor's retry loop mean something.
  it("surfaces a failed save and keeps the document dirty for the retry", async () => {
    vi.mocked(api.putNode).mockRejectedValue(new Error("磁碟已滿"));
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" })],
      activeTab: "a",
      operations: {},
    });

    await expect(useApp.getState().saveTab("a", { auto: true })).rejects.toThrow(
      "磁碟已滿",
    );

    expect(useApp.getState().tabs[0].dirty).toBe(true);
    expect(useApp.getState().operations["document:a"]).toMatchObject({
      status: "error",
      message: "磁碟已滿",
    });
    // Not on disk, so it goes somewhere the user can get it back from.
    expect(api.putDraft).toHaveBeenCalledWith("a", "draft a");
  });

  // The server tells every client about a change, including the one that made
  // it. Reacting to our own echo would cost a read per save and would flag the
  // document as externally modified against its own author.
  it("ignores the disk-change broadcast caused by its own save", async () => {
    useApp.setState({
      tabs: [tab("a", { dirty: true, content: "draft a" })],
      activeTab: "a",
    });

    await useApp.getState().saveTab("a");
    await useApp.getState().notifyDiskChange("a");

    expect(api.getNode).not.toHaveBeenCalled();
    expect(useApp.getState().tabs[0].diskChanged).toBe(false);

    // Somebody else's write is a different revision, and is still read back.
    vi.mocked(api.getNode).mockResolvedValue({
      id: "a",
      content: "別人的版本",
      rev: "rev-9",
    });
    await useApp.getState().notifyDiskChange("a");
    expect(api.getNode).toHaveBeenCalledTimes(1);
    expect(useApp.getState().tabs[0].content).toBe("別人的版本");
  });
});
