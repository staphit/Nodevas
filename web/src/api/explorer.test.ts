import { afterEach, describe, expect, it, vi } from "vitest";
import { ExplorerError, explorerApi, repoUrl } from "./explorer";

afterEach(() => vi.restoreAllMocks());

function mockFetch(body: unknown, status = 200) {
  return vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(
      async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
    );
}

const urls = (fetch: ReturnType<typeof mockFetch>) => fetch.mock.calls.map((c) => String(c[0]));

// W-02: URLs, escaping, and the codes the Explorer reacts to.
describe("explorer API", () => {
  it("escapes the runner, the repo and the path", () => {
    expect(repoUrl("rn 1", "repo/x", "git/status")).toBe("/api/explorer/repos/rn%201/repo%2Fx/git/status");
  });

  it("asks for workspace files, a file, its git and a commit", async () => {
    const fetch = mockFetch({ entries: [], hidden: 0, truncated: false });
    await explorerApi.workspaceFiles();
    await explorerApi.workspaceFile("nodes/login.md");
    await explorerApi.workspaceFile("docs/a b#c.md");
    await explorerApi.workspaceGit();
    await explorerApi.workspaceCommit("a".repeat(40));
    expect(urls(fetch)).toEqual([
      "/api/explorer/workspace/files",
      "/api/explorer/workspace/file?path=nodes%2Flogin.md",
      "/api/explorer/workspace/file?path=docs%2Fa%20b%23c.md",
      "/api/explorer/workspace/git",
      `/api/explorer/workspace/git/commit?sha=${"a".repeat(40)}`,
    ]);
  });

  it("asks a runner's repo for a tree, a file, its status, log and diff", async () => {
    const fetch = mockFetch({ path: "", entries: [], hidden: 0, truncated: false });
    await explorerApi.repoTree("rn1", "r1", "src/lib");
    await explorerApi.repoFile("rn1", "r1", "src/math.js");
    await explorerApi.repoStatus("rn1", "r1");
    await explorerApi.repoLog("rn1", "r1", 30);
    await explorerApi.repoDiff("rn1", "r1", "");
    await explorerApi.repoDiff("rn1", "r1", "b".repeat(40));
    expect(urls(fetch)).toEqual([
      "/api/explorer/repos/rn1/r1/tree?path=src%2Flib",
      "/api/explorer/repos/rn1/r1/file?path=src%2Fmath.js",
      "/api/explorer/repos/rn1/r1/git/status",
      "/api/explorer/repos/rn1/r1/git/log?limit=30",
      "/api/explorer/repos/rn1/r1/git/diff?ref=",
      `/api/explorer/repos/rn1/r1/git/diff?ref=${"b".repeat(40)}`,
    ]);
  });

  it("reads the agents and a session's actions", async () => {
    const fetch = mockFetch({ live: [], external: [], recent: [], actions: [] });
    expect(await explorerApi.agents()).toEqual({ live: [], external: [], recent: [] });
    await explorerApi.sessionActions("claude:s1", 200);
    expect(urls(fetch)).toEqual(["/api/explorer/agents", "/api/explorer/sessions/claude%3As1/actions?limit=200"]);
  });

  it.each([
    [503, "RUNNER_OFFLINE"],
    [412, "REPO_BROWSE_OFF"],
    [403, "HIDDEN_FILE"],
    [400, "BAD_PATH"],
    [504, "TIMEOUT"],
  ] as const)("maps %i %s to an ExplorerError with its code", async (status, code) => {
    mockFetch({ code, error: "no" }, status);
    const err = await explorerApi.repoFile("rn1", "r1", ".env").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ExplorerError);
    expect((err as ExplorerError).code).toBe(code);
    expect((err as ExplorerError).status).toBe(status);
  });
});
