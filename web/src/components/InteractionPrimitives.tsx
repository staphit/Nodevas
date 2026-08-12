import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react";
import { useI18n } from "../i18n";

export type OperationStatusValue =
  | "idle"
  | "pending"
  | "saved"
  | "error"
  | "conflict";

interface ResizeHandleProps {
  orientation: "horizontal" | "vertical";
  value: number;
  min: number;
  max: number;
  label: string;
  className?: string;
  title?: string;
  step?: number;
  largeStep?: number;
  direction?: 1 | -1;
  scale?: number;
  disabled?: boolean;
  style?: CSSProperties;
  valueText?: (value: number) => string;
  onChange: (value: number) => void;
  onCommit?: (value: number) => void;
  onReset?: () => void;
  onResizeStateChange?: (resizing: boolean) => void;
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

// Pointer capture keeps a drag alive outside the element, but it is an
// enhancement: environments without it (jsdom, older engines) must still drag.
function capturePointer(element: Element, pointerId: number): void {
  try {
    element.setPointerCapture?.(pointerId);
  } catch {
    /* capture unavailable: pointer events still arrive while over the handle */
  }
}

function releaseCapture(element: Element, pointerId: number): void {
  try {
    if (element.hasPointerCapture?.(pointerId)) {
      element.releasePointerCapture?.(pointerId);
    }
  } catch {
    /* nothing to release */
  }
}

export function ResizeHandle({
  orientation,
  value,
  min,
  max,
  label,
  className = "",
  title,
  step = 8,
  largeStep = 24,
  direction = 1,
  scale = 1,
  disabled = false,
  style,
  valueText,
  onChange,
  onCommit,
  onReset,
  onResizeStateChange,
}: ResizeHandleProps) {
  const { t } = useI18n();
  const dragRef = useRef<{
    pointerId: number;
    coordinate: number;
    value: number;
    latest: number;
  } | null>(null);

  const coordinateOf = (event: ReactPointerEvent<HTMLSpanElement>) =>
    orientation === "vertical" ? event.clientX : event.clientY;

  const valueFromPointer = (event: ReactPointerEvent<HTMLSpanElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return null;
    const delta = ((coordinateOf(event) - drag.coordinate) * direction) / Math.max(scale, 0.01);
    return clamp(Math.round(drag.value + delta), min, max);
  };

  const finishResize = (event: ReactPointerEvent<HTMLSpanElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const next = valueFromPointer(event) ?? drag.latest;
    dragRef.current = null;
    delete event.currentTarget.dataset.resizing;
    releaseCapture(event.currentTarget, event.pointerId);
    onResizeStateChange?.(false);
    onCommit?.(next);
  };

  const applyKeyboardValue = (next: number) => {
    const clamped = clamp(next, min, max);
    onChange(clamped);
    onCommit?.(clamped);
  };

  return (
    <span
      className={`ui-resize-handle ${className}`.trim()}
      role="separator"
      aria-orientation={orientation}
      aria-label={label}
      aria-valuemin={min}
      aria-valuemax={max}
      aria-valuenow={Math.round(value)}
      aria-valuetext={valueText?.(value)}
      aria-disabled={disabled || undefined}
      tabIndex={disabled ? -1 : 0}
      title={title ?? t("common.resizeTitle", { label })}
      style={style}
      onPointerDown={(event) => {
        if (disabled || (event.pointerType === "mouse" && event.button !== 0)) return;
        event.preventDefault();
        event.stopPropagation();
        capturePointer(event.currentTarget, event.pointerId);
        event.currentTarget.dataset.resizing = "true";
        dragRef.current = {
          pointerId: event.pointerId,
          coordinate: coordinateOf(event),
          value,
          latest: value,
        };
        onResizeStateChange?.(true);
      }}
      onPointerMove={(event) => {
        const next = valueFromPointer(event);
        if (next === null) return;
        event.preventDefault();
        dragRef.current!.latest = next;
        onChange(next);
      }}
      onPointerUp={finishResize}
      onPointerCancel={finishResize}
      onLostPointerCapture={(event) => {
        const drag = dragRef.current;
        if (!drag || drag.pointerId !== event.pointerId) return;
        dragRef.current = null;
        delete event.currentTarget.dataset.resizing;
        onResizeStateChange?.(false);
        onCommit?.(drag.latest);
      }}
      onDoubleClick={(event) => {
        if (disabled || !onReset) return;
        event.preventDefault();
        event.stopPropagation();
        onReset();
      }}
      onKeyDown={(event) => {
        if (disabled) return;
        const amount = event.shiftKey ? largeStep : step;
        let next: number | null = null;
        if (orientation === "vertical") {
          if (event.key === "ArrowLeft") next = value - amount * direction;
          if (event.key === "ArrowRight") next = value + amount * direction;
        } else {
          if (event.key === "ArrowUp") next = value - amount * direction;
          if (event.key === "ArrowDown") next = value + amount * direction;
        }
        if (event.key === "Home") next = min;
        if (event.key === "End") next = max;
        if (next === null) return;
        event.preventDefault();
        event.stopPropagation();
        applyKeyboardValue(next);
      }}
    >
      <span className="ui-resize-grip" aria-hidden="true" />
    </span>
  );
}

/**
 * A colour input that saves once, when the picker is finished with.
 *
 * React's `onChange` for `<input type="color">` is the native `input` event,
 * which fires continuously while the pointer moves around the picker. Wiring a
 * save to it turns choosing one colour into a hundred writes — and on a slow
 * link, a hundred chances to conflict. The draft is held here and committed on
 * the native `change` event, which fires when the picker closes, with blur as
 * the backstop for a browser that forgets.
 *
 * `onPreview` is for a caller that can show the colour without saving it. A
 * caller without one simply sees the colour arrive when the picker closes.
 */
export function ColorField({
  value,
  onCommit,
  onPreview,
  disabled,
  className,
  title,
  label,
}: {
  value: string;
  onCommit: (value: string) => void;
  onPreview?: (value: string) => void;
  disabled?: boolean;
  className?: string;
  title?: string;
  label: string;
}) {
  const ref = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(value);
  const committed = useRef(value);

  // A colour that changed elsewhere — undo, another editor, a reset — wins,
  // but only while this field is not the one being edited: adopting it
  // mid-pick would yank the swatch out from under the pointer.
  useEffect(() => {
    if (ref.current && ref.current.ownerDocument.activeElement === ref.current) return;
    committed.current = value;
    setDraft(value);
  }, [value]);

  const commit = useCallback(() => {
    const next = ref.current?.value;
    if (next == null || next === committed.current) return;
    committed.current = next;
    onCommit(next);
  }, [onCommit]);

  useEffect(() => {
    const element = ref.current;
    if (!element) return;
    element.addEventListener("change", commit);
    return () => element.removeEventListener("change", commit);
  }, [commit]);

  return (
    <input
      ref={ref}
      type="color"
      value={draft}
      disabled={disabled}
      className={className}
      title={title}
      aria-label={label}
      onChange={(event) => {
        const next = event.target.value;
        setDraft(next);
        onPreview?.(next);
      }}
      onBlur={commit}
    />
  );
}

export function OperationStatus({
  status,
  message,
}: {
  status: OperationStatusValue;
  message?: string;
}) {
  const { t } = useI18n();
  if (status === "idle") return null;
  const labels: Record<Exclude<OperationStatusValue, "idle">, string> = {
    pending: t("operation.pending"),
    saved: t("operation.saved"),
    error: t("operation.error"),
    conflict: t("operation.conflict"),
  };
  return (
    // A conflict needs a decision, so it is announced like an error, not as
    // passive progress.
    <span
      className={`operation-status ${status}`}
      role={status === "error" || status === "conflict" ? "alert" : "status"}
    >
      <span className="operation-status-dot" aria-hidden="true" />
      {message || labels[status]}
    </span>
  );
}

export function InlineNotice({
  kind,
  children,
}: {
  kind: "success" | "error" | "warning" | "info";
  children: ReactNode;
}) {
  return (
    <span className={`inline-notice ${kind}`} role={kind === "error" ? "alert" : "status"}>
      {children}
    </span>
  );
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <section className="ui-empty-state">
      <span className="ui-empty-state-mark" aria-hidden="true" />
      <div>
        <b>{title}</b>
        {description && <p>{description}</p>}
      </div>
      {action && <div className="ui-empty-state-action">{action}</div>}
    </section>
  );
}
