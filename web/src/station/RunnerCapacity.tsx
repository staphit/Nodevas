// The viewer's runners on the Global agents tab, one line each: how many
// runs are at work against how many it takes at once, what waits, and what
// it spent today; then a stepper for how many it takes at once. The runner
// decides: its owner may keep that change to the machine itself
// (remoteSettings), and it never goes past its own maxConcurrentCeiling.

import type { RunnerLoad } from "../api";
import { useI18n } from "../i18n";
import { useEdition } from "../runners/edition";
import { plainRunnerName } from "../runners/runnerName";
import { formatCompact, formatCost } from "../runs/runFormat";
import { useSimpleMode } from "../simple/simpleMode";
import { useGlobalAgents } from "./globalAgentsStore";

const SETTABLE_BLOCKS = new Set(["offline", "outdated", "settings_off"]);

/** Why the hub cannot change this runner's limit, as a capacity key's
 * suffix; "" when it can. A runner that names no ceiling cannot be set. */
export function capacityBlock(load: Pick<RunnerLoad, "settable" | "ceiling">): string {
  if (load.settable) return SETTABLE_BLOCKS.has(load.settable) ? load.settable : "outdated";
  return load.ceiling > 0 ? "" : "outdated";
}

export function RunnerCapacity() {
  const { t } = useI18n();
  const loads = useGlobalAgents((s) => s.runners);
  // Single-user: the one machine is this computer, not "a runner".
  const single = useEdition() === "single";
  if (loads.length === 0) return null;
  return (
    <section className="station-panel global-capacity" aria-labelledby="global-capacity-title">
      <div className="station-panel-head">
        <h2 id="global-capacity-title">{t(single ? "local.concurrency" : "globalAgents.capacity.title")}</h2>
      </div>
      <ul className="global-capacity-list">
        {loads.map((load) => (
          <CapacityRow key={load.runnerId} load={load} />
        ))}
      </ul>
    </section>
  );
}

function CapacityRow({ load }: { load: RunnerLoad }) {
  const { t } = useI18n();
  // Simple: this computer by name, what it cost without token counts, and
  // no setting's name for the most it takes.
  const simple = useSimpleMode();
  const saving = useGlobalAgents((s) => Boolean(s.saving[load.runnerId]));
  const note = useGlobalAgents((s) => s.settingNotes[load.runnerId]);
  const runner = plainRunnerName(t, load.name, simple, true) || load.runnerId;
  const label = plainRunnerName(t, load.name, simple) || load.runnerId;
  const max = Math.max(1, load.maxConcurrent || 1);
  const block = capacityBlock(load);
  const blockText = block ? t(`globalAgents.capacity.${block}`, { runner }) : "";
  const ceilingText =
    load.ceiling > 0 && !simple ? t("globalAgents.capacity.ceiling", { max: load.ceiling, runner }) : "";
  const locked = Boolean(block) || saving;
  const set = (value: number) => void useGlobalAgents.getState().setMaxConcurrent(load.runnerId, value);
  const cost = formatCost(load.today?.costUsd) || "$0";
  const line = simple
    ? t("simple.capacity.line", { busy: load.busy, max, queued: load.queued, cost })
    : t("globalAgents.capacity.line", {
        busy: load.busy,
        max,
        queued: load.queued,
        cost,
        tokens: formatCompact(load.today?.tokens ?? 0),
      });

  return (
    <li className="global-capacity-row">
      <span className="global-capacity-who">
        <span className={`station-dot ${load.online ? "status-running" : "is-off"}`} aria-hidden="true" />
        <span className="global-capacity-name">{label}</span>
      </span>
      <span className="global-capacity-line station-panel-meta">{line}</span>
      <span
        className={`global-capacity-stepper${locked ? " is-locked" : ""}`}
        role="group"
        aria-label={t("globalAgents.capacity.atOnce")}
        title={blockText || undefined}
      >
        <span className="global-capacity-label" aria-hidden="true">
          {t("globalAgents.capacity.atOnce")}
        </span>
        <button
          type="button"
          className="ap-btn global-capacity-step"
          aria-label={t("globalAgents.capacity.less", { runner })}
          title={blockText || undefined}
          disabled={locked || max <= 1}
          onClick={() => set(max - 1)}
        >
          −
        </button>
        <output className="global-capacity-value" aria-label={t("globalAgents.capacity.value", { count: max, runner })}>
          {max}
        </output>
        <button
          type="button"
          className="ap-btn global-capacity-step"
          aria-label={t("globalAgents.capacity.more", { runner })}
          title={blockText || (max >= load.ceiling ? ceilingText : "") || undefined}
          disabled={locked || max >= load.ceiling}
          onClick={() => set(max + 1)}
        >
          +
        </button>
      </span>
      {(blockText || ceilingText || saving) && (
        <span className="global-capacity-hint station-panel-meta">
          {saving ? t("globalAgents.capacity.saving") : blockText || ceilingText}
        </span>
      )}
      {note && (
        <span
          className={`global-capacity-note is-${note.kind}`}
          role={note.kind === "error" ? "alert" : "status"}
        >
          {note.text}
        </span>
      )}
    </li>
  );
}
