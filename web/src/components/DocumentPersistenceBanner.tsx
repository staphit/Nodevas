export type DocumentPersistenceAction =
  | { type: "degraded"; id: string }
  | { type: "restored"; id: string }
  | { type: "reset" };

/** Turns only the two server durability events into local state changes. */
export function documentPersistenceAction(
  event: Readonly<{ type: string; id?: string }>,
): DocumentPersistenceAction | null {
  if (!event.id) return null;
  if (event.type === "doc-persistence-error") {
    return { type: "degraded", id: event.id };
  }
  if (event.type === "doc-persistence-restored") {
    return { type: "restored", id: event.id };
  }
  return null;
}

/** A set prevents repeated error frames from creating repeated banners. */
export function degradedDocumentsReducer(
  state: ReadonlySet<string>,
  action: DocumentPersistenceAction,
): ReadonlySet<string> {
  if (action.type === "reset") return state.size === 0 ? state : new Set();
  if (action.type === "degraded") {
    if (state.has(action.id)) return state;
    const next = new Set(state);
    next.add(action.id);
    return next;
  }
  if (!state.has(action.id)) return state;
  const next = new Set(state);
  next.delete(action.id);
  return next;
}

export function DocumentPersistenceBanner({
  documents,
}: {
  documents: ReadonlySet<string>;
}) {
  if (documents.size === 0) return null;
  return (
    <div className="document-persistence-alert" role="alert">
      <strong>協作內容尚未受到伺服器保護</strong>
      <span>
        伺服器目前無法保護未儲存的協作內容。請保持此分頁開啟，立即匯出或複製內容，並重試儲存。
      </span>
    </div>
  );
}
