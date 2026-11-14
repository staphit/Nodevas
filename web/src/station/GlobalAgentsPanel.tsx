// Station's Global agents tab: agents the viewer starts in a repository on
// one of their own runners, outside any project or board. A card to start
// one (open while there is none), each runner's load with how many runs it
// takes at once, then the agents themselves. In the single-user edition a
// "This computer" card (agents found, repos, settings) sits above the load.

import { useEffect, useState } from "react";

import { useI18n } from "../i18n";
import { ThisComputerCard } from "../local/ThisComputerCard";
import { useEdition } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { GlobalAgentForm } from "./GlobalAgentForm";
import { GlobalAgentList } from "./GlobalAgentList";
import { useGlobalAgents } from "./globalAgentsStore";
import { RunnerCapacity } from "./RunnerCapacity";

export function GlobalAgentsPanel() {
  const { t } = useI18n();
  const single = useEdition() === "single";
  const loaded = useGlobalAgents((s) => s.loaded);
  const empty = useGlobalAgents((s) => s.runs.length === 0);
  // null: not chosen yet, so the form is open exactly while there is no
  // global agent to list.
  const [formOpen, setFormOpen] = useState<boolean | null>(null);
  const open = formOpen ?? (loaded && empty);

  // The form needs each runner's agents, repositories and capabilities; the
  // budget line says up front when the hub would refuse.
  useEffect(() => {
    void useRunners.getState().refresh();
    void useBudget.getState().load();
  }, []);

  return (
    <>
      <section className="station-panel global-agents" aria-labelledby="global-agents-title">
        <div className="station-panel-head">
          <h2 id="global-agents-title">{t("station.tab.global")}</h2>
          {!open && (
            <button type="button" className="ap-btn is-primary station-head-end" onClick={() => setFormOpen(true)}>
              {t("globalAgents.new")}
            </button>
          )}
        </div>
        <p className="station-panel-meta global-agents-intro">
          {t(single ? "globalAgents.introLocal" : "globalAgents.intro")}
        </p>
        {open && (
          <GlobalAgentForm
            // Started: the form stays open for the next one, keeping the
            // runner, repository and agent.
            onStarted={() => setFormOpen(true)}
            onClose={empty ? undefined : () => setFormOpen(false)}
          />
        )}
      </section>
      {single && <ThisComputerCard />}
      <RunnerCapacity />
      <GlobalAgentList />
    </>
  );
}
