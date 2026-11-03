import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../../store";
import { openNodeDetail, useNodeDetailView } from "./openNodeDetail";

const realOpenTab = useApp.getState().openTab;

beforeEach(() => {
  useApp.setState({ activeProject: "p" });
  useNodeDetailView.getState().clearRequest();
});

afterEach(() => {
  useApp.setState({ openTab: realOpenTab });
  vi.restoreAllMocks();
});

describe("openNodeDetail's request", () => {
  it("is dropped when the node fails to open", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    useApp.setState({ openTab: vi.fn(() => Promise.reject(new Error("gone"))) });
    openNodeDetail("a", "dependencies");
    expect(useNodeDetailView.getState().request).toMatchObject({ nodeId: "a", view: "dependencies" });
    await vi.waitFor(() => expect(useNodeDetailView.getState().request).toBeNull());
  });

  it("keeps a newer request when an older open fails", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    let reject: (error: Error) => void = () => {};
    useApp.setState({
      openTab: vi.fn(() => new Promise<void>((_, no) => (reject = no))),
    });
    openNodeDetail("a", "dependencies");
    const failFirst = reject;
    openNodeDetail("b", "activity");
    failFirst(new Error("gone"));
    await Promise.resolve();
    await Promise.resolve();
    expect(useNodeDetailView.getState().request).toMatchObject({ nodeId: "b", view: "activity" });
  });

  it("is dropped when the project changes", () => {
    useApp.setState({ openTab: vi.fn(() => new Promise<void>(() => {})) });
    openNodeDetail("a", "activity");
    expect(useNodeDetailView.getState().request).not.toBeNull();
    useApp.setState({ activeProject: "q" });
    expect(useNodeDetailView.getState().request).toBeNull();
  });
});
