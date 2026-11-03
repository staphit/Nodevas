import { useId, type ReactNode } from "react";

/** "a · b · c +2": a few names for a row's value, "" for none. */
export function listValue(items: readonly string[], max = 3): string {
  const shown = items.filter(Boolean);
  if (shown.length === 0) return "";
  const head = shown.slice(0, max).join(" · ");
  return shown.length > max ? `${head} +${shown.length - max}` : head;
}

/**
 * A row of a Settings section: its name and current value, opening in place
 * to the part that edits it. Opened rows are regions named after the row, so
 * a reader (and a test) finds the part by the name it was asked for by.
 */
export function SettingsDisclosure({
  label,
  value,
  open,
  onToggle,
  children,
}: {
  label: string;
  /** The current value, short; "" draws none. */
  value?: string;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  const bodyId = useId();
  return (
    <div className={`settings-disclosure${open ? " is-open" : ""}`}>
      <button
        type="button"
        className="settings-disclosure-toggle"
        aria-expanded={open}
        aria-controls={open ? bodyId : undefined}
        onClick={onToggle}
      >
        <span className="settings-disclosure-label">{label}</span>
        {/* Cut with an ellipsis where the row is narrow; whole on hover. */}
        {value && (
          <span className="settings-disclosure-value" title={value}>
            {value}
          </span>
        )}
        <span className="settings-disclosure-chevron" aria-hidden="true">
          ›
        </span>
      </button>
      {open && (
        <div id={bodyId} className="settings-disclosure-body" role="region" aria-label={label}>
          {children}
        </div>
      )}
    </div>
  );
}
