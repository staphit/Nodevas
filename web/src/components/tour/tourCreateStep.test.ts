import { expect, it } from "vitest";
import { translate } from "../../i18n";
import { chapterById } from "./chapters";

// The "create a node" step spotlights the toolbar's New node button. In
// English it told people to click "Add node", a label no button carries.
const step = chapterById("graph")!.steps.find((s) => s.id === "graph-create")!;

it("points at the toolbar's New node button", () => {
  expect(step.target).toBe(".board-primary-action");
});

it.each(["en", "zh-TW"] as const)("names that button by the label it carries (%s)", (language) => {
  expect(translate(step.bodyKey, language)).toContain(translate("board.newNode", language));
});

it("does not call it Add node", () => {
  expect(translate(step.bodyKey, "en")).not.toMatch(/add node/i);
});
