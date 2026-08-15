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
  const { t } = useI18n();
  if (documents.size === 0) return null;
  return (
    <div className="document-persistence-alert" role="alert">
      <strong>{t("persistence.title")}</strong>
      <span>
        {t("persistence.description")}
      </span>
    </div>
  );
}
import { useI18n } from "../i18n";
