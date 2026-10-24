import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type RemoteConfig } from "../api";
import { readPreferences } from "../preferences/storage";
import { useApp } from "../store";
import { resetCoalescing } from "../state/coalesce";
import { BackupModal } from "./BackupModal";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getRemoteConfig: vi.fn(),
      putRemoteConfig: vi.fn(),
      listRemoteBundles: vi.fn(),
      getRemoteSyncStatus: vi.fn(),
      pushRemote: vi.fn(),
      flushRemoteSync: vi.fn(),
    },
  };
});

const FOLDER_CONFIG: RemoteConfig = {
  kind: "folder",
  folder: "/backups",
  autoBackup: false,
  intervalHours: 24,
  retainBundles: 14,
  lastBackupAt: "0001-01-01T00:00:00Z",
};

const BUNDLE = {
  id: "b1",
  name: "workspace.veproj",
  size: 4096,
  modified: "2026-01-02T03:04:05Z",
};

function open() {
  const onClose = vi.fn();
  render(<BackupModal onClose={onClose} />);
  return { onClose, user: userEvent.setup() };
}

beforeEach(() => {
  resetCoalescing();
  vi.mocked(api.getRemoteConfig).mockResolvedValue(FOLDER_CONFIG);
  vi.mocked(api.putRemoteConfig).mockResolvedValue(FOLDER_CONFIG);
  vi.mocked(api.listRemoteBundles).mockResolvedValue({ backend: "folder", bundles: [BUNDLE] });
  vi.mocked(api.getRemoteSyncStatus).mockResolvedValue({
    state: "synced",
    localHash: "h",
    lastPushedHash: "h",
    lastPushedBundleId: "b1",
    remoteLatest: BUNDLE,
    remoteHash: "h",
    lastBackupAt: FOLDER_CONFIG.lastBackupAt,
  });
  vi.mocked(api.pushRemote).mockResolvedValue({ backend: "folder", bundle: BUNDLE });
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    remoteConfig: null,
    remoteBundles: null,
    remoteSyncStatus: null,
    operations: {},
    activeProject: "p1",
  });
});

describe("BackupModal", () => {
  it("shows the configured destination", async () => {
    open();

    expect(await screen.findByLabelText("資料夾路徑")).toHaveValue("/backups");
    expect(screen.getByLabelText("備份到伺服器上的資料夾")).toBeChecked();
    expect(screen.getByRole("button", { name: "備份整個工作區" })).toBeInTheDocument();
    // Only one backend exists, so there is nothing to pick between.
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
  });

  it("turns folder backup on as an unsaved change", async () => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue({ ...FOLDER_CONFIG, kind: "", folder: "" });
    const { user } = open();
    const toggle = await screen.findByLabelText("備份到伺服器上的資料夾");
    expect(toggle).not.toBeChecked();
    expect(screen.queryByLabelText("資料夾路徑")).not.toBeInTheDocument();

    await user.click(toggle);

    expect(screen.getByLabelText("資料夾路徑")).toHaveValue("");
    expect(screen.getByRole("button", { name: "儲存設定" })).toBeEnabled();
  });
});

describe("BackupModal backup panel", () => {
  it("lists what the backend already holds", async () => {
    open();

    expect(await screen.findByText("workspace.veproj")).toBeInTheDocument();
    expect(screen.getByText(/4\.0 KB/)).toBeInTheDocument();
    expect(screen.getByText("上次備份：尚未備份")).toBeInTheDocument();
  });

  // Pushing what is on screen rather than what the server has would back up
  // settings nobody chose, so an edited form blocks both push buttons and only
  // 儲存設定 is live.
  it("blocks pushing while the form disagrees with the server", async () => {
    const { user } = open();
    const folder = await screen.findByLabelText("資料夾路徑");

    expect(screen.getByRole("button", { name: "儲存設定" })).toBeDisabled();

    await user.type(folder, "/2");

    expect(screen.getByRole("button", { name: "備份目前專案" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "備份整個工作區" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "儲存設定" })).toBeEnabled();
  });

  it("hides the schedule and the listing when backup is turned off", async () => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue({ ...FOLDER_CONFIG, kind: "" });
    open();

    await waitFor(() => expect(screen.getByLabelText("備份到伺服器上的資料夾")).not.toBeChecked());
    expect(screen.queryByText("自動排程備份整個工作區")).not.toBeInTheDocument();
    expect(screen.queryByText("備份（新到舊）")).not.toBeInTheDocument();
  });

  it("asks how long history should reach, not just how many files", async () => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue({
      ...FOLDER_CONFIG,
      autoBackup: true,
      intervalHours: 12,
      retainBundles: 10,
    });
    open();

    expect(await screen.findByLabelText("保留份數")).toHaveValue(10);
    expect(screen.getByText(/約 5 天/)).toBeInTheDocument();
  });
});
