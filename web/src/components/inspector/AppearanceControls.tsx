/**
 * Card appearance controls, without a node.
 *
 * The presentational half of `NodeAppearance`: it is handed a style and a
 * callback and knows nothing about the store, so the same controls serve both
 * the inspector (editing a node that exists) and the creation form (choosing
 * how a node will look before it does). Extracted rather than copied — two
 * copies of six colour palettes and six shapes would disagree with each other
 * within a month.
 */

import type { CSSProperties } from "react";
import { ColorField } from "../InteractionPrimitives";
import { StatusShape, statusTheme } from "../../statusTheme";
import type {
  NodeAlign,
  NodeShape,
  NodeStyle,
  NodeVAlign,
  Status,
  StatusDefinition,
} from "../../types";

const SHAPES: { value: NodeShape; label: string }[] = [
  { value: "rect", label: "矩形" },
  { value: "round", label: "圓角" },
  { value: "pill", label: "膠囊" },
  { value: "ellipse", label: "橢圓" },
  { value: "diamond", label: "菱形" },
  { value: "hexagon", label: "六角形" },
];

const PRESETS: { value: string; label: string; width: number; height: number }[] = [
  { value: "compact", label: "精簡", width: 152, height: 56 },
  { value: "default", label: "標準", width: 152, height: 68 },
  { value: "wide", label: "寬版", width: 240, height: 68 },
  { value: "large", label: "大型", width: 240, height: 112 },
  { value: "square", label: "正方", width: 140, height: 140 },
];

const ALIGNMENTS: { value: NodeAlign; label: string; glyph: string }[] = [
  { value: "left", label: "靠左", glyph: "⬅" },
  { value: "center", label: "水平置中", glyph: "↔" },
  { value: "right", label: "靠右", glyph: "➡" },
];

const VALIGNMENTS: { value: NodeVAlign; label: string; glyph: string }[] = [
  { value: "top", label: "靠上", glyph: "⬆" },
  { value: "middle", label: "垂直置中", glyph: "↕" },
  { value: "bottom", label: "靠下", glyph: "⬇" },
];

/** Outline palette: readable against both themes' card fills. */
const BORDER_SWATCHES = [
  "#3a4a55",
  "#7dd3a7",
  "#8fd3ff",
  "#ffd479",
  "#ff9f9f",
  "#c9a7ff",
  "#e6edf3",
  "#0b0e11",
];

/** Card palette: enough choice to group nodes, few enough to stay coherent. */
const SWATCHES = [
  "#1b2429",
  "#243b46",
  "#2b3a2a",
  "#3d3320",
  "#3b2530",
  "#2f2a44",
  "#e6edf3",
  "#0b0e11",
];

const TEXT_SWATCHES = ["#e6edf3", "#0b0e11", "#8fd3ff", "#ffd479", "#ff9f9f", "#a6e3a1"];

const MIN_W = 120;
const MAX_W = 360;
const MIN_H = 52;
const MAX_H = 180;
export const DEFAULT_NODE_W = 152;
export const DEFAULT_NODE_H = 68;

/** Every key the "reset" button clears, and the whole of `NodeStyle`. */
export const EMPTY_NODE_STYLE: { [K in keyof Required<NodeStyle>]: undefined } = {
  width: undefined,
  height: undefined,
  shape: undefined,
  color: undefined,
  textColor: undefined,
  borderColor: undefined,
  align: undefined,
  valign: undefined,
};

/**
 * Applies a patch the way the store's `node.setStyle` command does: a key set
 * to `undefined` means "go back to the default", so it is removed rather than
 * stored as an explicit undefined that would serialise into graph.yaml.
 */
export function mergeNodeStyle(style: NodeStyle, patch: Partial<NodeStyle>): NodeStyle {
  const next: NodeStyle = { ...style };
  for (const [key, value] of Object.entries(patch)) {
    if (value === undefined) delete next[key as keyof NodeStyle];
    else Object.assign(next, { [key]: value });
  }
  return next;
}

export interface AppearanceControlsProps {
  /** Disambiguates the `aria-labelledby` ids when two copies are on screen. */
  idPrefix: string;
  style: NodeStyle;
  onChange: (patch: Partial<NodeStyle>) => void;
  /** Drawn in the preview card, so the choice is judged in context. */
  status: Status;
  customStatuses?: StatusDefinition[];
  previewTitle: string;
  /** Second line of the preview card; the inspector shows a placeholder. */
  previewAssignee?: string;
  disabled?: boolean;
  /** Hint under the size sliders. The canvas handles only exist for real nodes. */
  sizeHint?: string;
}

export function AppearanceControls({
  idPrefix,
  style,
  onChange,
  status,
  customStatuses = [],
  previewTitle,
  previewAssignee = "預覽",
  disabled = false,
  sizeHint,
}: AppearanceControlsProps) {
  const shape = style.shape ?? "rect";
  const width = style.width ?? DEFAULT_NODE_W;
  const height = style.height ?? DEFAULT_NODE_H;

  return (
    <>
      {/* What the canvas will draw, at the size being chosen. */}
      <div className="appearance-preview">
        <div
          className={`col-card status-${status} shape-${shape}${
            style.align ? ` align-${style.align}` : ""
          }${style.valign ? ` valign-${style.valign}` : ""}`}
          style={
            {
              position: "relative",
              width,
              height,
              "--card-w": `${width}px`,
              "--card-h": `${height}px`,
              "--card-bg": style.color,
              "--card-border-color": style.borderColor,
              borderColor: style.borderColor,
              color: style.textColor,
            } as CSSProperties
          }
        >
          <span className="col-card-head">
            <StatusShape status={status} definitions={customStatuses} />
            <span
              className="col-card-st"
              style={{ color: statusTheme(status, customStatuses).color }}
            >
              {statusTheme(status, customStatuses).label}
            </span>
          </span>
          <span className="col-card-title">{previewTitle}</span>
          <span className="col-card-assignee unassigned">{previewAssignee}</span>
        </div>
      </div>

      <section className="appearance-group" aria-labelledby={`shape-${idPrefix}`}>
        <h4 id={`shape-${idPrefix}`}>形狀</h4>
        <div className="appearance-choices">
          {SHAPES.map((option) => (
            <button
              key={option.value}
              type="button"
              className={`appearance-shape${shape === option.value ? " on" : ""}`}
              aria-pressed={shape === option.value}
              disabled={disabled}
              onClick={() =>
                onChange({ shape: option.value === "rect" ? undefined : option.value })
              }
            >
              <span
                className={`appearance-shape-mark shape-${option.value}`}
                aria-hidden
              />
              {option.label}
            </button>
          ))}
        </div>
      </section>

      <section className="appearance-group" aria-labelledby={`size-${idPrefix}`}>
        <h4 id={`size-${idPrefix}`}>尺寸</h4>
        <label className="appearance-slider">
          <span>寬度</span>
          <input
            type="range"
            min={MIN_W}
            max={MAX_W}
            step={4}
            value={width}
            aria-label="寬度"
            disabled={disabled}
            onChange={(event) => onChange({ width: Number(event.target.value) })}
          />
          <input
            type="number"
            min={MIN_W}
            max={MAX_W}
            value={width}
            aria-label="寬度數值"
            disabled={disabled}
            onChange={(event) => onChange({ width: Number(event.target.value) })}
          />
        </label>
        <label className="appearance-slider">
          <span>高度</span>
          <input
            type="range"
            min={MIN_H}
            max={MAX_H}
            step={4}
            value={height}
            aria-label="高度"
            disabled={disabled}
            onChange={(event) => onChange({ height: Number(event.target.value) })}
          />
          <input
            type="number"
            min={MIN_H}
            max={MAX_H}
            value={height}
            aria-label="高度數值"
            disabled={disabled}
            onChange={(event) => onChange({ height: Number(event.target.value) })}
          />
        </label>
        <div className="appearance-choices">
          {PRESETS.map((preset) => (
            <button
              key={preset.value}
              type="button"
              className={`appearance-preset${
                width === preset.width && height === preset.height ? " on" : ""
              }`}
              disabled={disabled}
              onClick={() => onChange({ width: preset.width, height: preset.height })}
            >
              {preset.label}
              <small>
                {preset.width}×{preset.height}
              </small>
            </button>
          ))}
        </div>
        {sizeHint && <p className="appearance-hint">{sizeHint}</p>}
      </section>

      <section className="appearance-group" aria-labelledby={`align-${idPrefix}`}>
        <h4 id={`align-${idPrefix}`}>文字位置</h4>
        <div className="appearance-choices" role="group" aria-label="文字水平位置">
          {ALIGNMENTS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={`appearance-align${style.align === option.value ? " on" : ""}`}
              aria-pressed={style.align === option.value}
              aria-label={option.label}
              disabled={disabled}
              onClick={() =>
                onChange({ align: style.align === option.value ? undefined : option.value })
              }
            >
              <span aria-hidden>{option.glyph}</span>
              {option.label}
            </button>
          ))}
        </div>
        <div className="appearance-choices" role="group" aria-label="文字垂直位置">
          {VALIGNMENTS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={`appearance-align${style.valign === option.value ? " on" : ""}`}
              aria-pressed={style.valign === option.value}
              aria-label={option.label}
              disabled={disabled}
              onClick={() =>
                onChange({
                  valign: style.valign === option.value ? undefined : option.value,
                })
              }
            >
              <span aria-hidden>{option.glyph}</span>
              {option.label}
            </button>
          ))}
        </div>
        <p className="appearance-hint">
          未選擇時，文字依形狀擺放：窄形狀置中，矩形靠左。
        </p>
      </section>

      <section className="appearance-group" aria-labelledby={`colour-${idPrefix}`}>
        <h4 id={`colour-${idPrefix}`}>顏色</h4>
        <div className="appearance-colour-row">
          <span>底色</span>
          <div className="appearance-swatches">
            {SWATCHES.map((swatch) => (
              <button
                key={swatch}
                type="button"
                className={`appearance-swatch${style.color === swatch ? " on" : ""}`}
                style={{ background: swatch }}
                aria-label={`底色 ${swatch}`}
                disabled={disabled}
                onClick={() => onChange({ color: swatch })}
              />
            ))}
          </div>
          <ColorField
            value={style.color || "#1b2429"}
            label="自訂底色"
            disabled={disabled}
            onCommit={(color) => onChange({ color })}
          />
        </div>
        <div className="appearance-colour-row">
          <span>外框</span>
          <div className="appearance-swatches">
            {BORDER_SWATCHES.map((swatch) => (
              <button
                key={swatch}
                type="button"
                className={`appearance-swatch${
                  style.borderColor === swatch ? " on" : ""
                }`}
                style={{ background: swatch }}
                aria-label={`外框顏色 ${swatch}`}
                disabled={disabled}
                onClick={() => onChange({ borderColor: swatch })}
              />
            ))}
          </div>
          <ColorField
            value={style.borderColor || "#3a4a55"}
            label="自訂外框顏色"
            disabled={disabled}
            onCommit={(borderColor) => onChange({ borderColor })}
          />
          <button
            type="button"
            className="appearance-clear"
            disabled={disabled || !style.borderColor}
            onClick={() => onChange({ borderColor: undefined })}
          >
            清除
          </button>
        </div>
        <div className="appearance-colour-row">
          <span>文字</span>
          <div className="appearance-swatches">
            {TEXT_SWATCHES.map((swatch) => (
              <button
                key={swatch}
                type="button"
                className={`appearance-swatch${style.textColor === swatch ? " on" : ""}`}
                style={{ background: swatch }}
                aria-label={`文字顏色 ${swatch}`}
                disabled={disabled}
                onClick={() => onChange({ textColor: swatch })}
              />
            ))}
          </div>
          <ColorField
            value={style.textColor || "#e6edf3"}
            label="自訂文字顏色"
            disabled={disabled}
            onCommit={(textColor) => onChange({ textColor })}
          />
        </div>
      </section>

      <div className="appearance-footer">
        <button
          type="button"
          disabled={disabled}
          onClick={() => onChange({ ...EMPTY_NODE_STYLE })}
        >
          重設為預設外觀
        </button>
      </div>
    </>
  );
}
