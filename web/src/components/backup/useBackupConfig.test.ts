import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type RemoteConfig } from "../../api";
import { readPreferences } from "../../preferences/storage";
import { useApp } from "../../store";
import { resetCoalescing } from "../../state/coalesce";
import {
  DEFAULT_RETAIN,
  draftFromConfig,
  draftIsDirty,
  retentionOf,
  useBackupConfig,
  type BackupDraft,
} from "./useBackupConfig";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      getRemoteConfig: vi.fn(),
      putRemoteConfig: vi.fn(),
      listRemoteBundles: vi.fn(),
      getRemoteSyncStatus: vi.fn(),
      pushRemote: vi.fn(),
      flushRemoteSync: vi.fn(),
      importRemote: vi.fn(),
    },
  };
});

const CONFIG: RemoteConfig = {
  kind: "folder",
  folder: "/backups",
  driveFolderId: "",
  autoBackup: true,
  intervalHours: 24,
  retainBundles: 14,
  driveAvailable: false,
  driveConnected: false,
  driveNeedsReauth: false,
  lastBackupAt: "2026-01-02T03:04:05Z",
};

const BUNDLE = {
  id: "b1",
  name: "workspace.veproj",
  size: 1024,
  modified: "2026-01-02T03:04:05Z",
};

beforeEach(() => {
  resetCoalescing();
  vi.mocked(api.getRemoteConfig).mockResolvedValue(CONFIG);
  vi.mocked(api.putRemoteConfig).mockResolvedValue(CONFIG);
  vi.mocked(api.listRemoteBundles).mockResolvedValue({ backend: "folder", bundles: [BUNDLE] });
  vi.mocked(api.getRemoteSyncStatus).mockResolvedValue({
    state: "synced",
    localHash: "h",
    lastPushedHash: "h",
    lastPushedBundleId: "b1",
    remoteLatest: BUNDLE,
    remoteHash: "h",
    lastBackupAt: CONFIG.lastBackupAt,
  });
  vi.mocked(api.pushRemote).mockResolvedValue({ backend: "folder", bundle: BUNDLE });
  vi.mocked(api.flushRemoteSync).mockResolvedValue({ state: "synced", bundle: BUNDLE });
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    remoteConfig: null,
    remoteBundles: null,
    remoteSyncStatus: null,
    operations: {},
    activeProject: "p1",
  });
});

describe("draft arithmetic", () => {
  it("reads the server's -1 as pruning turned off, keeping the last count", () => {
    const draft = draftFromConfig({ ...CONFIG, retainBundles: -1 });
    expect(draft.pruneOld).toBe(false);
    // Re-ticking the box must not silently change the policy to something else.
    expect(draft.retainBundles).toBe(DEFAULT_RETAIN);
    expect(retentionOf(draft)).toBe(-1);
    expect(retentionOf({ ...draft, pruneOld: true })).toBe(DEFAULT_RETAIN);
  });

  it("treats a zero interval as the server's 24 hour default", () => {
    expect(draftFromConfig({ ...CONFIG, intervalHours: 0 }).intervalHours).toBe(24);
    expect(draftIsDirty(draftFromConfig({ ...CONFIG, intervalHours: 0 }), {
      ...CONFIG,
      intervalHours: 0,
    })).toBe(false);
  });

  // Finishing the OAuth round trip leaves Drive connected but not chosen. The
  // form preselects it, and that showing as unsaved is the point: the user
  // still has to press 儲存設定.
  it("preselects a freshly connected Drive as an unsaved change", () => {
    const config = { ...CONFIG, kind: "" as const, driveConnected: true, driveAvailable: true };
    const draft = draftFromConfig(config);
    expect(draft.kind).toBe("drive");
    expect(draftIsDirty(draft, config)).toBe(true);
  });

  it("counts an untouched draft as clean", () => {
    expect(draftIsDirty(draftFromConfig(CONFIG), CONFIG)).toBe(false);
  });
});

describe("useBackupConfig", () => {
  it("fills the form from the server and lists what it holds", async () => {
    const { result } = renderHook(() => useBackupConfig());

    await waitFor(() => expect(result.current.config).toEqual(CONFIG));
    expect(result.current.draft).toMatchObject<Partial<BackupDraft>>({
      kind: "folder",
      folder: "/backups",
      retainBundles: 14,
    });
    expect(result.current.dirty).toBe(false);
    expect(result.current.canBackup).toBe(true);
    await waitFor(() => expect(result.current.bundles).toEqual([BUNDLE]));
  });

  it("does not ask for a listing before a backend is chosen", async () => {
    vi.mocked(api.getRemoteConfig).mockResolvedValue({ ...CONFIG, kind: "" });

    const { result } = renderHook(() => useBackupConfig());

    await waitFor(() => expect(result.current.config).not.toBeNull());
    expect(api.listRemoteBundles).not.toHaveBeenCalled();
    expect(result.current.canBackup).toBe(false);
  });

  it("marks the form dirty as soon as a field is edited", async () => {
    const { result } = renderHook(() => useBackupConfig());
    await waitFor(() => expect(result.current.config).toEqual(CONFIG));

    act(() => result.current.patch({ folder: "/elsewhere" }));

    expect(result.current.dirty).toBe(true);
  });

  it("sends -1 for retention when pruning is unticked", async () => {
    const { result } = renderHook(() => useBackupConfig());
    await waitFor(() => expect(result.current.config).toEqual(CONFIG));

    act(() => result.current.patch({ pruneOld: false }));
    await act(() => result.current.save());

    expect(api.putRemoteConfig).toHaveBeenCalledWith(
      expect.objectContaining({ retainBundles: -1 }),
    );
    expect(result.current.note).toEqual({ text: "備份設定已儲存。", kind: "ok" });
  });

  it("names the bundle a push produced", async () => {
    const { result } = renderHook(() => useBackupConfig());
    await waitFor(() => expect(result.current.config).toEqual(CONFIG));

    await act(() => result.current.push());

    expect(api.pushRemote).toHaveBeenCalledWith("p1");
    expect(result.current.note).toEqual({
      text: "已備份：workspace.veproj",
      kind: "ok",
    });
  });

  // The refusal is the sync model working. Repeating the server's wording
  // would leave the user with nothing to do about it.
  it("turns a flush conflict into directions", async () => {
    vi.mocked(api.flushRemoteSync).mockRejectedValue(
      new Error("conflict: remote snapshot changed"),
    );
    const { result } = renderHook(() => useBackupConfig());
    await waitFor(() => expect(result.current.config).toEqual(CONFIG));

    await act(() => result.current.pushWorkspace());

    expect(result.current.note?.kind).toBe("error");
    expect(result.current.note?.text).toContain("從 Drive 匯入");
  });

  it("passes a plain flush failure through unchanged", async () => {
    vi.mocked(api.flushRemoteSync).mockRejectedValue(new Error("磁碟已滿"));
    const { result } = renderHook(() => useBackupConfig());
    await waitFor(() => expect(result.current.config).toEqual(CONFIG));

    await act(() => result.current.pushWorkspace());

    expect(result.current.note).toEqual({ text: "磁碟已滿", kind: "error" });
  });

  it("reports a config that could not be loaded at all", async () => {
    vi.mocked(api.getRemoteConfig).mockRejectedValue(new Error("伺服器沒回應"));

    const { result } = renderHook(() => useBackupConfig());

    await waitFor(() =>
      expect(result.current.note).toEqual({ text: "伺服器沒回應", kind: "error" }),
    );
    expect(result.current.config).toBeNull();
  });
});
