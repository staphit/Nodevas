import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ExplorerError, explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import FileView from "./FileView";

const openNodeDetail = vi.hoisted(() => vi.fn());
vi.mock("../components/drawer/openNodeDetail", async (original) => ({
  ...(await original<typeof import("../components/drawer/openNodeDetail")>()),
  openNodeDetail,
}));

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  openNodeDetail.mockReset();
});
afterEach(() => vi.restoreAllMocks());

// W-11: one file, read-only.
describe("FileView", () => {
  it("renders a node's markdown and opens it in the node panel", async () => {
    vi.spyOn(explorerApi, "workspaceFile").mockResolvedValue({
      path: "nodes/login.md",
      size: 40,
      kind: "text",
      content: "# Login form\n\nThe button does **nothing**.",
      truncated: false,
      nodeId: "login",
    });
    render(<FileView view={{ kind: "file", source: "workspace", path: "nodes/login.md" }} />);
    expect(await screen.findByRole("heading", { name: "Login form" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Open in node panel" }));
    expect(openNodeDetail).toHaveBeenCalledWith("login");
  });

  it("shows a repo file as code with the read-only chip", async () => {
    vi.spyOn(explorerApi, "repoFile").mockResolvedValue({
      path: "src/math.js",
      size: 38,
      binary: false,
      truncated: false,
      content: "export const add = (a, b) => a + b;\n",
    });
    render(
      <FileView view={{ kind: "file", source: "repo", runnerId: "rn1", runnerName: "mac-mini", repoId: "r1", path: "src/math.js" }} />,
    );
    expect(await screen.findByText("Read-only · from runner “mac-mini”")).toBeInTheDocument();
    expect(screen.getByText(/export const add/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Open in node panel" })).not.toBeInTheDocument();
  });

  it("says when a file is binary, cut short or hidden", async () => {
    const read = vi.spyOn(explorerApi, "repoFile");
    read.mockResolvedValueOnce({ path: "logo.bin", size: 10, binary: true, truncated: false, content: "" });
    const view = { kind: "file" as const, source: "repo" as const, runnerId: "rn1", runnerName: "mac-mini", repoId: "r1", path: "logo.bin" };
    const { unmount } = render(<FileView view={view} />);
    expect(await screen.findByText("Binary file: no preview.")).toBeInTheDocument();
    unmount();

    read.mockResolvedValueOnce({ path: "big.txt", size: 600 << 10, binary: false, truncated: true, content: "x" });
    const second = render(<FileView view={{ ...view, path: "big.txt" }} />);
    expect(await screen.findByText("Showing the first 512 KB.")).toBeInTheDocument();
    second.unmount();

    read.mockRejectedValueOnce(new ExplorerError(403, "HIDDEN_FILE", "hidden"));
    render(<FileView view={{ ...view, path: ".env" }} />);
    expect(await screen.findByText("This file is hidden because it may hold secrets.")).toBeInTheDocument();
  });

  it("previews a workspace image", async () => {
    vi.spyOn(explorerApi, "workspaceFile").mockResolvedValue({
      path: "nodes/login/files/shot.png",
      size: 2048,
      kind: "image",
      truncated: false,
      url: "/api/nodes/login/files/shot.png",
    });
    render(<FileView view={{ kind: "file", source: "workspace", path: "nodes/login/files/shot.png" }} />);
    expect(await screen.findByRole("img", { name: "nodes/login/files/shot.png" })).toHaveAttribute(
      "src",
      "/api/nodes/login/files/shot.png",
    );
  });
});
