import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError, type SessionChange } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { NodeSessions, repoAtBranch } from "./NodeSessions";
import { useSessions } from "./sessionStore";

const patch = [
  "diff --git a/web/src/App.tsx b/web/src/App.tsx",
  "index 1111111..2222222 100644",
  "--- a/web/src/App.tsx",
  "+++ b/web/src/App.tsx",
  "@@ -1,2 +1,2 @@",
  "-const old = 1;",
  "+const fresh = 2;",
  " export {};",
  "",
].join("\n");

function makeChange(overrides: Partial<SessionChange> = {}): SessionChange {
  return {
    id: "sc1",
    project: "p",
    nodeId: "alpha",
    sessionId: "s-1",
    session: "visual-editor-claude",
    agent: "claude",
    actorId: "u1",
    repo: "nodevas",
    branch: "feat/w6",
    files: ["web/src/App.tsx", "web/src/i18n.ts"],
    snapshot: {
      commit: "",
      base: "feat/w6",
      files: [{ path: "web/src/App.tsx", status: "modified", additions: 1, deletions: 1 }],
      additions: 1,
      deletions: 1,
      patch,
    },
    createdAt: "2026-10-24T07:00:00Z",
    updatedAt: "2026-10-24T08:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useSessions.getState().reset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("repoAtBranch", () => {
  it("joins what is known", () => {
    expect(repoAtBranch({ repo: "nodevas", branch: "main" })).toBe("nodevas@main");
    expect(repoAtBranch({ repo: "nodevas" })).toBe("nodevas");
    expect(repoAtBranch({ branch: "main" })).toBe("@main");
    expect(repoAtBranch({})).toBe("");
  });
});

describe("NodeSessions", () => {
  it("lists each session with its agent, repo@branch and touched files", async () => {
    const get = vi.spyOn(api, "getSessionChanges").mockResolvedValue({
      changes: [
        makeChange({ id: "old", session: "older-session", updatedAt: "2026-10-24T06:00:00Z", snapshot: null, files: [] }),
        makeChange(),
      ],
    });

    render(<NodeSessions id="alpha" />);

    const section = await screen.findByRole("region", { name: /Terminal sessions/ });
    expect(get).toHaveBeenCalledWith("alpha");
    const items = within(section).getAllByRole("listitem").filter((item) => item.classList.contains("session-item"));
    // Newest first.
    expect(items.map((item) => item.dataset.testid)).toEqual(["session-sc1", "session-old"]);

    const first = screen.getByTestId("session-sc1");
    expect(within(first).getByText("visual-editor-claude")).toBeInTheDocument();
    expect(within(first).getByText("claude")).toBeInTheDocument();
    expect(within(first).getByText("nodevas@feat/w6")).toBeInTheDocument();
    expect(within(first).getByText("Files touched (2)")).toBeInTheDocument();
    const details = within(first).getByText("Files touched (2)").closest("details")!;
    expect(details.open).toBe(false);
    fireEvent.click(within(first).getByText("Files touched (2)"));
    expect(within(details).getByText("web/src/i18n.ts")).toBeInTheDocument();
    expect(within(first).getByRole("button", { name: "View diff" })).toBeInTheDocument();

    const second = screen.getByTestId("session-old");
    expect(within(second).getByText("No file edits reported.")).toBeInTheDocument();
    expect(within(second).queryByRole("button", { name: "View diff" })).not.toBeInTheDocument();
  });

  it("opens the snapshot in the diff viewer", async () => {
    vi.spyOn(api, "getSessionChanges").mockResolvedValue({ changes: [makeChange()] });
    render(<NodeSessions id="alpha" />);

    fireEvent.click(await screen.findByRole("button", { name: "View diff" }));
    const dialog = await screen.findByRole("dialog", { name: "Session changes" });
    expect(within(dialog).getByText("visual-editor-claude · claude · nodevas@feat/w6")).toBeInTheDocument();
    expect(within(dialog).getByText("snapshot at Stop")).toBeInTheDocument();
    const files = within(dialog).getByRole("navigation", { name: "Changed files" });
    fireEvent.click(within(files).getByText("web/src/App.tsx"));
    expect(await within(dialog).findByText("const fresh = 2;")).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog", { name: "Session changes" })).not.toBeInTheDocument();
  });

  it("refreshes on a session-changes push for a node it shows", async () => {
    const get = vi
      .spyOn(api, "getSessionChanges")
      .mockResolvedValueOnce({ changes: [makeChange()] })
      .mockResolvedValueOnce({
        changes: [makeChange(), makeChange({ id: "sc2", session: "second-session", updatedAt: "2026-10-24T09:00:00Z" })],
      });

    render(<NodeSessions id="alpha" />);
    await screen.findByTestId("session-sc1");

    // A push for a node nothing shows is ignored.
    act(() => useSessions.getState().onChanged("beta"));
    expect(get).toHaveBeenCalledTimes(1);

    act(() => useSessions.getState().onChanged("alpha"));
    expect(await screen.findByTestId("session-sc2")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("shows nothing for a node without sessions, or on a hub without the route", async () => {
    vi.spyOn(api, "getSessionChanges").mockRejectedValue(new RequestError(404, "not found"));
    const { container } = render(<NodeSessions id="alpha" />);
    await waitFor(() => expect(useSessions.getState().byNode.alpha).toEqual([]));
    expect(container).toBeEmptyDOMElement();
  });

  it("shows a failed fetch", async () => {
    vi.spyOn(api, "getSessionChanges").mockRejectedValue(new RequestError(500, "boom"));
    render(<NodeSessions id="alpha" />);
    expect(await screen.findByText("boom")).toBeInTheDocument();
  });
});
