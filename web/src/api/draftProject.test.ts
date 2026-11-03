import { afterEach, expect, it, vi } from "vitest";
import { setProjectOverride } from "./http";
import { nodesApi } from "./nodes";

// A draft parked for a project the hub has already moved away from (another
// tab switched it) names that project, or it would land in the new one.

afterEach(() => {
  setProjectOverride("");
  vi.restoreAllMocks();
});

function fetchOk() {
  return vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

it("writes a draft into the project it names", async () => {
  const fetchMock = fetchOk();
  await nodesApi.putDraft("node-0003", "text", "daily-work-schedule");
  const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
  expect(headers.get("X-Nodevas-Project")).toBe("daily-work-schedule");
});

it("writes into the selected project when none is named", async () => {
  const fetchMock = fetchOk();
  await nodesApi.putDraft("node-0003", "text");
  const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
  expect(headers.get("X-Nodevas-Project")).toBeNull();
});
