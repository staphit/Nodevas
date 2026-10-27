import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { usePacks } from "../../game/packStore";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { AppearanceEditor, LayoutEditor } from "./AppearanceEditor";

vi.mock("../SignIn", () => ({ useViewer: () => null }));

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", projectMode: "settings" } });
  usePacks.setState({ packs: [], loaded: true, busy: false, load: async () => {} });
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
