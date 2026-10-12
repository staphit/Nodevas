import { afterEach, expect, it, vi } from "vitest";
import { setProjectOverride } from "./http";
import { workspaceApi } from "./workspace";

afterEach(() => setProjectOverride(""));

it.each(["", "project B"])("exports using the selected project %j", async (project) => {
  const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue({
    ok: true, status: 200, headers: new Headers(),
    blob: async () => new Blob(["exported document"]),
  } as Response);
  setProjectOverride(project);
  await workspaceApi.exportDocument({ format: "md", scope: "project" });
  const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
  expect(headers.get("X-Nodevas-Project")).toBe(project || null);
});
