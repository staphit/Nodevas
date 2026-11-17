/**
 * The line after a draft's Create: 「已建立 8 個節點 · 復原」 for ten seconds,
 * then what an undo did, naming the nodes it kept with "Delete anyway" for each.
 * Fixed above the copilot's button (and left of the open panel), so neither
 * covers the other.
 */

import { useEffect } from "react";
import { useI18n } from "../../i18n";
import { IconClose } from "../../icons";
import { TOAST_MS, undoDraftCreate } from "./draftApi";
import { KeptList } from "./KeptList";
import { useDraftUi } from "./draftUi";

export function DraftToast() {
  const { t } = useI18n();
  const toast = useDraftUi((state) => state.toast);
  // Deleting a kept node rewrites the toast; its time runs from when it came.
  const shownAt = toast?.at;

  useEffect(() => {
    if (shownAt === undefined) return;
    const timer = setTimeout(() => {
      if (useDraftUi.getState().toast?.at === shownAt) useDraftUi.getState().showToast(null);
    }, TOAST_MS);
    return () => clearTimeout(timer);
  }, [shownAt]);

  if (!toast) return null;
  let text: string;
  if (toast.kind === "created") {
    text = toast.count === 1 ? t("draft.createdOne") : t("draft.created", { n: toast.count });
  } else if (toast.kind === "undone") {
    const kept = toast.refused.length;
    if (kept > 0) {
      text =
        toast.count === 1 ? t("draft.undoKeptOne", { k: kept }) : t("draft.undoKept", { n: toast.count, k: kept });
    } else text = toast.count === 1 ? t("draft.undoneOne") : t("draft.undone", { n: toast.count });
  } else text = toast.text;

  return (
    <div className={`copilot-toast is-${toast.kind}`} role={toast.kind === "error" ? "alert" : "status"}>
      <span className="copilot-toast-text">{text}</span>
      {toast.kind === "created" && toast.undoable && (
        <>
          <span aria-hidden className="copilot-toast-sep">
            ·
          </span>
          <button type="button" className="copilot-toast-action" onClick={() => void undoDraftCreate(toast)}>
            {t("draft.undo")}
          </button>
        </>
      )}
      {toast.kind === "undone" && toast.refused.length > 0 && <KeptList nodes={toast.refused} />}
      <button
        type="button"
        className="copilot-toast-close"
        onClick={() => useDraftUi.getState().showToast(null)}
        aria-label={t("draft.dismiss")}
      >
        <IconClose size={12} />
      </button>
    </div>
  );
}
