import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { FolderBrowser, type FolderListing } from "./FolderBrowser";

const listing: FolderListing = {
  path: "C:\\Users\\me\\.local\\bin",
  parent: "C:\\Users\\me\\.local",
  dirs: [{ name: "old", path: "C:\\Users\\me\\.local\\bin\\old" }],
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("FolderBrowser", () => {
  it("lists the files it is given as rows to pick, and marks the one picked", () => {
    const onPickFile = vi.fn();
    render(
      <FolderBrowser
        listing={listing}
        selected={false}
        onBrowse={vi.fn()}
        onError={vi.fn()}
        files={[
          { name: "claude.exe", path: "C:\\Users\\me\\.local\\bin\\claude.exe" },
          { name: "claude.cmd", path: "C:\\Users\\me\\.local\\bin\\claude.cmd" },
        ]}
        pickedFile={"C:\\Users\\me\\.local\\bin\\claude.cmd"}
        onPickFile={onPickFile}
        emptyFilesText="No file named claude in this folder."
      />,
    );
    const exe = screen.getByRole("button", { name: /claude\.exe/ });
    expect(exe).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByRole("button", { name: /claude\.cmd/ })).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(exe);
    expect(onPickFile).toHaveBeenCalledWith("C:\\Users\\me\\.local\\bin\\claude.exe");
    expect(screen.queryByText("No file named claude in this folder.")).not.toBeInTheDocument();
  });

  it("says so when the folder holds no matching file", () => {
    render(
      <FolderBrowser
        listing={listing}
        selected={false}
        onBrowse={vi.fn()}
        onError={vi.fn()}
        files={[]}
        onPickFile={vi.fn()}
        emptyFilesText="No file named claude in this folder."
      />,
    );
    expect(screen.getByText("No file named claude in this folder.")).toBeInTheDocument();
  });

  it("goes to the home folder in one click when it knows it", () => {
    const onBrowse = vi.fn();
    render(<FolderBrowser listing={listing} selected={false} onBrowse={onBrowse} onError={vi.fn()} home={"C:\\Users\\me"} />);
    fireEvent.click(screen.getByRole("button", { name: "My folder" }));
    expect(onBrowse).toHaveBeenCalledWith("C:\\Users\\me");
  });

  it("renders exactly as before without the file props", () => {
    const plain = render(<FolderBrowser listing={listing} selected onBrowse={vi.fn()} onError={vi.fn()} />);
    const before = plain.container.innerHTML;
    plain.unmount();
    const again = render(
      <FolderBrowser listing={listing} selected onBrowse={vi.fn()} onError={vi.fn()} home="" files={undefined} />,
    );
    expect(again.container.innerHTML).toBe(before);
    expect(again.container.querySelector(".fs-browser-files")).toBeNull();
  });
});
