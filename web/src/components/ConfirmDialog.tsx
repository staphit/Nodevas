import { useEffect, useRef, useState, type ReactNode } from "react";
import { IconClose, IconWarning } from "../icons";
import { useI18n } from "../i18n";

export interface ConfirmOptions {
  title: string;
  description: string;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: "default" | "danger";
  /** Replaces the warning glyph, for confirmations that are not warnings. */
  icon?: ReactNode;
  /** A notice about something already done: one button, nothing to cancel. */
  notice?: boolean;
}

export type NoticeOptions = Pick<ConfirmOptions, "title" | "description" | "icon"> & {
  /** The one button; "OK" when left out. */
  closeLabel?: string;
};

type ConfirmRequest = ConfirmOptions & {
  resolve: (value: boolean) => void;
};

let showConfirm: ((request: ConfirmRequest) => void) | null = null;

export function confirmAction(options: ConfirmOptions): Promise<boolean> {
  if (!showConfirm) return Promise.resolve(false);
  return new Promise((resolve) => showConfirm?.({ ...options, resolve }));
}

/**
 * Tells the user something that has already happened. There is no choice to
 * make, so there is one button (and Escape, and the corner close), never a
 * Cancel beside a Confirm.
 */
export async function showNotice({ closeLabel, ...options }: NoticeOptions): Promise<void> {
  await confirmAction({ ...options, notice: true, confirmLabel: closeLabel });
}

export function ConfirmDialogHost() {
  const { t } = useI18n();
  const [request, setRequest] = useState<ConfirmRequest | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    showConfirm = setRequest;
    return () => {
      showConfirm = null;
    };
  }, []);

  useEffect(() => {
    if (!request) return;
    previousFocusRef.current = document.activeElement as HTMLElement | null;
    confirmRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        request.resolve(false);
        setRequest(null);
        return;
      }
      if (event.key === "Tab") {
        const focusable = Array.from(
          dialogRef.current?.querySelectorAll<HTMLElement>("button:not(:disabled)") ?? [],
        );
        if (focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      previousFocusRef.current?.focus();
    };
  }, [request]);

  if (!request) return null;

  const close = (confirmed: boolean) => {
    request.resolve(confirmed);
    setRequest(null);
  };

  return (
    <div
      className="confirm-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) close(false);
      }}
    >
      <section
        ref={dialogRef}
        className={`confirm-dialog ${request.tone === "danger" ? "danger" : ""}`}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-description"
      >
        <header>
          <span className="confirm-dialog-icon">
            {request.icon ?? <IconWarning size={17} />}
          </span>
          <div>
            <h2 id="confirm-title">{request.title}</h2>
            <p id="confirm-description">{request.description}</p>
          </div>
          <button
            type="button"
            className="confirm-dialog-close"
            onClick={() => close(false)}
            aria-label={t("common.close")}
          >
            <IconClose size={15} />
          </button>
        </header>
        <footer>
          {!request.notice && (
            <button type="button" onClick={() => close(false)}>
              {request.cancelLabel ?? t("common.cancel")}
            </button>
          )}
          <button
            ref={confirmRef}
            type="button"
            className={request.tone === "danger" ? "danger" : "primary"}
            onClick={() => close(true)}
          >
            {request.confirmLabel ?? t(request.notice ? "common.ok" : "common.confirm")}
          </button>
        </footer>
      </section>
    </div>
  );
}
