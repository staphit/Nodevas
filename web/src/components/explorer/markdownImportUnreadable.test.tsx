import { act, render, renderHook, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { translate } from "../../i18n";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { ProjectTransferBar } from "./ProjectTransferBar";
import { useProjectTransfer } from "./useProjectTransfer";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return { ...actual, api: { ...actual.api, createNode: vi.fn() } };
});

const UNREADABLE =
  "graph.yaml：第 40 行有錯誤。修正檔案後重新載入。（yaml: line 40: did not find expected key）";

beforeEach(() => {
  vi.clearAllMocks();
  writePreference("language", "zh-TW");
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "daily",
    projects: [{ name: "daily", label: "daily", depth: 0, path: "daily", nodes: 3 }],
    graph: null,
    graphUnreadable: null,
    tabs: [],
    pageDocs: {},
    error: null,
    loadAll: vi.fn(async () => undefined),
  });
});

afterEach(() => {
  writePreference("language", "en");
});

function useTransfer(notice: (text: string | null) => void) {
  return useProjectTransfer({
    graph: null,
    activeProject: "daily",
    expandedProjects: new Set(),
    persistExpandedProjects: () => undefined,
    setProjectTransferNotice: notice,
  });
}

function Bar() {
  const transfer = useTransfer(() => undefined);
  return (
    <ProjectTransferBar
      transfer={transfer}
      activeProject="daily"
      setProjectTransferNotice={() => undefined}
      openSaveAs={() => undefined}
      saveAsBusy={false}
    />
  );
}

/** The Import menu's items; the Export menu has a JSON Canvas item too. */
function importItem(container: HTMLElement, label: string): HTMLButtonElement {
  const menu = container.querySelector<HTMLElement>(".transfer-menu")!;
  return within(menu).getByRole("menuitem", { name: new RegExp(label) }) as HTMLButtonElement;
}

function markdownFile(): FileList {
  // jsdom's File has no text(); the import reads the file through it.
  const file = Object.assign(new File(["# Imported\n"], "import-me.md", { type: "text/markdown" }), {
    text: async () => "# Imported\n",
  });
  return Object.assign([file], { item: () => file }) as unknown as FileList;
}

describe("importing Markdown while graph.yaml cannot be read", () => {
  // Creating a node writes graph.yaml, the file the reader has to fix; the
  // board hides New node for the same reason.
  it("offers no node import from the transfer bar", () => {
    useApp.setState({ graphUnreadable: UNREADABLE });
    const { container } = render(<Bar />);
    expect(importItem(container, translate("explorer.markdownFiles", "zh-TW")).disabled).toBe(true);
    expect(importItem(container, translate("explorer.jsonCanvas", "zh-TW")).disabled).toBe(true);
  });

  it("still offers it while the file reads", () => {
    const { container } = render(<Bar />);
    expect(importItem(container, translate("explorer.markdownFiles", "zh-TW")).disabled).toBe(false);
    expect(importItem(container, translate("explorer.jsonCanvas", "zh-TW")).disabled).toBe(false);
  });

  it("writes nothing and says why, without the board's reason in the error bar", async () => {
    useApp.setState({ graphUnreadable: UNREADABLE });
    const notice = vi.fn();
    const { result } = renderHook(() => useTransfer(notice));
    await act(async () => {
      await result.current.importMarkdownFiles(markdownFile());
    });
    expect(api.createNode).not.toHaveBeenCalled();
    expect(notice).toHaveBeenLastCalledWith(translate("explorer.importUnreadable", "zh-TW"));
    expect(useApp.getState().error).toBeNull();
  });
});

describe("a Markdown file the hub refuses", () => {
  it("names the file with the reader's punctuation", async () => {
    vi.mocked(api.createNode).mockRejectedValue(new Error("節點已存在"));
    const notice = vi.fn();
    const { result } = renderHook(() => useTransfer(notice));
    await act(async () => {
      await result.current.importMarkdownFiles(markdownFile());
    });
    expect(useApp.getState().error).toBe("Markdown 匯入失敗：import-me.md：節點已存在");
  });
});
