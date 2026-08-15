import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { ConfirmDialogHost } from "../ConfirmDialog";
import { MilestoneTypeEditor } from "./MilestoneTypeEditor";

const REVIEW = { id: "custom-review" as const, label: "內審" };

const updateWorkflowDefinition = vi.fn();

function open(options: { scheduled?: boolean } = {}) {
  useApp.setState({
    graph: {
      version: 1,
      nodes: [{ id: "a", title: "設計稿" }],
      edges: [],
      ui: {
        planStatuses: [REVIEW],
        ...(options.scheduled
          ? { plans: { a: [{ status: REVIEW.id, date: "2026-01-01" }] } }
          : {}),
      },
    },
    updateWorkflowDefinition,
  });
  const notify = { onError: vi.fn(), onNotice: vi.fn() };
  render(
    <>
      <MilestoneTypeEditor notify={notify} />
      <ConfirmDialogHost />
    </>,
  );
  return { notify, user: userEvent.setup() };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
  useApp.getState().updateUIPreference("language", "zh-TW");
  updateWorkflowDefinition.mockReset().mockResolvedValue({ ok: true });
});

describe("MilestoneTypeEditor", () => {
  it("adds a type from the name that was typed and clears the box", async () => {
    const { user } = open();

    await user.type(screen.getByLabelText("新里程碑名稱"), "外審");
    await user.click(screen.getByRole("button", { name: "新增類型" }));

    expect(updateWorkflowDefinition).toHaveBeenCalledWith({
      type: "workflow.addMilestoneType",
      label: "外審",
    });
    await waitFor(() => expect(screen.getByLabelText("新里程碑名稱")).toHaveValue(""));
  });

  it("refuses a blank name instead of adding one", async () => {
    const { notify, user } = open();

    await user.click(screen.getByRole("button", { name: "新增類型" }));

    expect(notify.onError).toHaveBeenCalledWith("請輸入里程碑名稱。");
    expect(updateWorkflowDefinition).not.toHaveBeenCalled();
  });

  // Deleting a type that is in use silently drops scheduled dates, so the
  // choice between dropping and keeping them is asked, never assumed.
  it("asks before removing a type that still has milestones scheduled", async () => {
    const { user } = open({ scheduled: true });

    await user.click(screen.getByRole("button", { name: "刪除 內審" }));

    expect(await screen.findByText("刪除里程碑類型「內審」")).toBeInTheDocument();
    expect(updateWorkflowDefinition).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "一併刪除排程" }));

    await waitFor(() =>
      expect(updateWorkflowDefinition).toHaveBeenCalledWith({
        type: "workflow.removeMilestoneType",
        id: REVIEW.id,
        removeScheduled: true,
      }),
    );
  });

  // Backing out of that question keeps the schedule; the definition still goes.
  it("keeps the scheduled milestones when the question is cancelled", async () => {
    const { user } = open({ scheduled: true });

    await user.click(screen.getByRole("button", { name: "刪除 內審" }));
    await user.click(await screen.findByRole("button", { name: "取消" }));

    await waitFor(() =>
      expect(updateWorkflowDefinition).toHaveBeenCalledWith({
        type: "workflow.removeMilestoneType",
        id: REVIEW.id,
      }),
    );
  });

  it("removes an unused type without asking", async () => {
    const { user } = open();

    await user.click(screen.getByRole("button", { name: "刪除 內審" }));

    await waitFor(() =>
      expect(updateWorkflowDefinition).toHaveBeenCalledWith({
        type: "workflow.removeMilestoneType",
        id: REVIEW.id,
        removeScheduled: false,
      }),
    );
  });
});
