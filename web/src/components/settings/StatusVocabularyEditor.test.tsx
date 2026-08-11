import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../../store";
import type { StatusDefinition } from "../../types";
import { ConfirmDialogHost } from "../ConfirmDialog";
import { StatusVocabularyEditor } from "./StatusVocabularyEditor";

const SHARED: StatusDefinition = {
  id: "custom-status-1",
  label: "審核中",
  color: "#8b7cf6",
  shape: "circle",
};
const LOCAL: StatusDefinition = {
  id: "custom-status-2",
  label: "封存",
  color: "#445566",
  shape: "square",
};

const saveWorkspaceStatuses = vi.fn<(next: StatusDefinition[]) => Promise<void>>();
const updateWorkflowDefinition = vi.fn();

function seed(customStatuses: StatusDefinition[], workspaceStatuses: StatusDefinition[]) {
  useApp.setState({
    graph: {
      version: 1,
      nodes: [],
      edges: [],
      ui: { customStatuses },
    },
    runState: { nodes: {}, history: [] },
    statuses: {},
    workspaceStatuses,
    saveWorkspaceStatuses,
    updateWorkflowDefinition,
  });
}

function open(
  customStatuses: StatusDefinition[] = [],
  workspaceStatuses: StatusDefinition[] = [],
) {
  seed(customStatuses, workspaceStatuses);
  const notify = { onError: vi.fn(), onNotice: vi.fn() };
  render(
    <>
      <StatusVocabularyEditor notify={notify} />
      <ConfirmDialogHost />
    </>,
  );
  return { notify, user: userEvent.setup() };
}

beforeEach(() => {
  saveWorkspaceStatuses.mockReset().mockResolvedValue(undefined);
  updateWorkflowDefinition.mockReset().mockResolvedValue({ ok: true });
});

describe("StatusVocabularyEditor", () => {
  // A new state is deliberately workspace-wide: the per-project state is what
  // made people retype the same vocabulary in every project.
  it("saves a new state to the workspace file with everything that was typed", async () => {
    const { user } = open();

    await user.type(screen.getByLabelText("新狀態名稱"), "待驗收");
    fireEvent.change(screen.getByLabelText("新狀態顏色"), {
      target: { value: "#112233" },
    });
    await user.selectOptions(screen.getByLabelText("新狀態圖形"), "diamond");
    await user.click(screen.getByLabelText("新狀態視為完結"));
    await user.click(screen.getByRole("button", { name: "新增狀態" }));

    expect(saveWorkspaceStatuses).toHaveBeenCalledWith([
      {
        id: "custom-status-1",
        label: "待驗收",
        color: "#112233",
        shape: "diamond",
        settled: true,
      },
    ]);
    // The box empties only on success, so the next name starts from nothing.
    await waitFor(() => expect(screen.getByLabelText("新狀態名稱")).toHaveValue(""));
  });

  it("refuses a blank name instead of saving one", async () => {
    const { notify, user } = open();

    await user.click(screen.getByRole("button", { name: "新增狀態" }));

    expect(notify.onError).toHaveBeenCalledWith("請輸入狀態名稱。");
    expect(saveWorkspaceStatuses).not.toHaveBeenCalled();
  });

  // Two states with the same name are indistinguishable on the board.
  it("refuses a name that already exists", async () => {
    const { notify, user } = open([SHARED], [SHARED]);

    await user.type(screen.getByLabelText("新狀態名稱"), "審核中");
    await user.click(screen.getByRole("button", { name: "新增狀態" }));

    expect(notify.onError).toHaveBeenCalledWith("已存在名稱「審核中」。");
    expect(saveWorkspaceStatuses).not.toHaveBeenCalled();
  });

  // The merged list the board reads hides which file owns a definition, so the
  // edit has to route itself.
  it("edits a shared state through the workspace file", async () => {
    open([SHARED, LOCAL], [SHARED]);

    fireEvent.change(screen.getByLabelText("審核中 顏色"), {
      target: { value: "#ff0000" },
    });

    await waitFor(() =>
      expect(saveWorkspaceStatuses).toHaveBeenCalledWith([
        { ...SHARED, color: "#ff0000" },
      ]),
    );
    expect(updateWorkflowDefinition).not.toHaveBeenCalled();
  });

  it("edits a project-only state through the graph command", async () => {
    open([SHARED, LOCAL], [SHARED]);

    fireEvent.change(screen.getByLabelText("封存 顏色"), {
      target: { value: "#00ff00" },
    });

    await waitFor(() =>
      expect(updateWorkflowDefinition).toHaveBeenCalledWith({
        type: "workflow.updateLifecycleStatus",
        id: "custom-status-2",
        patch: { color: "#00ff00" },
      }),
    );
    expect(saveWorkspaceStatuses).not.toHaveBeenCalled();
  });

  // Deleting never rewrites the journal, so the count has to be said out loud
  // before anything happens.
  it("deletes only after the confirmation is accepted", async () => {
    const { user } = open([SHARED], [SHARED]);

    await user.click(screen.getByRole("button", { name: "刪除 審核中" }));
    expect(await screen.findByText("刪除實際狀態「審核中」")).toBeInTheDocument();
    expect(saveWorkspaceStatuses).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "刪除定義" }));

    await waitFor(() => expect(saveWorkspaceStatuses).toHaveBeenCalledWith([]));
  });
});
