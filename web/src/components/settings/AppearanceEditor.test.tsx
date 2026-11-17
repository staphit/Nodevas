import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { OPEN_FIRST_RUN_EVENT } from "../../firstRun/gate";
import { readPreferences } from "../../preferences";
import { EditionProvider } from "../../runners/edition";
import { useApp } from "../../store";
import { AppearanceEditor, LayoutEditor } from "./AppearanceEditor";

vi.mock("../SignIn", () => ({ useViewer: () => null }));

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", projectMode: "settings" } });
});

const notify = () => ({ onError: vi.fn(), onNotice: vi.fn() });

describe("AppearanceEditor", () => {
  it("replays the tour from Info, where its targets are", () => {
    const started = vi.fn(() => expect(useApp.getState().preferences.projectMode).toBe("info"));
    window.addEventListener("nodevas-tour-start", started);
    render(<AppearanceEditor />);

    fireEvent.click(screen.getByRole("button", { name: "Replay the tour" }));
    expect(started).toHaveBeenCalledTimes(1);
    window.removeEventListener("nodevas-tour-start", started);
  });

  // The bar has no help icon any more: General holds it, beside the tour.
  it("opens the palette on its help view", () => {
    const opened = vi.fn();
    const listener = (event: Event) => opened((event as CustomEvent).detail);
    window.addEventListener("nodevas-command-palette", listener);
    render(<AppearanceEditor />);

    fireEvent.click(screen.getByRole("button", { name: "Help and shortcuts" }));
    expect(opened).toHaveBeenCalledWith({ view: "help" });
    window.removeEventListener("nodevas-command-palette", listener);
  });

  it("asks the starting questions again in the single-user edition", () => {
    const opened = vi.fn();
    window.addEventListener(OPEN_FIRST_RUN_EVENT, opened);
    render(
      <EditionProvider edition="single">
        <AppearanceEditor />
      </EditionProvider>,
    );

    expect(screen.getByText("Starting questions")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Answer them again" }));
    expect(opened).toHaveBeenCalledTimes(1);
    window.removeEventListener(OPEN_FIRST_RUN_EVENT, opened);
  });

  it("has no starting questions on a team hub", () => {
    render(<AppearanceEditor />);
    expect(screen.queryByText("Starting questions")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Answer them again" })).not.toBeInTheDocument();
  });

  it("edits the same editor preferences the drawer uses", () => {
    render(<LayoutEditor notify={notify()} />);

    fireEvent.change(screen.getByRole("combobox", { name: "Editing mode" }), { target: { value: "source" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Text size" }), { target: { value: "18" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "Show the document outline" }));

    const prefs = useApp.getState().preferences;
    expect(prefs.editorMode).toBe("source");
    expect(prefs.editorFontSize).toBe(18);
    expect(prefs.editorOutline).toBe(true);
  });
});
