/** Canvas groups and sticky notes [B-06]: background decoration, not data. */

import { useState, useEffect, useRef } from "react";
import type { CanvasAnnotation, CanvasGroup } from "../../types";
import { ColorField } from "../InteractionPrimitives";
import { useI18n } from "../../i18n";
import type { Rect } from "./geometry";
import {
  NO_SNAP_TARGETS,
  SNAP_THRESHOLD_PX,
  buildSnapTargets,
  solveResizeSnap,
  solveSnap,
  type SnapBox,
  type SnapGuide,
  type SnapTargets,
} from "./snapping";

export function CanvasDecoration({
  kind,
  item,
  offsetX,
  offsetY,
  zoom,
  onChange,
  onDelete,
  snapEnabled = false,
  snapBoxes = [],
  onSnapGuides,
}: {
  kind: "group" | "annotation";
  item: CanvasGroup | CanvasAnnotation;
  offsetX: number;
  offsetY: number;
  zoom: number;
  onChange: (patch: Partial<CanvasGroup & CanvasAnnotation>) => void;
  onDelete: () => void;
  /** Aligns this box to the cards and the other decorations while it moves. */
  snapEnabled?: boolean;
  snapBoxes?: readonly SnapBox[];
  /**
   * Where to hand the lines being drawn. The board owns the overlay: this
   * element is positioned, so a line drawn inside it could not reach the boxes
   * it belongs to.
   */
  onSnapGuides?: (guides: SnapGuide[]) => void;
}) {
  const { t } = useI18n();
  const [working, setWorking] = useState(item);
  const targets = useRef<SnapTargets>(NO_SNAP_TARGETS);
  const gesture = useRef<{
    mode: "move" | "resize";
    pointerId: number;
    clientX: number;
    clientY: number;
    x: number;
    y: number;
    width: number;
    height: number;
  } | null>(null);
  useEffect(() => setWorking(item), [item]);

  const startGesture = (
    event: React.PointerEvent<HTMLElement>,
    mode: "move" | "resize",
  ) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    try {
      event.currentTarget.setPointerCapture?.(event.pointerId);
    } catch {
      // Pointer capture is unavailable in some test and embedded contexts.
    }
    targets.current = snapEnabled
      ? buildSnapTargets(snapBoxes, new Set([item.id]))
      : NO_SNAP_TARGETS;
    gesture.current = {
      mode,
      pointerId: event.pointerId,
      clientX: event.clientX,
      clientY: event.clientY,
      x: working.x,
      y: working.y,
      width: working.width,
      height: working.height,
    };
  };

  /**
   * Where the box ends up for a pointer delta, and the lines it landed on.
   * Both the live update and the commit go through this, so what is saved is
   * exactly what was on screen.
   */
  const resolveGesture = (
    current: NonNullable<typeof gesture.current>,
    dx: number,
    dy: number,
    suspended: boolean,
  ) => {
    const bounds: Rect = {
      left: current.x,
      right: current.x + current.width,
      top: current.y,
      bottom: current.y + current.height,
    };
    const snapping = snapEnabled && !suspended;
    if (current.mode === "move") {
      const pull = snapping
        ? solveSnap(bounds, dx, dy, targets.current, SNAP_THRESHOLD_PX / zoom)
        : { dx, dy, guides: [] };
      return {
        geometry: {
          x: Math.max(0, current.x + pull.dx),
          y: Math.max(0, current.y + pull.dy),
          width: current.width,
          height: current.height,
        },
        guides: pull.guides,
      };
    }
    // Resizing moves the right and bottom edges only: the corner stays put.
    const rawWidth = Math.max(140, current.width + dx);
    const rawHeight = Math.max(80, current.height + dy);
    const pull = snapping
      ? solveResizeSnap(
          { ...bounds, right: current.x + rawWidth, bottom: current.y + rawHeight },
          1,
          1,
          targets.current,
          SNAP_THRESHOLD_PX / zoom,
          "start",
        )
      : { dWidth: 0, dHeight: 0, guides: [] };
    return {
      geometry: {
        x: current.x,
        y: current.y,
        width: Math.max(140, rawWidth + pull.dWidth),
        height: Math.max(80, rawHeight + pull.dHeight),
      },
      guides: pull.guides,
    };
  };

  const moveGesture = (event: React.PointerEvent<HTMLElement>) => {
    const current = gesture.current;
    if (!current || current.pointerId !== event.pointerId) return;
    const dx = (event.clientX - current.clientX) / zoom;
    const dy = (event.clientY - current.clientY) / zoom;
    const { geometry, guides } = resolveGesture(current, dx, dy, event.shiftKey);
    setWorking((value) => ({ ...value, ...geometry }));
    onSnapGuides?.(guides);
  };
  const endGesture = (event: React.PointerEvent<HTMLElement>) => {
    const current = gesture.current;
    if (!current || current.pointerId !== event.pointerId) return;
    gesture.current = null;
    const dx = (event.clientX - current.clientX) / zoom;
    const dy = (event.clientY - current.clientY) / zoom;
    // Resolve before dropping the targets: what is saved has to be what was on
    // screen, alignment and all.
    const { geometry } = resolveGesture(current, dx, dy, event.shiftKey);
    targets.current = NO_SNAP_TARGETS;
    onSnapGuides?.([]);
    setWorking((value) => ({ ...value, ...geometry }));
    onChange({
      x: Math.round(geometry.x * 100) / 100,
      y: Math.round(geometry.y * 100) / 100,
      width: Math.round(geometry.width * 100) / 100,
      height: Math.round(geometry.height * 100) / 100,
    });
  };

  return (
    <section
      className={`canvas-decoration ${kind}`}
      style={{
        left: offsetX + working.x,
        top: offsetY + working.y,
        width: working.width,
        height: working.height,
        ["--decoration-color" as string]: working.color || "#31566a",
      }}
      onContextMenu={(event) => event.stopPropagation()}
    >
      <header
        onPointerDown={(event) => startGesture(event, "move")}
        onPointerMove={moveGesture}
        onPointerUp={endGesture}
        onPointerCancel={endGesture}
      >
        <span>{kind === "group" ? t("canvas.decoration.group") : t("canvas.decoration.annotation")}</span>
        <span
          className="canvas-decoration-color"
          onPointerDown={(event) => event.stopPropagation()}
        >
          <ColorField
            label={kind === "group" ? t("canvas.decoration.groupColor") : t("canvas.decoration.annotationColor")}
            title={kind === "group" ? t("canvas.decoration.groupColor") : t("canvas.decoration.annotationColor")}
            value={working.color || "#31566a"}
            // The decoration is drawn from `working`, so it can follow the
            // picker for free; only the write waits for the picker to close.
            onPreview={(color) => setWorking((value) => ({ ...value, color }))}
            onCommit={(color) => onChange({ color })}
          />
        </span>
        <button type="button" aria-label={t("common.delete")} onPointerDown={(event) => event.stopPropagation()} onClick={onDelete}>
          ×
        </button>
      </header>
      {kind === "group" ? (
        <input
          value={(working as CanvasGroup).title}
          onPointerDown={(event) => event.stopPropagation()}
          onChange={(event) =>
            setWorking((value) => ({ ...value, title: event.target.value }) as CanvasGroup)
          }
          onBlur={(event) => onChange({ title: event.target.value.trim() || t("canvas.decoration.untitledGroup") })}
          aria-label={t("canvas.decoration.groupName")}
        />
      ) : (
        <textarea
          value={(working as CanvasAnnotation).text}
          onPointerDown={(event) => event.stopPropagation()}
          onChange={(event) =>
            setWorking((value) => ({ ...value, text: event.target.value }) as CanvasAnnotation)
          }
          onBlur={(event) => onChange({ text: event.target.value })}
          aria-label={t("canvas.decoration.annotationText")}
        />
      )}
      <span
        className="canvas-decoration-resize"
        onPointerDown={(event) => startGesture(event, "resize")}
        onPointerMove={moveGesture}
        onPointerUp={endGesture}
        onPointerCancel={endGesture}
        aria-hidden
      />
    </section>
  );
}
