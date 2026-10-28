import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError, setProjectOverride } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return { ...actual, api: { ...actual.api, openProject: vi.fn() } };
});

// What the hub answers when graph.yaml does not parse (project.go wraps the
// store's "graph.yaml: ..." in "load project %q: ...").
const PARSE_FAILURE =
  'load project "Disk project": graph.yaml: yaml: unmarshal errors:\n  line 16: field start not found in type engine.Node';

beforeEach(() => {
  vi.clearAllMocks();
  setProjectOverride("");
  vi.mocked(api.openProject).mockRejectedValue(new RequestError(400, PARSE_FAILURE));
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "Alpha",
    projects: [
      { name: "Alpha", label: "Alpha", depth: 0, path: "Alpha", nodes: 1 },
      {
        name: "Disk project",
        label: "Disk project",
        depth: 0,
        path: "Disk project",
        nodes: 3,
      },
    ],
    tabs: [],
    pageDocs: {},
    activeTab: null,
    operations: {},
    lastOperation: null,
  });
});

async function switchFailure(): Promise<Error> {
  try {
    await useApp.getState().switchProject("Disk project");
  } catch (error) {
    return error as Error;
  }
  throw new Error("switchProject did not fail");
}

describe("switching to a project whose graph.yaml does not parse", () => {
  it("explains it once, in the reader's language, with the line to fix", async () => {
    const error = await switchFailure();
    expect(error.message).not.toMatch(/unmarshal|not found in type|load project/);
    expect(error.message).toMatch(/[一-鿿]/);
    expect(error.message).toContain("Disk project");
    expect(error.message).toContain("graph.yaml");
    expect(error.message).toContain("16");
  });

  it("keeps the top bar's badge to the short status, not the explanation again", async () => {
    await switchFailure();
    const last = useApp.getState().lastOperation;
    expect(last?.status).toBe("error");
    expect(last?.message).toBe(translate("workspace.error.switchFailed", "zh-TW"));
  });

  it("says so in English too, without the parser's words", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const error = await switchFailure();
    expect(error.message).not.toMatch(/unmarshal|not found in type/);
    expect(error.message).toContain("graph.yaml");
    expect(error.message).toContain("16");
  });

  it("still reports any other refusal as the hub worded it", async () => {
    vi.mocked(api.openProject).mockRejectedValue(new RequestError(400, "disk full"));
    const error = await switchFailure();
    expect(error.message).toBe("disk full");
    expect(useApp.getState().lastOperation?.message).toBe("disk full");
  });
});

// graph.yaml parses but the hub's validation refuses it (store/graph_cache.go
// wraps ValidateGraphForStorage's reason in "graph.yaml: ..."). The file is
// readable; what the reader needs is which node and field to fix.
const INVALID_PRIORITY = 'load project "Disk project": graph.yaml: node "c" has invalid priority "zzz"';
const NOTE_ACCEPTANCE =
  'load project "Disk project": graph.yaml: node "c": type "note" cannot carry acceptance criteria';

describe("switching to a project whose graph.yaml fails validation", () => {
  it("names the node and the field to fix, in the reader's language", async () => {
    vi.mocked(api.openProject).mockRejectedValue(new RequestError(400, INVALID_PRIORITY));
    const error = await switchFailure();
    expect(error.message).not.toContain(
      translate("workspace.error.graphUnreadable", "zh-TW", {
        name: "Disk project",
      }),
    );
    expect(error.message).not.toMatch(/無法讀取|invalid priority|load project/);
    expect(error.message).toContain("Disk project");
    expect(error.message).toContain("graph.yaml");
    expect(error.message).toContain("節點「c」的優先度「zzz」無效");
  });

  it("says why a note cannot be opened with acceptance criteria", async () => {
    vi.mocked(api.openProject).mockRejectedValue(new RequestError(400, NOTE_ACCEPTANCE));
    const error = await switchFailure();
    expect(error.message).not.toMatch(/無法讀取|cannot carry|load project/);
    expect(error.message).toContain("節點「c」");
    expect(error.message).toContain("「note」");
  });

  it("keeps the hub's reason in English, without calling the file unreadable", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    vi.mocked(api.openProject).mockRejectedValue(new RequestError(400, INVALID_PRIORITY));
    const error = await switchFailure();
    expect(error.message).not.toMatch(/can't be read|load project/);
    expect(error.message).toContain('node "c" has invalid priority "zzz"');
    expect(error.message).toContain("Disk project");
  });

  it("keeps a reason it has no words for as the hub wrote it", async () => {
    vi.mocked(api.openProject).mockRejectedValue(
      new RequestError(400, 'load project "Disk project": graph.yaml: node "c" has too many plan milestones'),
    );
    const error = await switchFailure();
    expect(error.message).not.toMatch(/無法讀取/);
    expect(error.message).toContain('node "c" has too many plan milestones');
  });

  it("keeps the badge to the short status", async () => {
    vi.mocked(api.openProject).mockRejectedValue(new RequestError(400, INVALID_PRIORITY));
    await switchFailure();
    expect(useApp.getState().lastOperation?.message).toBe(translate("workspace.error.switchFailed", "zh-TW"));
  });
});
