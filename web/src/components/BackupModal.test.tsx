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
      testRemoteGit: vi.fn(),
      keepLocalRemoteGit: vi.fn(),
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
    // The backends are a short choice in plain sight, not a menu.
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

const GIT_CONFIG: RemoteConfig = {
  ...FOLDER_CONFIG,
  kind: "git",
  folder: "",
  autoBackup: true,
  gitUrl: "https://github.com/me/backup.git",
  gitUser: "",
  hasGitToken: true,
};

describe("BackupModal git remote", () => {
  beforeEach(() => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue(GIT_CONFIG);
    vi.mocked(api.putRemoteConfig).mockResolvedValue(GIT_CONFIG);
    vi.mocked(api.listRemoteBundles).mockResolvedValue({ backend: "git", bundles: [] });
    vi.mocked(api.getRemoteSyncStatus).mockResolvedValue({
      state: "conflict",
      localHash: "",
      lastPushedHash: "",
      lastPushedBundleId: "",
      remoteLatest: null,
      remoteHash: "",
      lastBackupAt: GIT_CONFIG.lastBackupAt,
      projects: [
        {
          project: "alpha",
          branch: "nodevas/alpha",
          state: "synced",
          lastPush: { at: "2026-01-02T03:04:05Z", branch: "nodevas/alpha", ok: true },
        },
        { project: "beta", branch: "nodevas/beta", state: "conflict" },
      ],
    });
    vi.mocked(api.testRemoteGit).mockResolvedValue({ ok: true, branches: 2 });
    vi.mocked(api.keepLocalRemoteGit).mockResolvedValue({ branch: "nodevas/beta-local-20260102-030405" });
  });

  it("offers a git remote with URL, user and token fields", async () => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue({ ...FOLDER_CONFIG, kind: "", folder: "" });
    const { user } = open();

    await user.click(await screen.findByLabelText("備份到 git 遠端儲存庫"));

    expect(screen.getByLabelText("儲存庫網址")).toHaveValue("");
    expect(screen.getByLabelText("使用者名稱（選填）")).toHaveAttribute("placeholder", "x-access-token");
    expect(screen.getByLabelText("存取權杖")).toHaveAttribute("type", "password");
    expect(screen.queryByLabelText("資料夾路徑")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "測試連線" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "儲存設定" })).toBeEnabled();
  });

  // The token never comes back from the server; the form says one is saved
  // and leaves the field empty, and an empty field keeps it.
  it("shows a saved token without its value and keeps it on save", async () => {
    const { user } = open();
    const token = await screen.findByLabelText("存取權杖");

    expect(token).toHaveValue("");
    expect(token).toHaveAttribute("placeholder", "已儲存，留空則沿用");
    expect(screen.getByLabelText("儲存庫網址")).toHaveValue("https://github.com/me/backup.git");
    // A git remote keeps every commit: there is no retention to set.
    expect(screen.queryByText("排程備份成功後清理較舊的備份")).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("使用者名稱（選填）"), "me");
    await user.click(screen.getByRole("button", { name: "儲存設定" }));

    const sent = vi.mocked(api.putRemoteConfig).mock.calls.at(-1)?.[0];
    expect(sent).toMatchObject({ kind: "git", gitUrl: "https://github.com/me/backup.git", gitUser: "me" });
    expect(sent).not.toHaveProperty("gitToken");
  });

  it("saves, then tests the connection", async () => {
    const { user } = open();
    await user.type(await screen.findByLabelText("存取權杖"), "ghp_new");

    await user.click(screen.getByRole("button", { name: "測試連線" }));

    expect(api.putRemoteConfig).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "git", gitToken: "ghp_new" }),
    );
    expect(await screen.findByText("連線成功。遠端有 2 個 Nodevas 分支。")).toBeInTheDocument();
    expect(api.testRemoteGit).toHaveBeenCalledTimes(1);
  });

  it("lists each project's branch and offers keep local only where it diverged", async () => {
    const { user } = open();

    expect(await screen.findByText("alpha → nodevas/alpha")).toBeInTheDocument();
    expect(screen.getByText("已是最新")).toBeInTheDocument();
    expect(screen.getByText(/上次推送 .*：成功/)).toBeInTheDocument();
    expect(screen.getByText("本機與遠端已分歧")).toBeInTheDocument();
    expect(screen.getByText("尚未推送")).toBeInTheDocument();
    // No bundles to restore from a git remote.
    expect(screen.queryByText("備份（新到舊）")).not.toBeInTheDocument();

    const keep = screen.getAllByRole("button", { name: "保留本機" });
    expect(keep).toHaveLength(1);
    await user.click(keep[0]);

    expect(api.keepLocalRemoteGit).toHaveBeenCalledWith("beta");
    expect(
      await screen.findByText("已把 beta 推送到 nodevas/beta-local-20260102-030405。"),
    ).toBeInTheDocument();
  });
});
