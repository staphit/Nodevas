import { afterEach, describe, expect, it, vi } from "vitest";
import { writePreference } from "../preferences";
import { workspaceApi } from "./workspace";

// An exported document carries words the hub writes itself (headings, labels,
// the file name of an untitled page), and the reader of the file is whoever
// set the app's language. The archive download is a plain link, so it cannot
// carry an Accept-Language header; every export names the language in its
// query instead. (The JSON body is not used: the hub refuses unknown fields.)

afterEach(() => {
  vi.restoreAllMocks();
  writePreference("language", "en");
});

function stubFetch() {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async () => new Response("x", { status: 200 }));
}

function queryLanguage(url: string): string | null {
  return new URL(url, "http://hub.test").searchParams.get("language");
}

describe("an export names the app's language", () => {
  it("on a document export (editor Export menu, explorer document export)", async () => {
    const fetchMock = stubFetch();
    writePreference("language", "zh-TW");
    await workspaceApi.exportDocument({ format: "md", scope: "project" });
    expect(queryLanguage(String(fetchMock.mock.calls[0][0]))).toBe("zh-TW");

    fetchMock.mockClear();
    writePreference("language", "en");
    await workspaceApi.exportDocument({ format: "html", scope: "node", nodeId: "a" });
    expect(queryLanguage(String(fetchMock.mock.calls[0][0]))).toBe("en");
  });

  it("keeps the request body to the fields the hub accepts", async () => {
    const fetchMock = stubFetch();
    writePreference("language", "zh-TW");
    await workspaceApi.exportDocument({ format: "md", scope: "page", nodeId: "a", pageId: "main" });
    const body = JSON.parse(String((fetchMock.mock.calls[0][1] as RequestInit).body));
    expect(Object.keys(body).sort()).toEqual(["format", "nodeId", "pageId", "scope"]);
  });

  it("on a project or workspace archive download", () => {
    writePreference("language", "zh-TW");
    const url = workspaceApi.projectExportURL("書/第一部");
    expect(queryLanguage(url)).toBe("zh-TW");
    expect(new URL(url, "http://hub.test").searchParams.get("project")).toBe("書/第一部");

    writePreference("language", "en");
    expect(queryLanguage(workspaceApi.projectExportURL())).toBe("en");
    expect(new URL(workspaceApi.projectExportURL(), "http://hub.test").searchParams.has("project")).toBe(false);
  });
});
