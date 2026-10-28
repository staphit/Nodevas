/**
 * Where each project's graph was last looked at, for the rest of the session.
 *
 * The board unmounts whenever the app shows Station or Settings in its place,
 * and a fresh mount used to open at the pan buffer at 100% — so a round trip
 * to another view threw away the pan, the zoom and whatever a Ctrl+K jump had
 * centred. Kept in module state, not in the project file or preferences: like
 * the zoom itself it is a way of looking at the board, not a fact about it,
 * and a reload still opens on the opening scroll.
 */

export interface BoardView {
  /** The user's own zoom, as the toolbar shows it. */
  zoom: number;
  /** What the board was drawn at when the scroll was read (zoom × node scale). */
  scale: number;
  left: number;
  top: number;
}

const views = new Map<string, BoardView>();

export function boardViewKey(project: string | null | undefined): string {
  return project || "__default__";
}

export function recallBoardView(key: string): BoardView | undefined {
  return views.get(key);
}

export function rememberBoardView(key: string, view: BoardView) {
  views.set(key, view);
}

/** Tests only: every test starts from a board nobody has looked at. */
export function forgetBoardViews() {
  views.clear();
}

/**
 * The scroll that shows the same middle of the board as `view` did, at the
 * scale the board is drawn at now. Equal scales come back exactly.
 */
export function restoredScroll(
  view: BoardView,
  scale: number,
  viewport: { width: number; height: number },
): { left: number; top: number } {
  if (view.scale === scale || view.scale <= 0) {
    return { left: view.left, top: view.top };
  }
  const ratio = scale / view.scale;
  const halfW = viewport.width / 2;
  const halfH = viewport.height / 2;
  return {
    left: (view.left + halfW) * ratio - halfW,
    top: (view.top + halfH) * ratio - halfH,
  };
}
