import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { translate } from "../../i18n";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { useProjectTransfer } from "./useProjectTransfer";

vi.mock("../ConfirmDialog", () => ({
  confirmAction: vi.fn(async () => true),
}));

// The hub refuses an archive of a project whose graph.yaml does not read with
// a 422 and its own sentence (store/graph_unreadable.go), before any byte of
// the archive is sent. A plain <a download> never sees that answer, so the
// "Exporting …" notice used to stay up for good.
const REFUSAL = "graph.yaml：第 9 行有錯誤。修正檔案後重新載入。（yaml: line 9: did not find expected key）";

let clicked: HTMLAnchorElement[] = [];

beforeEach(() => {
  clicked = [];
  writePreference("language", "zh-TW");
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "daily",
    projects: [{ name: "daily", label: "daily", depth: 0, path: "daily", nodes: 3 }],
  });
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push(this);
  });
  Object.assign(URL, {
    createObjectURL: vi.fn(() => "blob:archive"),
    revokeObjectURL: vi.fn(),
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  writePreference("language", "en");
});

function renderTransfer() {
  const notice = vi.fn();
  const { result } = renderHook(() =>
    useProjectTransfer({
      graph: null,
      activeProject: "daily",
      expandedProjects: new Set(),
      persistExpandedProjects: () => undefined,
      setProjectTransferNotice: notice,
    }),
  );
  return { result, notice };
}

function lastNotice(notice: ReturnType<typeof vi.fn>): string {
  return String(notice.mock.calls.at(-1)?.[0]);
}

describe("exporting a project archive", () => {
  it("says the export failed, in the hub's words, when the hub refuses it", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: REFUSAL }), {
        status: 422,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const { result, notice } = renderTransfer();
    await act(async () => {
      await result.current.exportProjectArchive();
    });
    expect(lastNotice(notice)).not.toBe(translate("explorer.exportingArchive", "zh-TW", { target: "daily" }));
    expect(lastNotice(notice)).toContain(REFUSAL);
    expect(lastNotice(notice)).toBe(translate("explorer.archiveExportFailed", "zh-TW", { error: REFUSAL }));
    // Nothing is saved: the refusal is not handed to the browser as a file.
    expect(clicked).toHaveLength(0);
  });

  it("says so for the whole-workspace bundle too", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: REFUSAL }), { status: 422 }),
    );
    const { result, notice } = renderTransfer();
    await act(async () => {
      await result.current.exportProjectArchive({ name: ".", label: "Workspace" });
    });
    const url = new URL(String(fetchMock.mock.calls[0][0]), "http://hub.test");
    expect(url.pathname).toBe("/api/projects/export");
    expect(url.searchParams.get("project")).toBe(".");
    expect(url.searchParams.get("language")).toBe("zh-TW");
    expect(lastNotice(notice)).toContain(REFUSAL);
  });

  it("saves the archive under the hub's file name and says it is done", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(new Blob(["PK"]), {
        status: 200,
        headers: { "Content-Disposition": 'attachment; filename="daily.veproj"' },
      }),
    );
    const { result, notice } = renderTransfer();
    await act(async () => {
      await result.current.exportProjectArchive();
    });
    expect(clicked).toHaveLength(1);
    expect(clicked[0].download).toBe("daily.veproj");
    expect(clicked[0].href).toBe("blob:archive");
    expect(lastNotice(notice)).toBe(translate("explorer.exportedFile", "zh-TW", { name: "daily.veproj" }));
  });
});
