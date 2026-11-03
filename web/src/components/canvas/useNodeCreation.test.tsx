import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { LaneContextMenu } from "../LaneView";
import { useNodeCreation } from "./useNodeCreation";

// The toast after creating a node named it by its generated id ("節點
// node-0001 已建立。"), which the reader never typed and the board never shows.
// It names the node by the title it was just given.

const MENU: Extract<LaneContextMenu, { kind: "node-create" }> = {
  kind: "node-create",
  x: 40,
  y: 60,
  col: 3,
  row: 5,
};

function setup(language: "en" | "zh-TW") {
  useApp.setState({ preferences: { ...readPreferences(), language } });
  const setGraphNotice = vi.fn();
  const createNode = vi.fn(async () => "node-0001");
  const { result } = renderHook(() =>
    useNodeCreation({
      planStatusDefinitions: [],
      createNode,
      setLifecycleStatus: async () => ({ ok: true }),
      setGraphNotice,
      setContextMenu: () => undefined,
    }),
  );
  return { result, setGraphNotice };
}

async function create(
  result: ReturnType<typeof setup>["result"],
  title: string,
  plannedStart?: string,
) {
  act(() => result.current.setNodeCreateTitle(title));
  if (plannedStart) act(() => result.current.setNodePlanDates({ started: plannedStart }));
  await act(async () => {
    await result.current.addGraphNode(MENU);
  });
}

describe("the node-created notice", () => {
  afterEach(() => {
    useApp.setState({ preferences: readPreferences() });
  });

  it("names the node by its title in zh-TW", async () => {
    const { result, setGraphNotice } = setup("zh-TW");
    await create(result, "第一章");
    expect(setGraphNotice).toHaveBeenLastCalledWith({ text: "已建立節點「第一章」。", kind: "ok" });
  });

  it("names the node by its title in English", async () => {
    const { result, setGraphNotice } = setup("en");
    await create(result, "Chapter one");
    expect(setGraphNotice).toHaveBeenLastCalledWith({
      text: "Created node “Chapter one”.",
      kind: "ok",
    });
  });

  it("names it by title with its planned dates too", async () => {
    const { result, setGraphNotice } = setup("zh-TW");
    await create(result, "第一章", "2026-11-02");
    const notice = setGraphNotice.mock.lastCall?.[0] as { text: string };
    expect(notice.text).toBe("已建立節點「第一章」，含 1 個預期日期。");
    expect(notice.text).not.toContain("node-0001");
  });

  it("falls back to the id for a node created without a title", async () => {
    const { result, setGraphNotice } = setup("en");
    await create(result, "   ");
    expect(setGraphNotice).toHaveBeenLastCalledWith({
      text: "Created node “node-0001”.",
      kind: "ok",
    });
  });
});
