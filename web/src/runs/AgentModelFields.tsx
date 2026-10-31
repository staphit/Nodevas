import { useId, useState } from "react";
import {
  MAX_FALLBACKS,
  MAX_PIPELINE_ROUNDS,
  type AgentModel,
  type Runner,
  type RunnerModel,
  type RunPipeline,
} from "../api";
import { IconChevronDown, IconChevronUp, IconPlus, IconTrash } from "../icons";
import { useI18n } from "../i18n";
import { runnerModelLabel } from "../runners/runnerName";

/** What a picker offers: the agents runners reported in their hello. */
export interface CatalogAgent {
  kind: string;
  models: RunnerModel[];
}

/** Installed agents across `runners`, models merged by id, in first-seen
 * order. One runner gives its own catalog; several give the union. */
export function agentCatalog(runners: Runner[]): CatalogAgent[] {
  const byKind = new Map<string, CatalogAgent>();
  for (const runner of runners) {
    for (const agent of runner.info?.agents ?? []) {
      if (!agent.installed || !agent.kind) continue;
      let entry = byKind.get(agent.kind);
      if (!entry) {
        entry = { kind: agent.kind, models: [] };
        byKind.set(agent.kind, entry);
      }
      for (const model of agent.models ?? []) {
        if (!entry.models.some((held) => held.id === model.id)) entry.models.push(model);
      }
    }
  }
  return [...byKind.values()];
}

export function catalogDefaultModel(agent: CatalogAgent | undefined): string {
  const models = agent?.models ?? [];
  return (models.find((model) => model.default) ?? models[0])?.id ?? "";
}

/** Longest agent or model name the editors accept. */
export const MAX_NAME_CHARS = 64;

/** "agent" / "model" when that half is missing or too long, else "". */
export function agentModelProblem(value: AgentModel | undefined): "" | "agent" | "model" {
  const agent = value?.agent.trim() ?? "";
  const model = value?.model.trim() ?? "";
  if (!agent || agent.length > MAX_NAME_CHARS) return "agent";
  if (!model || model.length > MAX_NAME_CHARS) return "model";
  return "";
}

export function trimAgentModel(value: AgentModel): AgentModel {
  return { agent: value.agent.trim(), model: value.model.trim() };
}

/** "claude/sonnet" for a header or a note. */
export function agentModelText(value: AgentModel | undefined): string {
  if (!value) return "";
  return value.model ? `${value.agent}/${value.model}` : value.agent;
}

const OTHER = "\u0000other";

/**
 * An agent and a model. Picks from the runners' catalogs; types freely when
 * the runner is offline (its catalog may be stale), when nothing reported
 * one, when the value is not in it, or on request ("Other…").
 */
export function AgentModelPicker({
  label,
  value,
  onChange,
  catalog,
  offline = false,
  invalid = "",
}: {
  /** "Target", "Fallback 2"…: names both fields for assistive tech. */
  label: string;
  value: AgentModel;
  onChange: (value: AgentModel) => void;
  catalog: CatalogAgent[];
  offline?: boolean;
  /** Which half to mark invalid. */
  invalid?: "" | "agent" | "model";
}) {
  const { t } = useI18n();
  const listId = useId();
  const [manual, setManual] = useState(false);
  const agent = catalog.find((item) => item.kind === value.agent);
  const fits =
    (!value.agent || Boolean(agent)) &&
    (!value.model || !agent || agent.models.some((model) => model.id === value.model));
  const free = manual || offline || catalog.length === 0 || !fits;

  const agentLabel = t("routing.agentOf", { label });
  const modelLabel = t("routing.modelOf", { label });

  if (free) {
    const suggestions = agent?.models ?? [];
    return (
      <div className="am-picker">
        <input
          className="am-input"
          value={value.agent}
          maxLength={MAX_NAME_CHARS}
          placeholder={t("runs.agent")}
          aria-label={agentLabel}
          aria-invalid={invalid === "agent" ? true : undefined}
          list={catalog.length > 0 ? `${listId}-agents` : undefined}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          onChange={(event) => {
            setManual(true);
            onChange({ ...value, agent: event.target.value });
          }}
        />
        <input
          className="am-input"
          value={value.model}
          maxLength={MAX_NAME_CHARS}
          placeholder={t("runs.model")}
          aria-label={modelLabel}
          aria-invalid={invalid === "model" ? true : undefined}
          list={suggestions.length > 0 ? `${listId}-models` : undefined}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          onChange={(event) => {
            setManual(true);
            onChange({ ...value, model: event.target.value });
          }}
        />
        {catalog.length > 0 && (
          <datalist id={`${listId}-agents`}>
            {catalog.map((item) => (
              <option key={item.kind} value={item.kind} />
            ))}
          </datalist>
        )}
        {suggestions.length > 0 && (
          <datalist id={`${listId}-models`}>
            {suggestions.map((model) => (
              <option key={model.id} value={model.id}>
                {runnerModelLabel(t, model)}
              </option>
            ))}
          </datalist>
        )}
        {manual && !offline && catalog.length > 0 && (
          <button
            type="button"
            className="am-mode"
            onClick={() => {
              setManual(false);
              if (!fits) {
                const first = agent ?? catalog[0];
                onChange({ agent: first.kind, model: catalogDefaultModel(first) });
              }
            }}
          >
            {t("routing.pickFromList")}
          </button>
        )}
      </div>
    );
  }

  const models = agent?.models ?? [];
  return (
    <div className="am-picker">
      <select
        className="am-input"
        value={value.agent}
        aria-label={agentLabel}
        aria-invalid={invalid === "agent" ? true : undefined}
        onChange={(event) => {
          if (event.target.value === OTHER) {
            setManual(true);
            return;
          }
          const next = catalog.find((item) => item.kind === event.target.value);
          onChange({ agent: event.target.value, model: catalogDefaultModel(next) });
        }}
      >
        {!value.agent && <option value="">{t("routing.chooseAgent")}</option>}
        {catalog.map((item) => (
          <option key={item.kind} value={item.kind}>
            {item.kind}
          </option>
        ))}
        <option value={OTHER}>{t("routing.other")}</option>
      </select>
      <select
        className="am-input"
        value={value.model}
        aria-label={modelLabel}
        aria-invalid={invalid === "model" ? true : undefined}
        disabled={!agent}
        onChange={(event) => {
          if (event.target.value === OTHER) {
            setManual(true);
            return;
          }
          onChange({ ...value, model: event.target.value });
        }}
      >
        {!value.model && <option value="">{t("routing.chooseModel")}</option>}
        {models.map((model) => (
          <option key={model.id} value={model.id}>
            {runnerModelLabel(t, model) + (model.default ? t("runs.defaultSuffix") : "")}
          </option>
        ))}
        <option value={OTHER}>{t("routing.other")}</option>
      </select>
    </div>
  );
}

function move<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length) return list;
  const next = [...list];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

/** Moves one item of a list up (-1) or down (+1); out of range is a no-op. */
export function moveItem<T>(list: T[], index: number, delta: number): T[] {
  return move(list, index, index + delta);
}

/** Up / down / remove, for one item of an ordered list. */
export function ReorderButtons({
  index,
  count,
  name,
  onMove,
  onRemove,
}: {
  index: number;
  count: number;
  /** What the item is called: "Rule 2", "Fallback 1". */
  name: string;
  onMove: (delta: number) => void;
  onRemove: () => void;
}) {
  const { t } = useI18n();
  return (
    <span className="reorder-buttons">
      <button
        type="button"
        disabled={index === 0}
        aria-label={t("routing.moveUp", { name })}
        title={t("routing.moveUp", { name })}
        onClick={() => onMove(-1)}
      >
        <IconChevronUp size={13} />
      </button>
      <button
        type="button"
        disabled={index === count - 1}
        aria-label={t("routing.moveDown", { name })}
        title={t("routing.moveDown", { name })}
        onClick={() => onMove(1)}
      >
        <IconChevronDown size={13} />
      </button>
      <button
        type="button"
        className="is-danger"
        aria-label={t("routing.remove", { name })}
        title={t("routing.remove", { name })}
        onClick={onRemove}
      >
        <IconTrash size={13} />
      </button>
    </span>
  );
}

/**
 * An ordered fallback chain, at most MAX_FALLBACKS long: each entry is tried
 * in turn when the one before failed for a reason another model could fix.
 */
export function FallbackChainEditor({
  value,
  onChange,
  catalog,
  offline = false,
  showErrors = true,
}: {
  value: AgentModel[];
  onChange: (value: AgentModel[]) => void;
  catalog: CatalogAgent[];
  offline?: boolean;
  showErrors?: boolean;
}) {
  const { t } = useI18n();
  const full = value.length >= MAX_FALLBACKS;
  return (
    <div className="fallback-chain">
      {value.length === 0 ? (
        <p className="run-hint">{t("routing.noFallbacks")}</p>
      ) : (
        <ol className="fallback-list">
          {value.map((item, index) => {
            const name = t("routing.fallbackN", { n: index + 1 });
            const problem = agentModelProblem(item);
            return (
              <li key={index} className="fallback-item">
                <span className="fallback-index" aria-hidden="true">
                  {index + 1}
                </span>
                <AgentModelPicker
                  label={name}
                  value={item}
                  catalog={catalog}
                  offline={offline}
                  invalid={showErrors ? problem : ""}
                  onChange={(next) =>
                    onChange(value.map((held, i) => (i === index ? next : held)))
                  }
                />
                <ReorderButtons
                  index={index}
                  count={value.length}
                  name={name}
                  onMove={(delta) => onChange(moveItem(value, index, delta))}
                  onRemove={() => onChange(value.filter((_, i) => i !== index))}
                />
              </li>
            );
          })}
        </ol>
      )}
      <div className="fallback-actions">
        <button
          type="button"
          className="run-link-button"
          disabled={full}
          onClick={() => {
            const first = catalog[0];
            onChange([
              ...value,
              { agent: first?.kind ?? "", model: catalogDefaultModel(first) },
            ]);
          }}
        >
          <IconPlus size={12} /> {t("routing.addFallback")}
        </button>
        <small className="run-count">
          {t("routing.fallbackCount", { count: value.length, max: MAX_FALLBACKS })}
        </small>
      </div>
    </div>
  );
}

/** A pipeline's steps and rounds. `implementNote` replaces the implement
 * picker where the form's own agent and model are the implementer. */
export function PipelineFields({
  value,
  onChange,
  catalog,
  offline = false,
  implementNote,
  showErrors = true,
}: {
  value: RunPipeline;
  onChange: (value: RunPipeline) => void;
  catalog: CatalogAgent[];
  offline?: boolean;
  implementNote?: string;
  showErrors?: boolean;
}) {
  const { t } = useI18n();
  const roundsId = useId();
  const rounds = Array.from({ length: MAX_PIPELINE_ROUNDS }, (_, i) => i + 1);
  return (
    <div className="pipeline-fields">
      <div className="run-field">
        <span>{t("routing.implement")}</span>
        {implementNote !== undefined ? (
          <p className="pipeline-implement-note">{implementNote}</p>
        ) : (
          <AgentModelPicker
            label={t("routing.implement")}
            value={value.implement}
            catalog={catalog}
            offline={offline}
            invalid={showErrors ? agentModelProblem(value.implement) : ""}
            onChange={(implement) => onChange({ ...value, implement })}
          />
        )}
      </div>
      <div className="run-field">
        <span>{t("routing.review")}</span>
        <AgentModelPicker
          label={t("routing.review")}
          value={value.review}
          catalog={catalog}
          offline={offline}
          invalid={showErrors ? agentModelProblem(value.review) : ""}
          onChange={(review) => onChange({ ...value, review })}
        />
      </div>
      <label className="run-field pipeline-rounds" htmlFor={roundsId}>
        <span>{t("routing.rounds")}</span>
        <select
          id={roundsId}
          value={value.maxRounds}
          onChange={(event) => onChange({ ...value, maxRounds: Number(event.target.value) })}
        >
          {rounds.map((n) => (
            <option key={n} value={n}>
              {t("routing.roundsN", { n })}
            </option>
          ))}
        </select>
      </label>
      <p className="run-hint">{t("routing.pipelineHint")}</p>
    </div>
  );
}

/** A pipeline with the review step on a different agent where one exists. */
export function defaultPipeline(implement: AgentModel, catalog: CatalogAgent[]): RunPipeline {
  const other = catalog.find((item) => item.kind !== implement.agent) ?? catalog[0];
  return {
    implement: { ...implement },
    review: other
      ? { agent: other.kind, model: catalogDefaultModel(other) }
      : { ...implement },
    maxRounds: 1,
  };
}

/** Whether a pipeline's rounds are a whole number in 1..MAX_PIPELINE_ROUNDS. */
export function validRounds(rounds: number): boolean {
  return Number.isInteger(rounds) && rounds >= 1 && rounds <= MAX_PIPELINE_ROUNDS;
}
