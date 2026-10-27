import { describe, expect, it } from "vitest";
import { joinSessions, nameHasRunTag, runNameTag, runSessionName, sessionIsRuns, slug } from "./joins";
import { makeRun, makeSession, NOW } from "./testFixtures";

const RUN_ID = "a1b2c3d4e5f60718293a4b5c";

describe("slug", () => {
  it("matches the runner's slug", () => {
    expect(slug("Visual Editor")).toBe("visual-editor");
    expect(slug("--claude-opus-4.1--")).toBe("claude-opus-4-1");
    expect(slug("模型 x")).toBe("x");
    expect(slug("")).toBe("");
  });
});

describe("run session names", () => {
  it("pins the name a runner gives a run's agent (internal/runner/jobs.go mcpArgs)", () => {
    // repoName-agent-slug(model)-slug(runID)[:6]
    const run = makeRun({ id: RUN_ID, repoName: "Visual Editor", agent: "claude", model: "claude-opus-4.1" });
    expect(runSessionName(run)).toBe("visual-editor-claude-claude-opus-4-1-a1b2c3");
    expect(runSessionName({ ...run, model: "", repoName: "" })).toBe("repo-claude-default-a1b2c3");
    expect(runNameTag(RUN_ID)).toBe("a1b2c3");
  });

  it("finds the tag at the end, or before the hub's uniqueness suffix", () => {
    expect(nameHasRunTag("visual-editor-claude-opus-a1b2c3", RUN_ID)).toBe(true);
    expect(nameHasRunTag("visual-editor-claude-opus-a1b2c3-2", RUN_ID)).toBe(true);
    expect(nameHasRunTag("Visual-Editor-Claude-Opus-A1B2C3", RUN_ID)).toBe(true);
  });

  it("does not match a tag elsewhere in the name, another run's tag, or a short id", () => {
    expect(nameHasRunTag("a1b2c3-claude-opus-ffffff", RUN_ID)).toBe(false);
    expect(nameHasRunTag("visual-editor-claude-opus-a1b2c4", RUN_ID)).toBe(false);
    expect(nameHasRunTag("visual-editor-claude-opus-a1b2c3x", RUN_ID)).toBe(false);
    expect(nameHasRunTag("x-abc", "abc")).toBe(false);
  });

  it("decides by runId alone when the session has one", () => {
    const run = makeRun({ id: RUN_ID });
    expect(sessionIsRuns(makeSession({ runId: RUN_ID, name: "anything" }), run)).toBe(true);
    // The name would match, the runId says otherwise.
    expect(
      sessionIsRuns(makeSession({ runId: "ffffffffffffffffffffffff", name: "repo-claude-opus-a1b2c3" }), run),
    ).toBe(false);
    expect(sessionIsRuns(makeSession({ run: false, name: "repo-claude-opus-a1b2c3" }), run)).toBe(false);
  });
});

describe("joinSessions", () => {
  it("pairs by runId first, then by name, and leaves the rest unjoined", () => {
    const exact = makeRun({ id: RUN_ID });
    const named = makeRun({ id: "0badc0de0000000000000000" });
    const sessions = [
      makeSession({ id: "s-exact", runId: RUN_ID, name: "whatever-1" }),
      makeSession({ id: "s-named", name: "visual-editor-claude-opus-0badc0", run: true }),
      makeSession({ id: "s-person", name: "laptop-claude" }),
    ];
    const join = joinSessions([exact, named], sessions);
    expect(join.byRun.get(RUN_ID)?.id).toBe("s-exact");
    expect(join.byRun.get(named.id)?.id).toBe("s-named");
    expect(join.runOf.get("s-named")).toBe(named.id);
    expect(join.unjoined.map((s) => s.id)).toEqual(["s-person"]);
  });

  it("lets an exact runId beat a name match for the same run", () => {
    const run = makeRun({ id: RUN_ID });
    const join = joinSessions(
      [run],
      [
        makeSession({ id: "s-name", name: "repo-claude-opus-a1b2c3", lastSeen: NOW }),
        makeSession({ id: "s-id", runId: RUN_ID, name: "other", lastSeen: NOW - 50_000 }),
      ],
    );
    expect(join.byRun.get(RUN_ID)?.id).toBe("s-id");
    expect(join.unjoined.map((s) => s.id)).toEqual(["s-name"]);
  });

  it("takes the most recently seen of two name matches (a resumed conversation)", () => {
    const run = makeRun({ id: RUN_ID });
    const join = joinSessions(
      [run],
      [
        makeSession({ id: "old", name: "repo-claude-opus-a1b2c3", lastSeen: NOW - 60_000 }),
        makeSession({ id: "new", name: "repo-claude-opus-a1b2c3-2", lastSeen: NOW - 1_000 }),
      ],
    );
    expect(join.byRun.get(RUN_ID)?.id).toBe("new");
    expect(join.unjoined.map((s) => s.id)).toEqual(["old"]);
  });

  it("does not name-join a session that carries another run's id", () => {
    const run = makeRun({ id: RUN_ID });
    const join = joinSessions(
      [run],
      [makeSession({ id: "s", runId: "ffffffffffffffffffffffff", name: "repo-claude-opus-a1b2c3" })],
    );
    expect(join.byRun.size).toBe(0);
    expect(join.unjoined).toHaveLength(1);
  });

  it("gives each session to one run at most", () => {
    // Two runs whose ids share the first six characters: a session cannot
    // sit at both desks.
    const a = makeRun({ id: "a1b2c3000000000000000000" });
    const b = makeRun({ id: "a1b2c3ffffffffffffffffff" });
    const join = joinSessions([a, b], [makeSession({ id: "s", name: "repo-claude-opus-a1b2c3" })]);
    expect(join.byRun.size).toBe(1);
    expect(join.runOf.size).toBe(1);
  });
});
