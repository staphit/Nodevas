import { describe, expectTypeOf, it } from "vitest";
import type { BuildingAsk } from "./station";

// The web types mirror internal/httpapi/station/types.go. Checked by tsc -b,
// which compiles the tests; at run time these assertions do nothing.
describe("BuildingAsk", () => {
  it("declares only what the hub sends: no toolInput", () => {
    expectTypeOf<BuildingAsk>().not.toHaveProperty("toolInput");
  });
});
