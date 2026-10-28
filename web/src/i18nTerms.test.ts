import { describe, expect, it } from "vitest";
import { translate } from "./i18n";

// Words a Traditional Chinese reader should not meet in English. Product
// and protocol names (Runner, Token, Git, Copilot, PDF…) stay as they are.
const KEYS = [
  "settingsPage.page.agents",
  "station.roster.title",
  "editor.markdown.task",
  "node.writeAccess.worker",
  "node.writeAccess.orchestrator",
  "auth.email",
  "merge.pullRequest",
  "merge.head",
  "routing.fallbackN",
  "autopilot.stage.commit",
  "autopilot.stageShort.commit",
  "autopilot.stage.push",
  "autopilot.stageShort.push",
  "autopilot.stage.pr",
  "evidence.head",
  "runners.link.accessClientSecret",
  "usage.tab.sessions",
  "usage.col.session",
  "usage.source.session",
  "usage.source.run",
  "explorer.frontMatter.assignee",
  "explorer.frontMatter.priority",
  "explorer.frontMatter.requires",
  "explorer.frontMatter.tags",
  "explorer.frontMatter.title",
  "explorer.frontMatter.kind",
];

describe("Traditional Chinese terms", () => {
  it.each(KEYS)("%s reads in Chinese", (key) => {
    expect(translate(key, "zh-TW")).toMatch(/\p{Script=Han}/u);
  });
});
