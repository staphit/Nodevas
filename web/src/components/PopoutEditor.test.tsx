import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { PopoutEditor } from "./PopoutEditor";

vi.mock("./Drawer", () => ({ TabBody: () => null }));
vi.mock("../collab/popoutLiveUpdates", () => ({ usePopoutLiveUpdates: () => {} }));

// A subpage popped out into its own window showed its internal id
// ("p-3f9c…") under the node's title, where the drawer shows the page's name.
describe("PopoutEditor header", () => {
  beforeEach(() => {
    useApp.setState({
      preferences: { ...readPreferences(), language: "en" },
      graph: null,
      loadAll: vi.fn(async () => {}),
      refreshProjects: vi.fn(async () => {}),
      refreshTrash: vi.fn(async () => {}),
      openTab: vi.fn(async () => {}),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    window.history.replaceState(null, "", "/");
  });

  it("names a popped-out subpage by its title, not its id", async () => {
    window.history.replaceState(null, "", "/popout?node=node-0001&page=p-3f9c2a");
    vi.spyOn(api, "listNodePages").mockResolvedValue({
      pages: [{ id: "p-3f9c2a", title: "Meeting notes" }],
    });
    render(<PopoutEditor />);
    expect(await screen.findByText("Meeting notes")).toBeTruthy();
    expect(screen.queryByText("p-3f9c2a")).toBeNull();
  });

  it("says Main page for the main document without asking for the pages", () => {
    window.history.replaceState(null, "", "/popout?node=node-0001&page=main");
    const list = vi.spyOn(api, "listNodePages");
    render(<PopoutEditor />);
    expect(screen.getByText("Main page")).toBeTruthy();
    expect(list).not.toHaveBeenCalled();
  });
});
