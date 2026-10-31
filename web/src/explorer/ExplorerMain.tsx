import "./explorer.css";
import { useEffect } from "react";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import AgentView from "./AgentView";
import DiffView from "./DiffView";
import { useExplorer } from "./explorerStore";
import FileView from "./FileView";

/** The main area while the Explorer shows something: a file, a diff or an
 * agent, with "Back to board". */
export default function ExplorerMain() {
  const { t } = useI18n();
  const view = useExplorer((s) => s.view);
  const close = useExplorer((s) => s.close);

  // What was open belongs to the project it was opened in.
  useEffect(() => {
    return useApp.subscribe((state, previous) => {
      if (state.activeProject !== previous.activeProject) useExplorer.getState().close();
    });
  }, []);

  useEffect(() => {
    if (!view) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) close();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [view, close]);

  if (!view) {
    return (
      <section className="ex-main">
        <div className="ex-empty-state">
          <strong>{t("explorer.empty.title")}</strong>
          <span>{t("explorer.empty.hint")}</span>
        </div>
      </section>
    );
  }

  return (
    <section className="ex-main">
      <div className="ex-mbar">
        <button type="button" className="ex-btn" onClick={close}>
          <span aria-hidden="true">← </span>
          {t("explorer.close")}
        </button>
      </div>
      {view.kind === "file" && <FileView key={JSON.stringify(view)} view={view} />}
      {view.kind === "diff" && <DiffView key={JSON.stringify(view)} view={view} />}
      {view.kind === "agent" && <AgentView key={view.id} id={view.id} />}
    </section>
  );
}
