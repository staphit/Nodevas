import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import {
  ColorField,
  EmptyState,
  InlineNotice,
  OperationStatus,
  ResizeHandle,
} from "./InteractionPrimitives";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
  useApp.getState().updateUIPreference("language", "zh-TW");
});

function renderHandle(overrides: Partial<Parameters<typeof ResizeHandle>[0]> = {}) {
  const onChange = vi.fn();
  const onCommit = vi.fn();
  const onReset = vi.fn();
  render(
    <ResizeHandle
      orientation="vertical"
      value={300}
      min={200}
      max={520}
      step={10}
      largeStep={50}
      label="調整專案總管寬度"
      onChange={onChange}
      onCommit={onCommit}
      onReset={onReset}
      {...overrides}
    />,
  );
  return { onChange, onCommit, onReset, handle: screen.getByRole("separator") };
}

describe("ResizeHandle", () => {
  it("exposes its range to assistive tech", () => {
    const { handle } = renderHandle({ valueText: (value) => `${value} 像素` });
    expect(handle).toHaveAttribute("aria-orientation", "vertical");
    expect(handle).toHaveAttribute("aria-valuemin", "200");
    expect(handle).toHaveAttribute("aria-valuemax", "520");
    expect(handle).toHaveAttribute("aria-valuenow", "300");
    expect(handle).toHaveAttribute("aria-valuetext", "300 像素");
  });

  it("is reachable and adjustable from the keyboard", async () => {
    const user = userEvent.setup();
    const { onChange, onCommit, handle } = renderHandle();

    await user.tab();
    expect(handle).toHaveFocus();

    await user.keyboard("{ArrowRight}");
    expect(onChange).toHaveBeenLastCalledWith(310);
    // Keyboard adjustments persist immediately: there is no drag end to wait for.
    expect(onCommit).toHaveBeenLastCalledWith(310);

    await user.keyboard("{Shift>}{ArrowLeft}{/Shift}");
    expect(onChange).toHaveBeenLastCalledWith(250);
  });

  it("clamps to the declared range", async () => {
    const user = userEvent.setup();
    const { onChange, handle } = renderHandle();
    handle.focus();

    await user.keyboard("{Home}");
    expect(onChange).toHaveBeenLastCalledWith(200);
    await user.keyboard("{End}");
    expect(onChange).toHaveBeenLastCalledWith(520);
  });

  it("resets on double click", async () => {
    const user = userEvent.setup();
    const { onReset, handle } = renderHandle();
    await user.dblClick(handle);
    expect(onReset).toHaveBeenCalledTimes(1);
  });

  it("is inert when disabled", async () => {
    const user = userEvent.setup();
    const { onChange, onReset, handle } = renderHandle({ disabled: true });
    expect(handle).toHaveAttribute("tabindex", "-1");
    await user.dblClick(handle);
    handle.focus();
    await user.keyboard("{ArrowRight}");
    expect(onChange).not.toHaveBeenCalled();
    expect(onReset).not.toHaveBeenCalled();
  });
});

describe("OperationStatus", () => {
  it("renders nothing while idle", () => {
    const { container } = render(<OperationStatus status="idle" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("announces a failure as an alert and shows the reason", () => {
    render(<OperationStatus status="error" message="磁碟已滿" />);
    const status = screen.getByRole("alert");
    expect(status).toHaveTextContent("磁碟已滿");
  });

  it("reports progress politely", () => {
    render(<OperationStatus status="pending" />);
    expect(screen.getByRole("status")).toHaveTextContent("儲存中");
  });

  it("names a conflict so it is not read as a plain error", () => {
    render(<OperationStatus status="conflict" />);
    expect(screen.getByRole("alert")).toHaveTextContent("版本衝突");
  });
});

describe("InlineNotice and EmptyState", () => {
  it("gives errors an assertive role and others a polite one", () => {
    const { rerender } = render(<InlineNotice kind="error">壞掉了</InlineNotice>);
    expect(screen.getByRole("alert")).toHaveTextContent("壞掉了");
    rerender(<InlineNotice kind="success">好了</InlineNotice>);
    expect(screen.getByRole("status")).toHaveTextContent("好了");
  });

  it("offers the action that fills the empty view", async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn();
    render(
      <EmptyState
        title="尚無節點"
        description="建立第一個節點後即可安排規劃。"
        action={
          <button type="button" onClick={onCreate}>
            新增節點
          </button>
        }
      />,
    );
    await user.click(screen.getByRole("button", { name: "新增節點" }));
    expect(onCreate).toHaveBeenCalledTimes(1);
  });
});

describe("ColorField", () => {
  it("previews every step but saves only when the picker closes", () => {
    const onCommit = vi.fn();
    const onPreview = vi.fn();
    render(
      <ColorField
        value="#111111"
        label="底色"
        onCommit={onCommit}
        onPreview={onPreview}
      />,
    );
    const field = screen.getByLabelText("底色");

    // Dragging around the picker: React's onChange is the `input` event.
    fireEvent.input(field, { target: { value: "#222222" } });
    fireEvent.input(field, { target: { value: "#333333" } });
    fireEvent.input(field, { target: { value: "#444444" } });
    expect(onPreview).toHaveBeenCalledTimes(3);
    expect(onCommit).not.toHaveBeenCalled();

    fireEvent.change(field);
    expect(onCommit).toHaveBeenCalledTimes(1);
    expect(onCommit).toHaveBeenCalledWith("#444444");
  });

  it("saves on blur, for the browser that never fires change", () => {
    const onCommit = vi.fn();
    render(<ColorField value="#111111" label="底色" onCommit={onCommit} />);
    const field = screen.getByLabelText("底色");

    fireEvent.input(field, { target: { value: "#abcdef" } });
    fireEvent.blur(field);
    expect(onCommit).toHaveBeenCalledWith("#abcdef");
  });

  it("does not save a colour that did not change", () => {
    const onCommit = vi.fn();
    render(<ColorField value="#111111" label="底色" onCommit={onCommit} />);
    const field = screen.getByLabelText("底色");

    fireEvent.input(field, { target: { value: "#222222" } });
    fireEvent.input(field, { target: { value: "#111111" } });
    fireEvent.change(field);
    expect(onCommit).not.toHaveBeenCalled();
  });
});
