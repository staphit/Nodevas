import { describe, expect, it } from "vitest";
import {
  formatNodeLink,
  parseNodeLinks,
  splitLinkTarget,
} from "./nodeLink";

describe("node links", () => {
  it("reads a bare, a cross-project and a labelled link", () => {
    const links = parseNodeLinks(
      "見 [[node-0006]]、[[Story/node-0012]] 與 [[Game mechanic/systems/node-1|規則]]",
    );
    expect(links.map((link) => [link.project, link.nodeId, link.label])).toEqual([
      ["", "node-0006", "node-0006"],
      ["Story", "node-0012", "node-0012"],
      ["Game mechanic/systems", "node-1", "規則"],
    ]);
  });

  it("splits the target at the last slash, so nested projects survive", () => {
    expect(splitLinkTarget("a/b/c")).toEqual({ project: "a/b", nodeId: "c" });
    expect(splitLinkTarget("node-1")).toEqual({ project: "", nodeId: "node-1" });
  });

  it("ignores an empty target and a link broken across lines", () => {
    expect(parseNodeLinks("[[]] and [[Story/\nnode-1]]")).toEqual([]);
  });

  it("leaves the project out when the target is in the current one", () => {
    expect(
      formatNodeLink({
        project: "Story",
        nodeId: "node-1",
        label: "序章",
        currentProject: "Story",
      }),
    ).toBe("[[node-1|序章]]");
    expect(
      formatNodeLink({
        project: "Story",
        nodeId: "node-1",
        label: "node-1",
        currentProject: "Other",
      }),
    ).toBe("[[Story/node-1]]");
  });
});
