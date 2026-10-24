import { useEffect, useState } from "react";
import { api, RequestError, type ProjectAccess } from "../api";
import { useI18n } from "../i18n";
import { useApp } from "../store";

type BannerState = { kind: "ok" } | { kind: "viewer" } | { kind: "denied" };

const accessListeners = new Set<() => void>();

/** Called when the server says this browser's access changed (the realtime
 * "access-denied" event), so the banner asks again. */
export function notifyAccessChanged(): void {
  for (const listener of accessListeners) listener();
}

/**
 * Says so when the open project is one this person may only look at, or not
 * reach at all, instead of letting every edit fail with a 403 one by one.
 */
export function ProjectAccessBanner() {
  const { t } = useI18n();
  const project = useApp((s) => s.activeProject) ?? "";
  const [state, setState] = useState<BannerState>({ kind: "ok" });
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    const listener = () => setRefreshKey((n) => n + 1);
    accessListeners.add(listener);
    return () => {
      accessListeners.delete(listener);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    api
      .projectAccess(project)
      .then((access: ProjectAccess) => {
        if (!cancelled) setState(access.canWrite ? { kind: "ok" } : { kind: "viewer" });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        // 404 is how the server answers for a project the caller may not
        // read; anything else (offline, signed out) is somebody else's banner.
        setState(error instanceof RequestError && error.status === 404 ? { kind: "denied" } : { kind: "ok" });
      });
    return () => {
      cancelled = true;
    };
  }, [project, refreshKey]);

  if (state.kind === "ok") return null;
  return (
    <div className="project-access-banner" role="status" data-kind={state.kind}>
      {state.kind === "viewer" ? t("members.viewerBanner") : t("members.deniedBanner")}
    </div>
  );
}
