import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { RemoteConfig, RemoteSyncStatus } from "../api";
import { resetCoalescing } from "./coalesce";
import { driveReady, hasEverBackedUp, remoteScope, sortBundles } from "./remoteSlice";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getRemoteConfig: vi.fn(),
      putRemoteConfig: vi.fn(),
      listRemoteBundles: vi.fn(),
      getRemoteSyncStatus: vi.fn(),
      flushRemoteSync: vi.fn(),
      pushRemote: vi.fn(),
      importRemote: vi.fn(),
      connectDriveWorkspace: vi.fn(),
      getDriveCredentials: vi.fn(),
      putDriveCredentials: vi.fn(),
      deleteDriveCredentials: vi.fn(),
      disconnectDrive: vi.fn(),
      getNotifySettings: vi.fn(),
      putNotifySettings: vi.fn(),
      testNotify: vi.fn(),
    },
  };
});

const CONFIG: RemoteConfig = {
  kind: "drive",
  folder: "",
  driveFolderId: "",
  autoBackup: true,
  intervalHours: 24,
  retainBundles: 14,
  driveAvailable: true,
  driveConnected: true,
  driveNeedsReauth: false,
  lastBackupAt: "2026-01-02T03:04:05Z",
};

const BUNDLE = {
  id: "b1",
  name: "workspace-2026.veproj",
  size: 2048,
  modified: "2026-01-02T03:04:05Z",
};

const SYNC: RemoteSyncStatus = {
  state: "synced",
  localHash: "h",
  lastPushedHash: "h",
  lastPushedBundleId: "b1",
  remoteLatest: BUNDLE,
  remoteHash: "h",
  lastBackupAt: CONFIG.lastBackupAt,
};

const NOTIFY = {
  enabled: true,
  leadMinutes: 60,
  smtpHost: "smtp.example.com",
  smtpPort: 587,
  smtpUser: "me",
  smtpPass: "",
  from: "me@example.com",
  defaultTo: "team@example.com",
};

beforeEach(() => {
  resetCoalescing();
  vi.mocked(api.getRemoteConfig).mockResolvedValue(CONFIG);
  vi.mocked(api.putRemoteConfig).mockResolvedValue(CONFIG);
  vi.mocked(api.listRemoteBundles).mockResolvedValue({ backend: "drive", bundles: [BUNDLE] });
  vi.mocked(api.getRemoteSyncStatus).mockResolvedValue(SYNC);
  vi.mocked(api.flushRemoteSync).mockResolvedValue({ state: "synced", bundle: BUNDLE });
  vi.mocked(api.pushRemote).mockResolvedValue({ backend: "drive", bundle: BUNDLE });
  vi.mocked(api.importRemote).mockResolvedValue({ active: "還原的專案", root: "/ws" });
  vi.mocked(api.connectDriveWorkspace).mockResolvedValue({
    active: "雲端專案",
    root: "/ws",
    folderId: "f1",
    bundle: BUNDLE,
  });
  vi.mocked(api.getDriveCredentials).mockResolvedValue({ configured: true, source: "app" });
  vi.mocked(api.putDriveCredentials).mockResolvedValue({ configured: true, source: "app" });
  vi.mocked(api.deleteDriveCredentials).mockResolvedValue({ configured: false, source: "none" });
  vi.mocked(api.disconnectDrive).mockResolvedValue({ ok: true });
  vi.mocked(api.getNotifySettings).mockResolvedValue({
    settings: NOTIFY,
    hasPassword: false,
  });
  vi.mocked(api.putNotifySettings).mockResolvedValue({ ok: true });
  vi.mocked(api.testNotify).mockResolvedValue({ ok: true });
  useApp.setState({
    remoteConfig: null,
    remoteBundles: null,
    remoteSyncStatus: null,
    driveCredentials: null,
    notifySettings: null,
    notifyHasPassword: false,
    operations: {},
    lastOperation: null,
    activeProject: "p1",
    refreshProjects: vi.fn().mockResolvedValue(undefined),
    switchProject: vi.fn().mockResolvedValue(undefined),
  });
});

describe("reads", () => {
  it("publishes the server's config to every reader", async () => {
    await useApp.getState().refreshRemoteConfig();
    expect(useApp.getState().remoteConfig).toEqual(CONFIG);
  });

  // The dialog opening and the banner mounting ask for the same thing at the
  // same moment; that is one read, not two.
  it("answers simultaneous readers with one request", async () => {
    await Promise.all([
      useApp.getState().refreshRemoteSyncStatus(),
      useApp.getState().refreshRemoteSyncStatus(),
    ]);
    expect(api.getRemoteSyncStatus).toHaveBeenCalledTimes(1);
    expect(useApp.getState().remoteSyncStatus).toEqual(SYNC);
  });

  it("lets a failed read reject so the panel can say why", async () => {
    vi.mocked(api.listRemoteBundles).mockRejectedValue(new Error("讀不到"));
    await expect(useApp.getState().refreshRemoteBundles()).rejects.toThrow("讀不到");
    expect(useApp.getState().remoteBundles).toBeNull();
  });
});

describe("saveRemoteConfig", () => {
  // The server clamps a retention count below its floor, and the number that
  // will actually be used is the one worth showing.
  it("adopts the effective config the server replies with", async () => {
    vi.mocked(api.putRemoteConfig).mockResolvedValue({ ...CONFIG, retainBundles: 3 });

    const result = await useApp.getState().saveRemoteConfig({ kind: "drive", retainBundles: 1 });

    expect(result.ok).toBe(true);
    expect(useApp.getState().remoteConfig?.retainBundles).toBe(3);
    expect(useApp.getState().operations[remoteScope.config()]).toMatchObject({
      status: "saved",
      operation: "remote.saveConfig",
    });
  });

  it("reports a refusal by value instead of throwing", async () => {
    vi.mocked(api.putRemoteConfig).mockRejectedValue(new Error("資料夾不存在"));

    const result = await useApp.getState().saveRemoteConfig({ kind: "folder", folder: "/nope" });

    expect(result).toMatchObject({ ok: false, message: "資料夾不存在" });
    expect(useApp.getState().operations[remoteScope.config()]).toMatchObject({
      status: "error",
      message: "資料夾不存在",
    });
  });

  // Turning the backend off leaves no listing to show, and keeping the old one
  // would claim those bundles are still reachable.
  it("drops the bundle listing when backup is switched off", async () => {
    useApp.setState({ remoteBundles: [BUNDLE] });
    vi.mocked(api.putRemoteConfig).mockResolvedValue({ ...CONFIG, kind: "" });

    await useApp.getState().saveRemoteConfig({ kind: "" });

    expect(useApp.getState().remoteBundles).toBeNull();
  });
});

describe("pushes", () => {
  // The banner and the "上次備份" line are both stale the moment a bundle
  // lands, and neither of them is the thing that pushed it.
  it("re-reads config, listing and sync status after a push", async () => {
    const result = await useApp.getState().pushRemoteBundle("p1");

    expect(api.pushRemote).toHaveBeenCalledWith("p1");
    expect(result).toMatchObject({ ok: true, value: BUNDLE });
    expect(api.getRemoteConfig).toHaveBeenCalled();
    expect(useApp.getState().remoteBundles).toEqual([BUNDLE]);
    expect(useApp.getState().remoteSyncStatus).toEqual(SYNC);
  });

  // The bundle is on the remote either way; a read that failed on the way back
  // is not a failed backup.
  it("keeps a push successful when the follow-up read fails", async () => {
    vi.mocked(api.getRemoteConfig).mockRejectedValue(new Error("離線"));

    const result = await useApp.getState().pushRemoteBundle();

    expect(result.ok).toBe(true);
  });

  it("surfaces a rejected workspace flush as a message, not a throw", async () => {
    vi.mocked(api.flushRemoteSync).mockRejectedValue(
      new Error("conflict: remote snapshot changed"),
    );

    const result = await useApp.getState().flushRemoteSync();

    expect(result).toMatchObject({ ok: false });
    expect(useApp.getState().operations[remoteScope.bundle()]).toMatchObject({
      status: "error",
    });
  });
});

describe("restores", () => {
  // A restore never overwrites anything: it lands as a new project, and the
  // user is taken to it rather than told about it.
  it("opens the project a bundle was restored into", async () => {
    const result = await useApp.getState().importRemoteBundle("b1");

    expect(result).toMatchObject({ ok: true, value: "還原的專案" });
    expect(useApp.getState().switchProject).toHaveBeenCalledWith("還原的專案");
  });

  it("opens the project a Drive snapshot was imported into", async () => {
    const result = await useApp.getState().connectDriveWorkspace("f1", "b1", "快照");

    expect(api.connectDriveWorkspace).toHaveBeenCalledWith("f1", "b1", "快照");
    expect(result).toMatchObject({ ok: true, value: "雲端專案" });
    expect(useApp.getState().switchProject).toHaveBeenCalledWith("雲端專案");
  });
});

describe("drive credentials", () => {
  // Credentials decide `driveAvailable`, which the backend picker reads off the
  // config — so the config is stale the moment they change.
  it("re-reads the config after credentials are stored", async () => {
    await useApp.getState().saveDriveCredentials("id", "secret");

    expect(api.putDriveCredentials).toHaveBeenCalledWith("id", "secret");
    expect(useApp.getState().driveCredentials).toEqual({ configured: true, source: "app" });
    expect(api.getRemoteConfig).toHaveBeenCalled();
  });

  it("re-reads the config after credentials are cleared", async () => {
    await useApp.getState().clearDriveCredentials();

    expect(useApp.getState().driveCredentials).toEqual({ configured: false, source: "none" });
    expect(api.getRemoteConfig).toHaveBeenCalled();
  });

  it("re-reads the config after disconnecting", async () => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue({ ...CONFIG, driveConnected: false });

    const result = await useApp.getState().disconnectDrive();

    expect(result.ok).toBe(true);
    expect(useApp.getState().remoteConfig?.driveConnected).toBe(false);
  });
});

describe("notify settings", () => {
  it("reads the settings and whether a password is already stored", async () => {
    vi.mocked(api.getNotifySettings).mockResolvedValue({
      settings: NOTIFY,
      hasPassword: true,
    });

    await useApp.getState().refreshNotifySettings();

    expect(useApp.getState().notifySettings).toEqual(NOTIFY);
    expect(useApp.getState().notifyHasPassword).toBe(true);
  });

  // An empty field means "keep the stored password", so a filled-in one is the
  // moment a stored password starts existing.
  it("remembers that a password now exists once one is sent", async () => {
    await useApp.getState().saveNotifySettings({ ...NOTIFY, smtpPass: "hunter2" });
    expect(useApp.getState().notifyHasPassword).toBe(true);
  });

  it("persists before asking the server to send the test mail", async () => {
    const result = await useApp.getState().sendNotifyTest(NOTIFY);

    expect(result.ok).toBe(true);
    expect(api.putNotifySettings).toHaveBeenCalledBefore(vi.mocked(api.testNotify));
    expect(api.testNotify).toHaveBeenCalledWith(NOTIFY.defaultTo);
  });

  it("does not send a test mail when saving the settings failed", async () => {
    vi.mocked(api.putNotifySettings).mockRejectedValue(new Error("SMTP 設定不完整"));

    const result = await useApp.getState().sendNotifyTest(NOTIFY);

    expect(result).toMatchObject({ ok: false });
    expect(api.testNotify).not.toHaveBeenCalled();
  });
});

describe("selectors", () => {
  it("treats the RFC3339 zero value as never backed up", () => {
    expect(hasEverBackedUp(null)).toBe(false);
    expect(hasEverBackedUp({ ...CONFIG, lastBackupAt: "0001-01-01T00:00:00Z" })).toBe(false);
    expect(hasEverBackedUp(CONFIG)).toBe(true);
  });

  it("calls Drive ready only when consent is current", () => {
    expect(driveReady(CONFIG)).toBe(true);
    expect(driveReady({ ...CONFIG, driveNeedsReauth: true })).toBe(false);
    expect(driveReady({ ...CONFIG, driveConnected: false })).toBe(false);
    expect(driveReady({ ...CONFIG, driveAvailable: false })).toBe(false);
    expect(driveReady(null)).toBe(false);
  });

  it("orders bundles newest first whatever the backend returned", () => {
    const older = { ...BUNDLE, id: "b0", modified: "2025-12-31T00:00:00Z" };
    expect(sortBundles([older, BUNDLE]).map((bundle) => bundle.id)).toEqual(["b1", "b0"]);
  });
});
