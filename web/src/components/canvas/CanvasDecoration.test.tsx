import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { CanvasDecoration } from "./CanvasDecoration";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
  useApp.getState().updateUIPreference("language", "zh-TW");
});

describe("CanvasDecoration color control", () => {
  it("uses one native color picker and saves immediately", () => {
    const onChange = vi.fn();
    render(
      <CanvasDecoration
        kind="group"
        item={{
          id: "group-1",
          title: "群組",
          x: 0,
          y: 0,
          width: 320,
          height: 180,
          color: "#31566a",
        }}
        offsetX={0}
        offsetY={0}
        zoom={1}
        onChange={onChange}
        onDelete={vi.fn()}
      />,
    );

    const picker = screen.getByLabelText("選擇群組顏色");
    expect(picker).toHaveAttribute("type", "color");
    expect(screen.getAllByRole("button")).toHaveLength(1);

    fireEvent.change(picker, { target: { value: "#123456" } });
    expect(onChange).toHaveBeenCalledWith({ color: "#123456" });
  });
});

describe("CanvasDecoration alignment", () => {
  const item = {
    id: "group-1",
    title: "群組",
    x: 100,
    y: 100,
    width: 320,
    height: 180,
    color: "#31566a",
  };
  const neighbour = {
    id: "card-1",
    rect: { left: 504, right: 604, top: 900, bottom: 950 },
  };

  // jsdom has no PointerEvent, and a plain fired event drops button/clientX
  // with it. A MouseEvent carries them, and React dispatches on the type name.
  function pointer(type: string, init: MouseEventInit) {
    return new MouseEvent(type, { bubbles: true, button: 0, ...init });
  }

  function drag(props: Record<string, unknown>, dx: number, extra: MouseEventInit = {}) {
    const onChange = vi.fn();
    render(
      <CanvasDecoration
        kind="group"
        item={item}
        offsetX={0}
        offsetY={0}
        zoom={1}
        onChange={onChange}
        onDelete={vi.fn()}
        {...props}
      />,
    );
    const header = screen.getByText("群組").parentElement!;
    fireEvent(header, pointer("pointerdown", { clientX: 0, clientY: 0 }));
    fireEvent(header, pointer("pointerup", { clientX: dx, clientY: 0, ...extra }));
    return onChange;
  }

  it("pulls the box onto a neighbour it is nearly lined up with", () => {
    // 100 + 400 = 500, four short of the card's left edge at 504.
    expect(drag({ snapEnabled: true, snapBoxes: [neighbour] }, 400)).toHaveBeenCalledWith(
      expect.objectContaining({ x: 504, y: 100 }),
    );
  });

  it("stays where it was dropped when alignment is off", () => {
    expect(drag({ snapEnabled: false, snapBoxes: [neighbour] }, 400)).toHaveBeenCalledWith(
      expect.objectContaining({ x: 500, y: 100 }),
    );
  });

  it("stays where it was dropped while Shift is held", () => {
    expect(
      drag({ snapEnabled: true, snapBoxes: [neighbour] }, 400, { shiftKey: true }),
    ).toHaveBeenCalledWith(expect.objectContaining({ x: 500, y: 100 }));
  });
});
