import { beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../store";
import { operationScope } from "./operations";
import { resyncAfterReconnect } from "./reconnect";

// The server was stopped and started again under an open page. The socket came
// back and live updates resumed, but the "can't reach the server" pill and
// banner stayed up for good, and whatever changed while the socket was down
// (a graph edit, a new project) never reached the page.
describe("resyncAfterReconnect", () => {
  const loadAll = vi.fn(async () => undefined);
  const refreshState = vi.fn(async () => undefined);
  const refreshTrash = vi.fn(async () => undefined);
  const refreshProjects = vi.fn(async () => undefined);

  beforeEach(() => {
    vi.clearAllMocks();
    useApp.setState({
      loadAll,
      refreshState,
      refreshTrash,
      refreshProjects,
      error: "無法連線到伺服器",
      operations: {
        workspace: { status: "error", operation: "project.switch", message: "無法連線到伺服器", at: 1 },
        [operationScope.node("a")]: { status: "error", operation: "node.save", message: "標題不可空白", at: 2 },
        [operationScope.document("b")]: { status: "conflict", operation: "document.save", message: "Failed to fetch", at: 3 },
      },
      lastOperation: { scope: "workspace", status: "error", operation: "project.switch", message: "無法連線到伺服器", at: 1 },
    });
  });

  it("drops the connection failures, keeps every other problem, and reloads", async () => {
    await resyncAfterReconnect();

    const state = useApp.getState();
    expect(state.error).toBeNull();
    expect(state.operations.workspace).toBeUndefined();
    expect(state.lastOperation).toBeNull();
    // Not about the connection: still the user's to see.
    expect(state.operations["node:a"]).toMatchObject({ status: "error" });
    // A conflict is a question, never dismissed on someone's behalf.
    expect(state.operations["document:b"]).toMatchObject({ status: "conflict" });

    expect(loadAll).toHaveBeenCalledTimes(1);
    expect(refreshState).toHaveBeenCalledTimes(1);
    expect(refreshTrash).toHaveBeenCalledTimes(1);
    expect(refreshProjects).toHaveBeenCalledTimes(1);
  });

  it("leaves an unrelated error banner alone", async () => {
    useApp.setState({ error: "匯入失敗：檔案格式不符" });
    await resyncAfterReconnect();
    expect(useApp.getState().error).toBe("匯入失敗：檔案格式不符");
  });
});
