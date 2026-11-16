import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor } from "../api";
import { translate } from "../i18n";
import { readPreferences, type Language } from "../preferences";
import { useApp } from "../store";
import { CommandPalette, CONCEPTS_DOCS_URL, GETTING_STARTED_DOCS_URL } from "./CommandPalette";
import { OPEN_START_SCREEN_EVENT } from "./StartScreen";
import { openShortcutsHelp } from "./topbar/actions";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("./SignIn", async (original) => ({
  ...(await original<typeof import("./SignIn")>()),
  useViewer: () => viewer.current,
}));

const heard: string[] = [];
const listen = (event: Event) => void heard.push(event.type);
const EVENTS = [OPEN_START_SCREEN_EVENT, "nodevas-tour-start"];

beforeEach(() => {
  viewer.current = null;
  heard.length = 0;
  useApp.setState({
    preferences: { ...readPreferences(), language: "en", projectMode: "info" },
    activeProject: "beta",
    projects: [{ name: "beta", label: "Beta", depth: 0, path: "beta", nodes: 0 }],
    workspaces: [],
  });
  for (const name of EVENTS) window.addEventListener(name, listen);
});

afterEach(() => {
  for (const name of EVENTS) window.removeEventListener(name, listen);
});

/** Settings › General › "Help and shortcuts". */
function openHelp(language: Language = "en") {
  useApp.setState({ preferences: { ...useApp.getState().preferences, language } });
  render(<CommandPalette />);
  act(() => openShortcutsHelp());
  return screen.getByRole("dialog");
}

/** The row of a shortcut table whose text is `key`'s: its keys and its line. */
function helpRow(dialog: HTMLElement, key: string, language: Language) {
  const line = within(dialog).getByText(translate(key, language));
  return line.closest("div") as HTMLElement;
}

describe("CommandPalette help: what Nodevas is", () => {
  it("opens with two plain sentences before any shortcut", () => {
    const dialog = openHelp();
    const lead = within(dialog).getByText(/^Nodevas breaks work into steps called nodes/);
    expect(lead.textContent).toMatch(/wait for other nodes.*graph.*timeline\.$/);
    const firstHeading = within(dialog).getAllByRole("heading", { level: 3 })[0];
    expect(lead.compareDocumentPosition(firstHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("says the same in Traditional Chinese", () => {
    const dialog = openHelp("zh-TW");
    expect(within(dialog).getByText(translate("palette.help.about", "zh-TW"))).toBeInTheDocument();
    expect(translate("palette.help.about", "zh-TW")).toMatch(/\p{Script=Han}/u);
  });
});

describe("CommandPalette help: the first things done on the board", () => {
  // The row names the toolbar button by the label it carries, in the
  // reader's language, so nobody looks for a button that reads otherwise.
  it.each(["en", "zh-TW"] as const)("names the New node button as it reads (%s)", (language) => {
    const dialog = openHelp(language);
    const button = translate("board.newNode", language);
    const row = helpRow(dialog, "palette.help.createNode", language);
    expect(within(row).getByText(button, { selector: "kbd" })).toBeInTheDocument();
    expect(translate("palette.help.createNode", language)).toContain(button);
  });

  it("says how to connect two nodes: the dot on the card's edge, or Alt and drag", () => {
    const dialog = openHelp();
    const row = helpRow(dialog, "palette.help.connectNodes", "en");
    expect(row).toHaveTextContent(/round dot on a card's right edge onto another card/);
    expect(within(row).getByText("Alt drag", { selector: "kbd" })).toBeInTheDocument();
    expect(within(row).getByText("Drag the dot", { selector: "kbd" })).toBeInTheDocument();
  });

  it("says a change to the graph can be undone, with its key", () => {
    const dialog = openHelp();
    const row = helpRow(dialog, "palette.help.undoGraph", "en");
    expect(within(row).getByText(/^(⌘|Ctrl) Z$/, { selector: "kbd" })).toBeInTheDocument();
  });

  it("has every new row in both languages", () => {
    for (const key of [
      "palette.help.createNode",
      "palette.help.connectNodes",
      "palette.help.undoGraph",
      "palette.key.rightClick",
      "palette.key.dragDot",
      "palette.help.docsStart",
      "palette.help.docsConcepts",
    ]) {
      expect(translate(key, "en"), key).not.toBe(key);
      expect(translate(key, "zh-TW"), key).toMatch(/\p{Script=Han}/u);
    }
  });
});

describe("CommandPalette help: where to go from here", () => {
  it("links to the guides in a new tab, telling the other site nothing", () => {
    const dialog = openHelp();
    const guides: Array<[string, string]> = [
      ["Getting started guide", GETTING_STARTED_DOCS_URL],
      ["Concepts guide", CONCEPTS_DOCS_URL],
    ];
    for (const [name, href] of guides) {
      const link = within(dialog).getByRole("link", { name });
      expect(link).toHaveAttribute("href", href);
      expect(link).toHaveAttribute("target", "_blank");
      expect(link.getAttribute("rel")?.split(" ").sort()).toEqual(["noopener", "noreferrer"]);
    }
    expect(GETTING_STARTED_DOCS_URL).toMatch(/\/docs\/getting-started\.md#first-steps-in-the-app$/);
    expect(CONCEPTS_DOCS_URL).toMatch(/\/docs\/concepts\.md$/);
  });

  it("opens the start screen's examples and closes itself", async () => {
    const user = userEvent.setup();
    const dialog = openHelp();
    await user.click(within(dialog).getByRole("button", { name: "Open an example project" }));
    expect(heard).toEqual([OPEN_START_SCREEN_EVENT]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  // The tour's welcome dialog must not open under the palette.
  it("replays the tour on the board and closes itself", async () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, projectMode: "settings" } });
    const user = userEvent.setup();
    const dialog = openHelp();
    await user.click(within(dialog).getByRole("button", { name: "Replay tour" }));
    expect(heard).toEqual(["nodevas-tour-start"]);
    expect(useApp.getState().preferences.projectMode).toBe("info");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  // An example becomes a new project, which is an administrator's call on a
  // team server: offered here exactly when the command list offers it.
  it("offers no example to someone who may not create projects", () => {
    viewer.current = { id: "u2", name: "bo", role: "member" };
    const dialog = openHelp();
    expect(within(dialog).queryByRole("button", { name: "Open an example project" })).toBeNull();
    expect(within(dialog).getByRole("button", { name: "Replay tour" })).toBeInTheDocument();
  });

  it("offers it to an administrator", () => {
    viewer.current = { id: "u1", name: "ann", role: "admin" };
    const dialog = openHelp();
    expect(within(dialog).getByRole("button", { name: "Open an example project" })).toBeInTheDocument();
  });
});
