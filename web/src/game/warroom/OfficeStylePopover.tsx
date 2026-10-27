// The office style picker, from the current office's door sign: the same
// picker as Settings › This project, in a small non-modal dialog under the
// sign. Escape or a click outside closes it, and focus goes back to the
// sign. There is no role check here: the hub refuses an agent, and what it
// says shows in the dialog.

import { useEffect, useMemo, useRef, useState, type RefObject } from "react";

import { OfficeStylePicker } from "../../components/settings/OfficeStylePicker";
import type { SettingsNotify } from "../../components/settings/notify";

export function OfficeStylePopover({
  label,
  onClose,
  anchor,
}: {
  label: string;
  /** returnFocus: closed from the keyboard (Escape), not by a click away. */
  onClose: (returnFocus: boolean) => void;
  /** The sign that opened it: a click on it toggles, not "outside". */
  anchor: RefObject<HTMLElement | null>;
}) {
  const panel = useRef<HTMLDivElement | null>(null);
  const [error, setError] = useState<string | null>(null);
  const notify: SettingsNotify = useMemo(() => ({ onError: setError, onNotice: () => undefined }), []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      onClose(true);
    };
    const onDown = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (panel.current?.contains(target) || anchor.current?.contains(target)) return;
      onClose(false);
    };
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("pointerdown", onDown, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("pointerdown", onDown, true);
    };
  }, [onClose, anchor]);

  return (
    <div ref={panel} className="px-style-popover" role="dialog" aria-label={label}>
      <OfficeStylePicker notify={notify} autoFocus />
      {error && (
        <p className="px-style-popover-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
