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
      getDriveCredentials: vi.fn(),
      listDriveFolders: vi.fn(),
      listDriveBundles: vi.fn(),
      pushRemote: vi.fn(),
      flushRemoteSync: vi.fn(),
      driveAuthURL: (source: string) => `/api/remote/drive/auth?source=${source}`,
    },
  };
});

const FOLDER_CONFIG: RemoteConfig = {
  kind: "folder",
  folder: "/backups",
  driveFolderId: "",
  autoBackup: false,
  intervalHours: 24,
  retainBundles: 14,
  driveAvailable: false,
  driveConnected: false,
  driveNeedsReauth: false,
  lastBackupAt: "0001-01-01T00:00:00Z",
};

const DRIVE_CONFIG: RemoteConfig = {
  ...FOLDER_CONFIG,
  kind: "drive",
  folder: "",
  driveAvailable: true,
  driveConnected: true,
};

const BUNDLE = {
  id: "b1",
  name: "workspace.veproj",
  size: 4096,
  modified: "2026-01-02T03:04:05Z",
};

function open(props: Partial<Parameters<typeof BackupModal>[0]> = {}) {
  const onClose = vi.fn();
  render(<BackupModal onClose={onClose} {...props} />);
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
  vi.mocked(api.getDriveCredentials).mockResolvedValue({ configured: true, source: "app" });
  vi.mocked(api.listDriveFolders).mockResolvedValue({
    parent: "root",
    folders: [{ id: "f1", name: "備份" }],
  });
  vi.mocked(api.listDriveBundles).mockResolvedValue({ parent: "root", bundles: [] });
  vi.mocked(api.pushRemote).mockResolvedValue({ backend: "folder", bundle: BUNDLE });
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    remoteConfig: null,
    remoteBundles: null,
    remoteSyncStatus: null,
    driveCredentials: null,
    operations: {},
    activeProject: "p1",
  });
});

describe("BackupModal tabs", () => {
  it("opens on the backup tab and shows the configured destination", async () => {
    open();

    expect(await screen.findByLabelText("資料夾路徑")).toHaveValue("/backups");
    expect(screen.getByRole("tab", { name: "備份" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("button", { name: "備份整個工作區" })).toBeInTheDocument();
  });

  // The two jobs share a dialog but not a footer: importing has its own single
  // action, and offering "備份目前專案" next to it would be an invitation to
  // press the wrong one.
  it("swaps the whole panel, footer included, when the import tab is chosen", async () => {
    const { user } = open();
    await screen.findByLabelText("資料夾路徑");

    await user.click(screen.getByRole("tab", { name: "從 Drive 匯入" }));

    expect(screen.queryByLabelText("資料夾路徑")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "備份整個工作區" })).not.toBeInTheDocument();
    expect(screen.getByText("尚未設定 Google OAuth")).toBeInTheDocument();
  });

  it("opens straight on the import tab when asked to", async () => {
    open({ initialTab: "import" });

    expect(await screen.findByText("尚未設定 Google OAuth")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "從 Drive 匯入" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("goes back to the backup tab with the form still filled in", async () => {
    const { user } = open();
    await screen.findByLabelText("資料夾路徑");

    await user.click(screen.getByRole("tab", { name: "從 Drive 匯入" }));
    await user.click(screen.getByRole("tab", { name: "備份" }));

    expect(screen.getByLabelText("資料夾路徑")).toHaveValue("/backups");
  });
});

describe("BackupModal backup tab", () => {
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

    await waitFor(() => expect(screen.getByLabelText("備份後端")).toHaveValue(""));
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

describe("BackupModal Drive folder picker", () => {
  beforeEach(() => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue(DRIVE_CONFIG);
    vi.mocked(api.listRemoteBundles).mockResolvedValue({ backend: "drive", bundles: [] });
  });

  it("stays closed until it is asked for", async () => {
    open();

    await screen.findByLabelText("Drive 資料夾（選填）");
    expect(api.listDriveFolders).not.toHaveBeenCalled();
    expect(screen.getByText("目前：雲端硬碟根目錄")).toBeInTheDocument();
  });

  // Walking into a folder is the choice: there is no second Confirm, and a
  // bundle filed somewhere the user only passed through would be a surprise.
  it("chooses the folder that is walked into", async () => {
    const { user } = open();
    await screen.findByLabelText("Drive 資料夾（選填）");

    await user.click(screen.getByRole("button", { name: "瀏覽" }));
    await user.click(await screen.findByRole("button", { name: "備份" }));

    expect(screen.getByLabelText("Drive 資料夾（選填）")).toHaveValue("f1");
    expect(screen.getByText("目前：備份")).toBeInTheDocument();
    // Choosing a destination is a change the user still has to save.
    expect(screen.getByRole("button", { name: "儲存設定" })).toBeEnabled();
  });

  it("returns to the drive root without a folder id", async () => {
    const { user } = open();
    await screen.findByLabelText("Drive 資料夾（選填）");

    await user.click(screen.getByRole("button", { name: "瀏覽" }));
    await user.click(await screen.findByRole("button", { name: "備份" }));
    await user.click(screen.getByRole("button", { name: "根目錄" }));

    await waitFor(() =>
      expect(screen.getByLabelText("Drive 資料夾（選填）")).toHaveValue(""),
    );
    expect(screen.getByText("目前：雲端硬碟根目錄")).toBeInTheDocument();
  });
});
