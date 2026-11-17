/**
 * The line after a draft's Create: 「已建立 8 個節點 · 復原」 for ten seconds,
 * then what an undo did. Fixed above the copilot's button (and left of the
 * open panel), so neither covers the other.
 */

import { useEffect } from "react";
import { useI18n } from "../../i18n";
import { IconClose } from "../../icons";
import { TOAST_MS, undoDraftCreate } from "./draftApi";
import { useDraftUi } from "./draftUi";

export function DraftToast() {
  const { t } = useI18n();
  const toast = useDraftUi((state) => state.toast);

  useEffect(() => {
    if (!toast) return;
    const shown = toast;
    const timer = setTimeout(() => {
      if (useDraftUi.getState().toast === shown) useDraftUi.getState().showToast(null);
    }, TOAST_MS);
    return () => clearTimeout(timer);
  }, [toast]);

  if (!toast) return null;
  let text: string;
  if (toast.kind === "created") text = t("draft.created", { n: toast.count });
  else if (toast.kind === "undone") {
    text = toast.refused > 0 ? t("draft.undoRefused", { n: toast.refused }) : t("draft.undone", { n: toast.count });
  } else text = toast.text;

  return (
    <div className={`copilot-toast is-${toast.kind}`} role={toast.kind === "error" ? "alert" : "status"}>
      <span className="copilot-toast-text">{text}</span>
      {toast.kind === "created" && toast.undoable && (
        <>
          <span aria-hidden className="copilot-toast-sep">
            ·
          </span>
          <button
            type="button"
            className="copilot-toast-action"
            onClick={() => void undoDraftCreate(toast.entryId)}
          >
            {t("draft.undo")}
          </button>
        </>
      )}
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
