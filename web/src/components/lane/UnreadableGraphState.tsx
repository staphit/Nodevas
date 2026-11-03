import { useLayoutEffect, useRef, useState, type CSSProperties, type RefObject } from "react";
import { useI18n } from "../../i18n";
import { reportError, useApp } from "../../store";
import { boardOverlayRects, insetsWithin, visibleArea, type Area } from "./boardVisibleArea";

/**
 * The hub's GET /api/graph refusal for a graph.yaml that does not parse or
 * validate (store/graph_unreadable.go): "graph.yaml: line 3 has an error. Fix
 * the file, then reload. (yaml: ...)", in the reader's language, sometimes
 * behind 'load project "x": ' (in zh-TW both colons are full-width:
 * "載入專案「x」：graph.yaml：第 3 行…"). Split into the hub's sentence, which already
 * carries the advice, and the parser's or validator's own words.
 */
const HUB_FRAME = /^([^(\uFF08]*[.\u3002]) ?[(\uFF08]([\s\S]*)[)\uFF09]$/;

export function splitUnreadableGraph(message: string): {
  sentence: string | null;
  detail: string;
} {
  const said = /(?:^|[:\uFF1A] ?)graph\.yaml[:\uFF1A] ?([\s\S]*)$/.exec(message)?.[1].trim() ?? message.trim();
  const framed = HUB_FRAME.exec(said);
  if (framed) return { sentence: framed[1].trim(), detail: framed[2].trim() };
  return { sentence: null, detail: said };
}

/**
 * Below this much visible board (in px at 100% text, scaled with the text)
 * the words get the card's whole width and Reload goes under them: beside
 * them it took a third of the ~360px a 580px drawer leaves at 1280 wide.
 */
const SIDE_BY_SIDE_MIN = 560;

export type NoticeLayout = "row" | "stacked";

export function noticeLayout(visibleWidth: number, fontScale: number): NoticeLayout {
  return visibleWidth < SIDE_BY_SIDE_MIN * fontScale ? "stacked" : "row";
}

/**
 * The least visible board (in px at 100% text, scaled with the text) the card
 * is laid out in: a short sentence and Reload under it. Less than that — a
 * phone's sheet leaves ~110px above it at 500×800, a drawer dragged wide a
 * strip — and the card goes full width across the foot of the board, over the
 * drawer, rather than under it or squeezed into what is left.
 */
const IN_BOARD_MIN_W = 220;
const IN_BOARD_MIN_H = 200;

export function noticeAboveDrawer(whole: Area, visible: Area, fontScale: number): boolean {
  const covered =
    visible.left !== whole.left ||
    visible.top !== whole.top ||
    visible.right !== whole.right ||
    visible.bottom !== whole.bottom;
  if (!covered) return false;
  return (
    visible.right - visible.left < IN_BOARD_MIN_W * fontScale ||
    visible.bottom - visible.top < IN_BOARD_MIN_H * fontScale
  );
}

function fontScale(root: HTMLElement): number {
  const scale = Number.parseFloat(root.style.getPropertyValue("--font-scale"));
  return Number.isFinite(scale) && scale > 0 ? scale : 1;
}

interface NoticeFrame {
  insets: Area;
  layout: NoticeLayout;
  aboveDrawer: boolean;
}

const NO_INSETS: Area = { left: 0, top: 0, right: 0, bottom: 0 };

/**
 * The part of the pane the notice can be read in: the pane less the node
 * drawer (a side panel or a phone's sheet) and any other board overlay
 * (boardVisibleArea). It follows the drawer coming, going and being dragged
 * wider, the window, and the text size.
 */
function useNoticeFrame(ref: RefObject<HTMLElement | null>): NoticeFrame {
  const [frame, setFrame] = useState<NoticeFrame>({ insets: NO_INSETS, layout: "row", aboveDrawer: false });
  useLayoutEffect(() => {
    const notice = ref.current;
    if (!notice) return;
    const doc = notice.ownerDocument;
    const root = doc.documentElement;
    const stage = notice.closest(".lane-wrap")?.parentElement ?? doc.body;
    // The pane the notice is laid over: over the drawer the notice is only a
    // band at its foot, so its own rect is not the board's.
    const pane = notice.parentElement ?? notice;
    const measure = () => {
      const rect = pane.getBoundingClientRect();
      const whole: Area = { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom };
      const visible = visibleArea(whole, boardOverlayRects(notice), { width: 0, height: 0 });
      const scale = fontScale(root);
      const aboveDrawer = noticeAboveDrawer(whole, visible, scale);
      const shown = aboveDrawer ? whole : visible;
      const insets = insetsWithin(whole, shown);
      const layout = noticeLayout(shown.right - shown.left, scale);
      setFrame((current) =>
        current.aboveDrawer === aboveDrawer &&
        current.layout === layout &&
        current.insets.left === insets.left &&
        current.insets.top === insets.top &&
        current.insets.right === insets.right &&
        current.insets.bottom === insets.bottom
          ? current
          : { insets, layout, aboveDrawer },
      );
    };
    const sizes = new ResizeObserver(measure);
    const watch = () => {
      sizes.disconnect();
      sizes.observe(pane);
      for (const overlay of stage.querySelectorAll<HTMLElement>(".drawer, [data-board-overlay]")) {
        sizes.observe(overlay);
      }
    };
    const children = new MutationObserver(() => {
      watch();
      measure();
    });
    children.observe(stage, { childList: true });
    const textSize = new MutationObserver(measure);
    textSize.observe(root, { attributes: true, attributeFilter: ["style"] });
    watch();
    measure();
    window.addEventListener("resize", measure);
    return () => {
      children.disconnect();
      textSize.disconnect();
      sizes.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [ref]);
  return frame;
}

/**
 * In place of the board while graph.yaml cannot be read: the reason, how to
 * get out of it, and no way to create a node — a write would go over the
 * file the reader has to fix. It goes away by itself on the next load that
 * reads (the watcher's graph-changed), or from its own button. It lies over
 * the board rather than replacing it, so the board keeps its scroll, and is
 * laid out in the part of it that shows beside an open drawer — or, where
 * that is too small to hold it, across the foot of the board over the drawer.
 */
export function UnreadableGraphState({ message }: { message: string }) {
  const { t } = useI18n();
  const loadAll = useApp((s) => s.loadAll);
  const [busy, setBusy] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const { insets, layout, aboveDrawer } = useNoticeFrame(ref);
  const { sentence, detail } = splitUnreadableGraph(message);
  const reload = () => {
    setBusy(true);
    void loadAll()
      .catch(reportError)
      .finally(() => setBusy(false));
  };
  return (
    <div
      ref={ref}
      className="board-unreadable"
      data-layout={layout}
      data-above-drawer={aboveDrawer ? "" : undefined}
      style={
        {
          "--notice-left": `${insets.left}px`,
          "--notice-top": `${insets.top}px`,
          "--notice-right": `${insets.right}px`,
          "--notice-bottom": `${insets.bottom}px`,
        } as CSSProperties
      }
    >
      <section className="ui-empty-state board-unreadable-state" role="alert">
        <span className="ui-empty-state-mark" aria-hidden="true" />
        <div>
          <b>{t("lane.unreadableTitle")}</b>
          <p>{sentence ?? t("lane.unreadableAdvice")}</p>
          {detail && <code className="board-unreadable-detail">{detail}</code>}
        </div>
        <div className="ui-empty-state-action">
          <button type="button" onClick={reload} disabled={busy}>
            {t("lane.unreadableReload")}
          </button>
        </div>
      </section>
    </div>
  );
}
