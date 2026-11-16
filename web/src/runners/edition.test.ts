import { describe, expect, it } from "vitest";

import type { Runner } from "../api";
import { localRunner, restartedRunner, startingRunner } from "../local/localFixtures";
import { isHubRunner, localState } from "./edition";

const classic = (over: Partial<Runner> = {}): Runner => ({
  id: "r9",
  name: "laptop",
  ownerId: "local",
  ownerName: "",
  createdAt: "2026-11-02T00:00:00Z",
  lastSeenAt: "2026-11-14T08:00:00Z",
  online: true,
  ...over,
});

describe("isHubRunner", () => {
  it("is the hub's word, from the list or from a welcome", () => {
    expect(isHubRunner(startingRunner())).toBe(true);
    // What the settings and add-repo routes answer with carries only builtIn.
    expect(isHubRunner({ ...localRunner(), hubLocal: undefined })).toBe(true);
    // Not the name: anyone may call a runner "this machine".
    expect(isHubRunner(classic({ name: "this machine" }))).toBe(false);
  });
});

describe("localState", () => {
  it("is ready once the hub's own runner is connected and welcomed", () => {
    expect(localState([localRunner()])).toBe("ready");
  });

  it("is starting until the hub's own runner has connected since the hub started", () => {
    expect(localState([startingRunner()])).toBe("starting");
    // After a restart the database still knows when it was last seen.
    expect(localState([restartedRunner()])).toBe("starting");
    expect(localState([classic(), startingRunner()])).toBe("starting");
  });

  it("is stopped when it was up and dropped, connected some other way, or is not there", () => {
    expect(localState([{ ...localRunner(), online: false }])).toBe("stopped");
    expect(localState([{ ...localRunner(), builtIn: false }])).toBe("stopped");
    expect(localState([classic()])).toBe("stopped");
    expect(localState([])).toBe("stopped");
  });
});
