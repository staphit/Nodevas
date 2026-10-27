import { describe, expect, it } from "vitest";
import { finishedRunStaysInRoom, runPose, sessionPose } from "./pose";
import { makeRun, makeSession, NOW } from "./testFixtures";
import { POSES } from "./world";

const here = { runnerOnline: true, asking: false };

describe("runPose", () => {
  it("lists the poses most urgent first", () => {
    expect(POSES).toEqual([
      "offline",
      "asking-user",
      "failed",
      "awaiting-accept",
      "waiting-ci",
      "reviewing",
      "working",
      "idle",
    ]);
  });

  it("works while running, and reviews when the run is a review", () => {
    expect(runPose(makeRun({ status: "running" }), here)).toBe("working");
    expect(runPose(makeRun({ status: "running", kind: "review" }), here)).toBe("reviewing");
    expect(runPose(makeRun({ status: "running", kind: "revise" }), here)).toBe("working");
  });

  it("waits at the door while queued or offered, or while a merge is in flight", () => {
    expect(runPose(makeRun({ status: "queued" }), here)).toBe("awaiting-accept");
    expect(runPose(makeRun({ status: "offered" }), here)).toBe("awaiting-accept");
    const merging = makeRun({
      status: "succeeded",
      prUrl: "https://github.com/o/r/pull/1",
      prState: "open",
      ciStatus: "success",
      merge: { id: "m", runId: "r", headSha: "h", status: "pending", requestedBy: "bea", createdAt: "" },
    });
    expect(runPose(merging, here)).toBe("awaiting-accept");
  });

  it("waits for CI on an open PR with CI pending", () => {
    const run = makeRun({ status: "succeeded", prUrl: "https://x/pr/1", prState: "open", ciStatus: "pending" });
    expect(runPose(run, here)).toBe("waiting-ci");
    // A merge in flight is the more urgent wait.
    expect(
      runPose({ ...run, merge: { id: "m", runId: "r", headSha: "h", status: "pending", requestedBy: "", createdAt: "" } }, here),
    ).toBe("awaiting-accept");
    // No PR URL, no CI to wait for.
    expect(runPose({ ...run, prUrl: undefined }, here)).toBe("idle");
  });

  it("fails on a failed run or a red CI, before any wait", () => {
    expect(runPose(makeRun({ status: "failed" }), here)).toBe("failed");
    const red = makeRun({ status: "succeeded", prUrl: "https://x/pr/1", prState: "open", ciStatus: "failure" });
    expect(runPose(red, here)).toBe("failed");
    expect(
      runPose({ ...red, merge: { id: "m", runId: "r", headSha: "h", status: "pending", requestedBy: "", createdAt: "" } }, here),
    ).toBe("failed");
    // A merged or closed PR's old CI no longer counts.
    expect(runPose({ ...red, prState: "merged" }, here)).toBe("idle");
  });

  it("raises a hand over everything but being offline", () => {
    const asking = { runnerOnline: true, asking: true };
    expect(runPose(makeRun({ status: "running" }), asking)).toBe("asking-user");
    expect(runPose(makeRun({ status: "failed" }), asking)).toBe("asking-user");
    expect(runPose(makeRun({ status: "running" }), { runnerOnline: false, asking: true })).toBe("offline");
  });

  it("is offline when its runner is, until the run has ended", () => {
    const away = { runnerOnline: false, asking: false };
    expect(runPose(makeRun({ status: "running" }), away)).toBe("offline");
    expect(runPose(makeRun({ status: "queued" }), away)).toBe("offline");
    expect(runPose(makeRun({ status: "failed" }), away)).toBe("failed");
    // An unknown runner never makes a run offline.
    expect(runPose(makeRun({ status: "running" }), { runnerOnline: null, asking: false })).toBe("working");
  });

  it("idles once finished with nothing pending", () => {
    expect(runPose(makeRun({ status: "succeeded" }), here)).toBe("idle");
    expect(runPose(makeRun({ status: "cancelled" }), here)).toBe("idle");
  });
});

describe("sessionPose", () => {
  it("works while busy and idles otherwise", () => {
    expect(sessionPose(makeSession({ state: "busy" }), { now: NOW, asking: false })).toBe("working");
    expect(sessionPose(makeSession({ state: "idle" }), { now: NOW, asking: false })).toBe("idle");
  });

  it("raises a hand while asking, and is offline past the TTL whatever it was doing", () => {
    expect(sessionPose(makeSession({ state: "busy" }), { now: NOW, asking: true })).toBe("asking-user");
    const quiet = makeSession({ state: "busy", lastSeen: NOW - 90_000 });
    expect(sessionPose(quiet, { now: NOW, asking: true })).toBe("offline");
    expect(sessionPose(makeSession({ online: false }), { now: NOW, asking: false })).toBe("offline");
  });
});

describe("finishedRunStaysInRoom", () => {
  it("keeps live runs and finished ones that need someone", () => {
    expect(finishedRunStaysInRoom(makeRun({ status: "running" }))).toBe(true);
    expect(finishedRunStaysInRoom(makeRun({ status: "failed" }))).toBe(true);
    expect(
      finishedRunStaysInRoom(makeRun({ status: "succeeded", prUrl: "https://x", prState: "open", ciStatus: "pending" })),
    ).toBe(true);
    expect(
      finishedRunStaysInRoom(makeRun({ status: "succeeded", prUrl: "https://x", prState: "open", ciStatus: "failure" })),
    ).toBe(true);
  });

  it("lets go of runs that ended cleanly", () => {
    expect(finishedRunStaysInRoom(makeRun({ status: "succeeded" }))).toBe(false);
    expect(finishedRunStaysInRoom(makeRun({ status: "cancelled" }))).toBe(false);
    expect(finishedRunStaysInRoom(makeRun({ status: "declined" }))).toBe(false);
    expect(
      finishedRunStaysInRoom(makeRun({ status: "succeeded", prUrl: "https://x", prState: "merged" })),
    ).toBe(false);
  });
});
