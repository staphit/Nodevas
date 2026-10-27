import { createContext, useContext, type ReactNode } from "react";

/**
 * Where a settings dialog draws itself: over the page (the default), or in
 * the Settings view, inline. The Settings view sets this; the dialogs do not
 * know which one they are in beyond hiding their own close buttons there.
 */
export const InlineDialogContext = createContext(false);

export function useInlineDialog(): boolean {
  return useContext(InlineDialogContext);
}

/**
 * A settings dialog's frame: a modal over a backdrop, or, in the Settings
 * view, a labelled pane in the page.
 */
export function DialogFrame({
  className,
  label,
  onDismiss,
  children,
}: {
  className: string;
  label: string;
  /** Clicking the backdrop; never called inline. */
  onDismiss: () => void;
  children: ReactNode;
}) {
  const inline = useInlineDialog();
  if (inline) {
    return (
      <section className={`settings-page-pane confirm-dialog ${className}`} aria-label={label}>
        {children}
      </section>
    );
  }
  return (
    <div className="confirm-backdrop" onClick={onDismiss}>
      <div
        className={`confirm-dialog ${className}`}
        role="dialog"
        aria-modal="true"
        aria-label={label}
        onClick={(event) => event.stopPropagation()}
      >
        {children}
      </div>
    </div>
  );
}
