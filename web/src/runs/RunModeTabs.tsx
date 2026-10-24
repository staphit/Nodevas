import { lazy, Suspense, useState, type ReactNode } from "react";
import { runInteractive, type Run } from "../api";
import { useI18n } from "../i18n";

// A dynamic import keeps xterm out of the main bundle: only an interactive
// run's view ever loads it.
const RunTerminal = lazy(() => import("./RunTerminal"));

type Tab = "terminal" | "activity";

/**
 * The run view's body. A headless run shows its activity timeline (children)
 * as before; an interactive run adds its live terminal, shown first. The
 * terminal stays mounted once opened so switching tabs does not reattach.
 */
export function RunModeTabs({ run, children }: { run: Run; children: ReactNode }) {
  const { t } = useI18n();
  const [tab, setTab] = useState<Tab>("terminal");
  if (!runInteractive(run)) return <>{children}</>;

  const tabId = (name: Tab) => `run-${run.id}-tab-${name}`;
  const panelId = (name: Tab) => `run-${run.id}-panel-${name}`;
  const tabButton = (name: Tab, label: string) => (
    <button
      type="button"
      role="tab"
      id={tabId(name)}
      aria-selected={tab === name}
      aria-controls={panelId(name)}
      tabIndex={tab === name ? 0 : -1}
      className={tab === name ? "active" : undefined}
      onClick={() => setTab(name)}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
          event.preventDefault();
          const next: Tab = name === "terminal" ? "activity" : "terminal";
          setTab(next);
          document.getElementById(tabId(next))?.focus();
        }
      }}
    >
      {label}
    </button>
  );

  return (
    <div className="run-mode-tabs">
      <div className="run-mode-tablist" role="tablist" aria-label={t("terminal.views")}>
        {tabButton("terminal", t("terminal.tab"))}
        {tabButton("activity", t("terminal.activityTab"))}
      </div>
      <div
        role="tabpanel"
        id={panelId("terminal")}
        aria-labelledby={tabId("terminal")}
        hidden={tab !== "terminal"}
        className="run-mode-panel"
      >
        <Suspense fallback={<p className="run-empty">{t("terminal.loading")}</p>}>
          <RunTerminal run={run} />
        </Suspense>
      </div>
      <div
        role="tabpanel"
        id={panelId("activity")}
        aria-labelledby={tabId("activity")}
        hidden={tab !== "activity"}
        className="run-mode-panel"
      >
        {children}
      </div>
    </div>
  );
}
