// Theme packs in Settings › General: which one this viewer sees, importing and
// deleting them, and for administrators the one everybody sees by default.

import { useEffect, useRef } from "react";
import type { ThemePack } from "../api";
import { formatSize } from "../components/backup/format";
import { confirmAction } from "../components/ConfirmDialog";
import { InlineNotice } from "../components/InteractionPrimitives";
import { useTeamServer, useViewer } from "../components/SignIn";
import { useI18n } from "../i18n";
import type { Language } from "../preferences";
import { SettingsDisclosure } from "../settings/SettingsDisclosure";
import { useApp, usePreference } from "../store";
import { activeThemePack, useThemePacks } from "./themePackStore";

function packName(pack: ThemePack, language: Language): string {
  return pack.name[language] || pack.name.en;
}

/** The row in General, saying what this viewer sees before it is opened. */
export function ThemePacksRow({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  const { t, language } = useI18n();
  const picked = usePreference("themePack");
  const packs = useThemePacks((s) => s.packs);
  const defaultId = useThemePacks((s) => s.defaultId);
  const known = useThemePacks((s) => s.loaded && s.error === null && !(s.loading && s.packs.length === 0));
  useEffect(() => useThemePacks.getState().ensureLoaded(), []);

  const active = activeThemePack(packs, defaultId, picked);
  const value = !known ? "" : active ? packName(active, language) : t("themePacks.builtIn");
  return (
    <SettingsDisclosure label={t("themePacks.title")} value={value} open={open} onToggle={onToggle}>
      <ThemePacksPanel />
    </SettingsDisclosure>
  );
}

export function ThemePacksPanel() {
  const { t, language } = useI18n();
  const viewer = useViewer();
  const teamServer = useTeamServer();
  const admin = Boolean(teamServer && viewer?.role === "admin");
  const picked = usePreference("themePack");
  const packs = useThemePacks((s) => s.packs);
  const defaultId = useThemePacks((s) => s.defaultId);
  const error = useThemePacks((s) => s.error);
  const loaded = useThemePacks((s) => s.loaded && !s.loading);
  const upload = useThemePacks((s) => s.upload);
  const uploadError = useThemePacks((s) => s.uploadError);
  const busy = useThemePacks((s) => s.busy);
  const actionError = useThemePacks((s) => s.actionError);
  const fileInput = useRef<HTMLInputElement | null>(null);

  // Fresh each time it is opened: what may be deleted depends on who is asking.
  useEffect(() => {
    void useThemePacks.getState().refresh();
  }, []);

  const builtIn = t("themePacks.builtIn");
  const siteDefault = activeThemePack(packs, defaultId, "");
  // A pack with the id "none" cannot be picked: the preference reads that word
  // as the built-in people.
  const pickable = packs.filter((pack) => pack.id !== "none");
  const yours = picked === "none" || pickable.some((pack) => pack.id === picked) ? picked : "";

  const remove = async (pack: ThemePack) => {
    const name = packName(pack, language);
    const confirmed = await confirmAction({
      title: t("themePacks.deleteTitle"),
      description: t("themePacks.deleteBody", { name }),
      confirmLabel: t("common.delete"),
      tone: "danger",
    });
    if (confirmed) await useThemePacks.getState().deletePack(pack.id);
  };

  const uploaded = upload !== null && upload.sent >= upload.total;

  return (
    <>
      <p className="settings-hint">{t("themePacks.note")}</p>
      <div className="settings-row">
        <span>{t("themePacks.yours")}</span>
        <select
          aria-label={t("themePacks.yours")}
          value={yours}
          onChange={(event) => useApp.getState().updateUIPreference("themePack", event.target.value)}
        >
          <option value="">
            {t("themePacks.followSite", { name: siteDefault ? packName(siteDefault, language) : builtIn })}
          </option>
          <option value="none">{builtIn}</option>
          {pickable.map((pack) => (
            <option key={pack.id} value={pack.id}>
              {packName(pack, language)}
            </option>
          ))}
        </select>
      </div>
      {admin && (
        <div className="settings-row">
          <span>{t("themePacks.siteDefault")}</span>
          <select
            aria-label={t("themePacks.siteDefault")}
            value={siteDefault?.id ?? ""}
            disabled={busy}
            onChange={(event) => void useThemePacks.getState().setDefault(event.target.value)}
          >
            <option value="">{builtIn}</option>
            {packs.map((pack) => (
              <option key={pack.id} value={pack.id}>
                {packName(pack, language)}
              </option>
            ))}
          </select>
        </div>
      )}
      {error !== null && <InlineNotice kind="error">{error || t("themePacks.actionFailed")}</InlineNotice>}
      {packs.length > 0 ? (
        <ul className="backup-list">
          {packs.map((pack) => {
            const name = packName(pack, language);
            const about = [
              pack.version,
              pack.author && t("themePacks.by", { author: pack.author }),
              formatSize(pack.sizeBytes),
              pack.mine ? t("themePacks.importedByYou") : t("themePacks.importedBy", { name: pack.owner }),
            ];
            return (
              <li key={pack.id}>
                <div className="backup-list-meta">
                  <span className="backup-list-name">{name}</span>
                  <span className="backup-list-sub">{about.filter(Boolean).join(" · ")}</span>
                </div>
                {pack.canDelete && (
                  <button
                    type="button"
                    className="danger"
                    disabled={busy}
                    aria-label={t("themePacks.deleteNamed", { name })}
                    onClick={() => void remove(pack)}
                  >
                    {t("common.delete")}
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      ) : (
        loaded && error === null && <p className="settings-hint">{t("themePacks.empty")}</p>
      )}
      {actionError !== null && (
        <InlineNotice kind="error">{actionError || t("themePacks.actionFailed")}</InlineNotice>
      )}
      <div className="settings-row">
        <button type="button" disabled={upload !== null} onClick={() => fileInput.current?.click()}>
          {t("themePacks.import")}
        </button>
        <input
          ref={fileInput}
          type="file"
          accept=".zip"
          hidden
          aria-label={t("themePacks.import")}
          onChange={(event) => {
            const file = event.target.files?.[0];
            // Cleared so the same file can be picked again after a refusal.
            event.target.value = "";
            if (file) void useThemePacks.getState().importPack(file);
          }}
        />
        {upload && (
          <>
            <progress
              className="station-progress-bar"
              max={uploaded ? undefined : upload.total}
              value={uploaded ? undefined : upload.sent}
              aria-label={t("themePacks.import")}
            />
            <span className="settings-hint" role="status">
              {uploaded
                ? t("themePacks.checking", { name: upload.name })
                : t("themePacks.uploading", {
                    name: upload.name,
                    percent: Math.floor((upload.sent / Math.max(upload.total, 1)) * 100),
                  })}
            </span>
          </>
        )}
      </div>
      {uploadError !== null && <InlineNotice kind="error">{uploadError || t("themePacks.importFailed")}</InlineNotice>}
    </>
  );
}
