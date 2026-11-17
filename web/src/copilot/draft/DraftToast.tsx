/**
 * The line after a draft's Create: 「已建立 8 個節點 · 復原」 for ten seconds,
 * then what an undo did, naming the nodes it kept with "Delete anyway" for each.
 * Fixed above the copilot's button (and left of the open panel), so neither
 * covers the other.
 *
 * While it lists kept nodes it is the only place their buttons are with the
 * panel closed, so it stays until the person closes it or deletes the last
 * of them; and it never goes while the pointer or the focus is in it.
 */

import { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n";
import { IconClose } from "../../icons";
import { TOAST_MS, undoDraftCreate } from "./draftApi";
import { KeptList } from "./KeptList";
import { useDraftUi } from "./draftUi";

export function DraftToast() {
  const { t } = useI18n();
  const toast = useDraftUi((state) => state.toast);
  const close = useRef<HTMLButtonElement>(null);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  // Deleting a kept node rewrites the toast; its time runs from when it came.
  const shownAt = toast?.at;
  const keptCount = toast?.kind === "undone" ? toast.refused.length : 0;
  const held = hovered || focused || keptCount > 0;

  // Gone (closed, or replaced by nothing): the next toast starts unheld.
  const showing = toast !== null;
  useEffect(() => {
    if (showing) return;
    setHovered(false);
    setFocused(false);
  }, [showing]);

  useEffect(() => {
    if (shownAt === undefined || held) return;
    const timer = setTimeout(() => {
      if (useDraftUi.getState().toast?.at === shownAt) useDraftUi.getState().showToast(null);
    }, TOAST_MS);
    return () => clearTimeout(timer);
  }, [shownAt, held]);

  // The last kept node deleted takes the list and its focused button with
  // it: focus goes to the close button rather than the page.
  const keptBefore = useRef(keptCount);
  useEffect(() => {
    const before = keptBefore.current;
    keptBefore.current = keptCount;
    if (before === 0 || keptCount > 0) return;
    const active = document.activeElement;
    if (!active || active === document.body || !document.contains(active)) close.current?.focus();
  }, [keptCount]);

  if (!toast) return null;
  let text: string;
  if (toast.kind === "created") {
    text = toast.count === 1 ? t("draft.createdOne") : t("draft.created", { n: toast.count });
  } else if (toast.kind === "undone") {
    const kept = toast.refused.length;
    // Kept nodes deleted anyway since are removed too.
    const removed = toast.count + (toast.deleted ?? 0);
    if (kept > 0) {
      if (removed === 0) text = t("draft.undoKeptNone", { k: kept });
      else text = removed === 1 ? t("draft.undoKeptOne", { k: kept }) : t("draft.undoKept", { n: removed, k: kept });
    } else text = removed === 1 ? t("draft.undoneOne") : t("draft.undone", { n: removed });
  } else text = toast.text;

  return (
    <div
      className={`copilot-toast is-${toast.kind}`}
      role={toast.kind === "error" ? "alert" : "status"}
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false);
      }}
    >
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
        ref={close}
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
