// Station's Global agents tab: agents the viewer starts in a repository on
// one of their own runners, outside any project or board. A card to start
// one (open while there is none), each runner's load with how many runs it
// takes at once, then the agents themselves. In the single-user edition a
// "This computer" card (agents found, repos, settings) sits above the load.
// In Simple the tab opens on the describe box instead; the full form waits
// behind "More options" (component state: someone who always wants the form
// picks Everything).

import { useEffect, useState } from "react";

import { useI18n } from "../i18n";
import { ThisComputerCard } from "../local/ThisComputerCard";
import { useEdition } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useSimpleMode } from "../simple/simpleMode";
import { GlobalAgentForm } from "./GlobalAgentForm";
import { GlobalAgentList } from "./GlobalAgentList";
import { useGlobalAgents } from "./globalAgentsStore";
import { RunnerCapacity } from "./RunnerCapacity";
import { SimpleAgentBox } from "./SimpleAgentBox";

export function GlobalAgentsPanel() {
  const { t } = useI18n();
  const single = useEdition() === "single";
  const simple = useSimpleMode();
  const loaded = useGlobalAgents((s) => s.loaded);
  const empty = useGlobalAgents((s) => s.runs.length === 0);
  // null: not chosen yet, so the form is open exactly while there is no
  // global agent to list.
  const [formOpen, setFormOpen] = useState<boolean | null>(null);
  const open = formOpen ?? (loaded && empty);
  const [more, setMore] = useState(false);

  // The form needs each runner's agents, repositories and capabilities; the
  // budget line says up front when the hub would refuse.
  useEffect(() => {
    void useRunners.getState().refresh();
    void useBudget.getState().load();
  }, []);

  return (
    <>
      {simple ? (
        <>
          <SimpleAgentBox variant="station" />
          <section className="station-panel global-agents" aria-labelledby="global-agents-title">
            <div className="station-panel-head">
              <h2 id="global-agents-title">{t("station.tab.global")}</h2>
              <button
                type="button"
                className="run-link-button station-head-end"
                aria-expanded={more}
                onClick={() => setMore((was) => !was)}
              >
                {t(more ? "globalAgents.simple.fewerOptions" : "globalAgents.simple.moreOptions")}
              </button>
            </div>
            {more && (
              <>
                <p className="station-panel-meta global-agents-intro">{t("simple.globalIntro")}</p>
                <GlobalAgentForm />
              </>
            )}
          </section>
        </>
      ) : (
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
      )}
      {single && <ThisComputerCard />}
      <RunnerCapacity />
      <GlobalAgentList />
    </>
  );
}
