import { describe, expect, it } from "vitest";
import { splitUnreadableGraph } from "./UnreadableGraphState";

// GET /api/graph's refusal (store/graph_unreadable.go). The zh-TW hub leads
// with "graph.yaml：" (full-width colon) where the English one has ": ".
describe("splitting the hub's graph.yaml refusal", () => {
  it("takes the English sentence and the parser's words apart", () => {
    expect(
      splitUnreadableGraph(
        "graph.yaml: Line 3 has an error. Fix the file, then reload. (yaml: line 3: did not find expected node content)",
      ),
    ).toEqual({
      sentence: "Line 3 has an error. Fix the file, then reload.",
      detail: "yaml: line 3: did not find expected node content",
    });
  });

  it("drops the file name the zh-TW sentence leads with, after its full-width colon", () => {
    expect(
      splitUnreadableGraph(
        "graph.yaml：第 3 行有錯誤。修正檔案後重新載入。（yaml: line 3: did not find expected '-' indicator）",
      ),
    ).toEqual({
      sentence: "第 3 行有錯誤。修正檔案後重新載入。",
      detail: "yaml: line 3: did not find expected '-' indicator",
    });
  });

  it("does the same behind the zh-TW project frame", () => {
    expect(
      splitUnreadableGraph(
        "載入專案「m5」：graph.yaml：檔案有錯誤。修正檔案後重新載入。（node \"c\" has invalid priority \"zzz\"）",
      ),
    ).toEqual({
      sentence: "檔案有錯誤。修正檔案後重新載入。",
      detail: 'node "c" has invalid priority "zzz"',
    });
  });
});
