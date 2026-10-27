// Settings › Agents: the workstation's switch in the header, a line per
// runner with one "Add runner", and the rarely changed rest under Advanced,
// collapsed until asked for.

import { useEffect, useId } from "react";

import type { RunBudgetStatus } from "../api";
import { useTeamServer, useViewer } from "../components/SignIn";
import { useI18n } from "../i18n";
import { RunnerAccessTokenSettings } from "../runners/LinkDevice";
import { AutoAcceptSettings, autoAcceptCounts, RunnersPanel } from "../runners/RunnersModal";
import { useRunners } from "../runners/runnerStore";
import { useWorkstationInUse } from "../runners/workstation";
import { useBudget } from "../runs/budgetStore";
import { BudgetSettings } from "../runs/BudgetSettings";
import { formatCompact, formatCount } from "../runs/runFormat";
import { RoutingSettings } from "../runs/RoutingSettings";
import { useRouting } from "../runs/routingStore";
import { useApp, usePreference } from "../store";
import { UsageModal } from "../usage/UsageModal";
import { formatTokens, formatUSD } from "../usage/usageFormat";
import { useUsage } from "../usage/usageStore";
import type { SettingsRow } from "./openSettings";
import { SettingsDisclosure } from "./SettingsDisclosure";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** "$5.00 a day · 20 runs a day", "No limit", or "" before it is known. */
export function budgetValue(status: RunBudgetStatus | null, t: Translate): string {
  if (!status) return "";
  const budget = status.budget;
  const parts: string[] = [];
  if (budget?.dailyCostUsd) parts.push(t("settingsPage.budgetCost", { cost: `$${budget.dailyCostUsd.toFixed(2)}` }));
  if (budget?.dailyRuns) {
    parts.push(
      budget.dailyRuns === 1
        ? t("settingsPage.budgetRunOne")
        : t("settingsPage.budgetRuns", { count: formatCount(budget.dailyRuns) }),
    );
  }
  if (budget?.dailyTokens) parts.push(t("settingsPage.budgetTokens", { count: formatCompact(budget.dailyTokens) }));
  return parts.length > 0 ? parts.join(" · ") : t("settingsPage.budgetNone");
}

/** "2 rules", "None", or "" before they are known. */
export function rulesValue(count: number | null, t: Translate): string {
  if (count === null) return "";
  if (count === 0) return t("settingsPage.none");
  return count === 1 ? t("settingsPage.ruleOne") : t("settingsPage.rules", { count });
}

/** "On for 1 of 2", "Off", or "" with no runner to ask. */
export function autoAcceptValue({ on, total }: { on: number; total: number }, t: Translate): string {
  if (total === 0) return "";
  return on === 0 ? t("settingsPage.off") : t("settingsPage.autoAcceptValue", { on, total });
}

function Advanced({
  open,
  onToggle,
  row,
  toggleRow,
  leave,
}: {
  open: boolean;
  onToggle: () => void;
  row: SettingsRow | null;
  toggleRow: (row: SettingsRow) => void;
  leave: () => void;
}) {
  const { t } = useI18n();
  const bodyId = useId();
  const viewer = useViewer();
  const teamServer = useTeamServer();
  // The runners' Access token is the server's, for its administrators.
  const admin = Boolean(teamServer && viewer?.role === "admin");
  const budget = useBudget((s) => s.status);
  const rules = useRouting((s) => s.rules);
  const runners = useRunners((s) => s.runners);
  const usage = useUsage((s) => s.report);

  // The values are fetched when they are shown, as the parts themselves do.
  useEffect(() => {
    if (!open) return;
    void useBudget.getState().load();
    void useRouting.getState().load();
  }, [open]);

  const items = admin ? 5 : 4;
  return (
    <section className="settings-page-pane settings-card settings-advanced" aria-label={t("settingsPage.advanced")}>
      <button
        type="button"
        className="settings-disclosure-toggle settings-advanced-toggle"
        aria-expanded={open}
        aria-controls={open ? bodyId : undefined}
        onClick={onToggle}
      >
        <span className="settings-disclosure-label">{t("settingsPage.advanced")}</span>
        {!open && <span className="settings-disclosure-value">{t("settingsPage.advancedCount", { count: items })}</span>}
        <span className="settings-disclosure-chevron" aria-hidden="true">
          ›
        </span>
      </button>
      {open && (
        <div id={bodyId} className="settings-advanced-body">
          <SettingsDisclosure
            label={t("settingsPage.part.budget")}
            value={budgetValue(budget, t)}
            open={row === "budget"}
            onToggle={() => toggleRow("budget")}
          >
            <BudgetSettings />
          </SettingsDisclosure>
          <SettingsDisclosure
            label={t("settingsPage.part.routing")}
            value={rulesValue(rules ? rules.length : null, t)}
            open={row === "routing"}
            onToggle={() => toggleRow("routing")}
          >
            <RoutingSettings />
          </SettingsDisclosure>
          {/* Read-only: a runner's policy is changed on that runner. */}
          <SettingsDisclosure
            label={t("runners.autoAccept")}
            value={autoAcceptValue(autoAcceptCounts(runners), t)}
            open={row === "autoAccept"}
            onToggle={() => toggleRow("autoAccept")}
          >
            <AutoAcceptSettings />
          </SettingsDisclosure>
          <SettingsDisclosure
            label={t("settingsPage.usageDetails")}
            value={
              usage
                ? t("settingsPage.usageValue", {
                    tokens: formatTokens(usage.totals.totalTokens),
                    cost: formatUSD(usage.totals.costUsd),
                  })
                : ""
            }
            open={row === "usage"}
            onToggle={() => toggleRow("usage")}
          >
            <UsageModal onClose={leave} />
          </SettingsDisclosure>
          {admin && (
            <SettingsDisclosure
              label={t("runners.link.accessTitle")}
              open={row === "accessToken"}
              onToggle={() => toggleRow("accessToken")}
            >
              <RunnerAccessTokenSettings />
            </SettingsDisclosure>
          )}
        </div>
      )}
    </section>
  );
}

export function AgentsSection({
  row,
  toggleRow,
  advanced,
  toggleAdvanced,
  addRequested,
  onAddOpened,
  leave,
}: {
  row: SettingsRow | null;
  toggleRow: (row: SettingsRow) => void;
  advanced: boolean;
  toggleAdvanced: () => void;
  /** Whether the add-runner form was asked for (the search box). */
  addRequested: boolean;
  onAddOpened: () => void;
  leave: () => void;
}) {
  const { t } = useI18n();
  const enabled = usePreference("agentWorkstation");
  const inUse = useWorkstationInUse();
  const on = enabled || inUse;
  const hintId = useId();

  return (
    <>
      <section className="settings-page-pane settings-card" aria-label={t("settingsPage.page.agents")}>
        <div className="settings-card-head">
          <h2>{t("settingsPage.page.agents")}</h2>
          {/* Once this workspace uses it, it stays: a runner or a run would
              turn it straight back on. */}
          <label className="settings-switch">
            <input
              type="checkbox"
              role="switch"
              checked={on}
              disabled={inUse}
              aria-describedby={on && !inUse ? undefined : hintId}
              onChange={() => useApp.getState().updateUIPreference("agentWorkstation", !on)}
            />
            <span>{t("settingsPage.workstationSwitch")}</span>
          </label>
        </div>
        {/* What turning it on brings, or why it cannot be turned off. */}
        {(!on || inUse) && (
          <p id={hintId} className="settings-hint">
            {inUse ? t("workstation.inUse") : t("workstation.hint")}
          </p>
        )}
        {on && <RunnersPanel addRequested={addRequested} onAddOpened={onAddOpened} />}
      </section>
      {/* With the workstation off, its switch is all Agents shows. */}
      {on && <Advanced open={advanced} onToggle={toggleAdvanced} row={row} toggleRow={toggleRow} leave={leave} />}
    </>
  );
}
