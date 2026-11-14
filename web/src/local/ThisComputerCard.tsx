// Station's Global agents tab, single-user edition: "This computer" in one
// line (the agents found, the repos added, how many runs at once) and a
// Settings button that opens the same panel as Settings → Agent in place.

import { useEffect, useId, useState } from "react";

import { useI18n } from "../i18n";
import { useLocalRunner } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { localSummary, ThisComputerPanel } from "./ThisComputerPanel";

export function ThisComputerCard() {
  const { t } = useI18n();
  const runner = useLocalRunner();
  const loaded = useRunners((s) => s.loaded);
  const [open, setOpen] = useState(false);
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);

  const summary = loaded ? localSummary(t, runner) : t("common.loading");

  return (
    <section className="station-panel this-computer-card" aria-labelledby={titleId}>
      <div className="station-panel-head">
        <h2 id={titleId}>{t("local.title")}</h2>
        <span className="station-panel-meta this-computer-summary">{summary}</span>
        <button
          type="button"
          className="ap-btn station-head-end"
          aria-expanded={open}
          aria-controls={open ? bodyId : undefined}
          onClick={() => setOpen((value) => !value)}
        >
          {open ? t("local.hide") : t("local.configure")}
        </button>
      </div>
      {open && (
        <div id={bodyId}>
          <ThisComputerPanel />
        </div>
      )}
    </section>
  );
}
