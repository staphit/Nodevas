import { useEffect, useRef } from "react";

import { packName, usePacks } from "../../game/packStore";
import { setStationPack } from "../../game/stageSkin";
import { useI18n } from "../../i18n";
import { useViewer } from "../SignIn";
import type { SettingsNotify } from "./notify";

/**
 * Settings → Appearance → View packs (docs/view-packs.md): everyone sees the
 * workspace's packs and can open Station drawn with one; admins install a
 * .zip and remove packs. On a server without accounts the local user is the
 * admin.
 */
export function ViewPacksEditor({ notify }: { notify: SettingsNotify }) {
  const { language, t } = useI18n();
  const viewer = useViewer();
  const admin = !viewer || viewer.role === "admin";
  const packs = usePacks((s) => s.packs);
  const loaded = usePacks((s) => s.loaded);
  const busy = usePacks((s) => s.busy);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    void usePacks.getState().load();
  }, []);

  const install = async (file: File | undefined) => {
    if (!file) return;
    notify.onError(null);
    const failed = await usePacks.getState().install(file);
    if (failed) notify.onError(t("packs.installFailed", { reason: failed }));
    else notify.onNotice(t("packs.installed", { name: file.name }));
    if (fileRef.current) fileRef.current.value = "";
  };

  const remove = async (id: string, name: string) => {
    notify.onError(null);
    const failed = await usePacks.getState().remove(id);
    if (failed) notify.onError(failed);
    else notify.onNotice(t("packs.removed", { name }));
  };

  return (
    <div className="view-packs" aria-labelledby="view-packs-title">
      <h3 id="view-packs-title">{t("packs.title")}</h3>
      <p className="settings-hint">{t("packs.hint")}</p>
      {loaded && packs.length === 0 && <p className="settings-hint">{t("packs.none")}</p>}
      {packs.length > 0 && (
        <ul className="settings-list view-pack-list">
          {packs.map((pack) => {
            const name = packName(pack, language);
            return (
              <li key={pack.id} className="view-pack-row">
                <span className="view-pack-name">
                  <strong>{name}</strong>
                  <span className="view-pack-meta">
                    {t(pack.layout === "rooms" ? "packs.layout.rooms" : "packs.layout.path")} · {pack.id}
                  </span>
                </span>
                <button type="button" onClick={() => setStationPack(pack)}>
                  {t("packs.use")}
                </button>
                {admin && (
                  <button type="button" className="danger" disabled={busy} onClick={() => void remove(pack.id, name)}>
                    {t("packs.remove")}
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {admin && (
        <div className="settings-row">
          <input
            ref={fileRef}
            type="file"
            accept=".zip,application/zip"
            className="sr-only"
            id="view-pack-file"
            onChange={(event) => void install(event.target.files?.[0])}
          />
          <button type="button" disabled={busy} onClick={() => fileRef.current?.click()}>
            {busy ? t("packs.installing") : t("packs.install")}
          </button>
        </div>
      )}
    </div>
  );
}
