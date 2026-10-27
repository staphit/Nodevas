import { useEffect, useId, useMemo, useState } from "react";
import {
  MAX_ROUTE_RULES,
  ROUTE_MATCHES,
  type RouteRule,
  type Runner,
} from "../api";
import { useViewer } from "../components/SignIn";
import { IconPlus } from "../icons";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import {
  AgentModelPicker,
  agentCatalog,
  agentModelProblem,
  agentModelText,
  defaultPipeline,
  FallbackChainEditor,
  moveItem,
  PipelineFields,
  ReorderButtons,
  trimAgentModel,
  validRounds,
} from "./AgentModelFields";
import { useRouting } from "./routingStore";
import { normalizeKind } from "../domain/graph/nodeTypes";
import { nodeTypeLabel, useNodeTypes } from "../nodeTypeTheme";
import { useRoleOptions } from "../roles/roleLabels";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** Longest tag or kind value a rule takes. */
export const MAX_ROUTE_VALUE_CHARS = 64;

/** Whether a match kind takes a value (a tag, a node kind or a role). */
export function matchTakesValue(match: string): boolean {
  return match === "tag" || match === "kind" || match === "role";
}

/**
 * "tag = review", "Kind = Review", "Project default", "Global default". A kind
 * value reads as its type's name when `kindLabel` knows it, and as written
 * otherwise (a stale or another project's type).
 */
export function routeLabel(
  t: Translate,
  match: string,
  value?: string,
  kindLabel?: (kind: string) => string | undefined,
  roleLabel?: (role: string) => string | undefined,
): string {
  switch (match) {
    case "tag":
    case "kind":
    case "role": {
      const shown =
        match === "kind" && value
          ? (kindLabel?.(value) ?? value)
          : match === "role" && value
            ? (roleLabel?.(value) ?? value)
            : value;
      return shown
        ? t("routing.labelValue", { match: t(`routing.match.${match}`), value: shown })
        : t(`routing.match.${match}`);
    }
    case "project":
      return t("routing.match.project");
    case "default":
      return t("routing.match.default");
    default:
      return match;
  }
}

let idCounter = 0;
/**
 * Node types a "kind" rule can route — the ones an agent may work — and a
 * lookup from a rule's value to the type's name.
 */
export function useRouteKinds() {
  const { t } = useI18n();
  const nodeTypes = useNodeTypes();
  return useMemo(() => {
    const options = nodeTypes
      .filter((type) => type.agentMode !== "none")
      .map((type) => ({ value: type.id, label: nodeTypeLabel(type, t) }));
    const all = new Map(nodeTypes.map((type) => [type.id, nodeTypeLabel(type, t)]));
    const kindLabel = (kind: string) => all.get(normalizeKind(kind) || "task");
    return { options, kindLabel };
  }, [nodeTypes, t]);
}

/** A new rule's id. The hub keeps it, or assigns its own on save. */
export function newRuleId(): string {
  idCounter += 1;
  const random =
    typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID().slice(0, 8)
      : Math.random().toString(36).slice(2, 10);
  return `rule-${random}${idCounter}`;
}

/** A blank rule: nothing is pre-filled, the owner decides every route. */
export function emptyRule(project: string): RouteRule {
  return {
    id: newRuleId(),
    project,
    match: "tag",
    value: "",
    runnerId: "",
    target: { agent: "", model: "" },
    fallbacks: [],
    pipeline: null,
  };
}

export type RuleField = "match" | "value" | "project" | "target" | "fallbacks" | "pipeline";
export type RuleProblems = Partial<Record<RuleField, string>>;

function ruleKey(rule: RouteRule): string {
  const value = matchTakesValue(rule.match) ? (rule.value ?? "").trim().toLowerCase() : "";
  return `${rule.project}\u0000${rule.match}\u0000${value}`;
}

/** Per rule, each field's problem as an i18n key; the hub checks the same
 * things and answers 400 when one slips past. */
export function ruleProblems(rules: RouteRule[]): RuleProblems[] {
  const seen = new Set<string>();
  return rules.map((rule) => {
    const problems: RuleProblems = {};
    if (!(ROUTE_MATCHES as readonly string[]).includes(rule.match)) {
      problems.match = "routing.err.match";
    }
    const value = (rule.value ?? "").trim();
    if (matchTakesValue(rule.match)) {
      if (!value) problems.value = `routing.err.value.${rule.match}`;
      else if (value.length > MAX_ROUTE_VALUE_CHARS) problems.value = "routing.err.valueLong";
    }
    if (rule.match === "project" && !rule.project) problems.project = "routing.err.project";
    const target = agentModelProblem(rule.target);
    if (target) problems.target = `routing.err.${target}`;
    const fallbacks = rule.fallbacks ?? [];
    if (fallbacks.some((item) => agentModelProblem(item))) {
      problems.fallbacks = "routing.err.fallbacks";
    }
    const pipeline = rule.pipeline;
    if (
      pipeline &&
      (agentModelProblem(pipeline.implement) ||
        agentModelProblem(pipeline.review) ||
        !validRounds(pipeline.maxRounds))
    ) {
      problems.pipeline = "routing.err.pipeline";
    }
    if (!problems.match && !problems.value) {
      const key = ruleKey(rule);
      // Rules are tried in order: a second rule for the same match never wins.
      if (seen.has(key)) problems.match = "routing.err.duplicate";
      seen.add(key);
    }
    return problems;
  });
}

/** The PUT body's rules: trimmed, with empty optionals left out. */
export function rulesPayload(rules: RouteRule[]): RouteRule[] {
  return rules.map((rule) => {
    const out: RouteRule = {
      id: rule.id,
      project: rule.project,
      match: rule.match,
      target: trimAgentModel(rule.target),
    };
    if (matchTakesValue(rule.match)) out.value = (rule.value ?? "").trim();
    if (rule.runnerId) out.runnerId = rule.runnerId;
    const fallbacks = (rule.fallbacks ?? []).map(trimAgentModel);
    if (fallbacks.length > 0) out.fallbacks = fallbacks;
    if (rule.pipeline) {
      out.pipeline = {
        implement: trimAgentModel(rule.pipeline.implement),
        review: trimAgentModel(rule.pipeline.review),
        maxRounds: rule.pipeline.maxRounds,
      };
    }
    return out;
  });
}

function FieldError({ id, text }: { id: string; text: string }) {
  if (!text) return null;
  return (
    <small id={id} className="run-field-error">
      {text}
    </small>
  );
}

function RuleEditor({
  rule,
  index,
  count,
  problems,
  showErrors,
  project,
  runners,
  tags,
  onChange,
  onMove,
  onRemove,
}: {
  rule: RouteRule;
  index: number;
  count: number;
  problems: RuleProblems;
  showErrors: boolean;
  project: string;
  runners: Runner[];
  tags: string[];
  onChange: (rule: RouteRule) => void;
  onMove: (delta: number) => void;
  onRemove: () => void;
}) {
  const { t } = useI18n();
  const ids = { match: useId(), value: useId(), scope: useId(), runner: useId(), list: useId() };
  const errorIds = { match: useId(), value: useId(), scope: useId(), target: useId(), more: useId() };
  const name = t("routing.ruleN", { n: index + 1 });
  const { options: kindOptions, kindLabel } = useRouteKinds();
  const { options: roleOptions, roleLabel } = useRoleOptions();
  const shown = (field: RuleField) => (showErrors && problems[field] ? t(problems[field]!) : "");

  const runner = runners.find((item) => item.id === rule.runnerId);
  const catalog = useMemo(
    () => agentCatalog(rule.runnerId ? (runner ? [runner] : []) : runners),
    [rule.runnerId, runner, runners],
  );
  const offline = rule.runnerId
    ? !runner?.online
    : !runners.some((item) => item.online);

  const scope = rule.project === "" ? "" : rule.project;
  const scopeFixed = rule.match === "project" || rule.match === "default";

  const setMatch = (match: string) => {
    const next: RouteRule = { ...rule, match };
    // A project default lives in a project; a global default in none.
    if (match === "project" && !rule.project) next.project = project;
    if (match === "default") next.project = "";
    onChange(next);
  };

  const fallbacks = rule.fallbacks ?? [];
  const valueList = tags;
  const kindValue = (rule.value ?? "").trim();
  const staleKind =
    kindValue && !kindOptions.some((option) => option.value === kindValue) ? kindValue : "";
  const staleRole =
    kindValue && !roleOptions.some((option) => option.value === kindValue) ? kindValue : "";

  return (
    <li className="route-rule" data-testid={`route-rule-${index}`} aria-label={name}>
      <div className="route-rule-head">
        <span className="route-rule-index" aria-hidden="true">
          {index + 1}
        </span>
        <strong className="route-rule-summary">
          {routeLabel(t, rule.match, (rule.value ?? "").trim(), kindLabel, roleLabel)}
          {rule.target.agent ? ` → ${agentModelText(rule.target)}` : ""}
        </strong>
        <ReorderButtons
          index={index}
          count={count}
          name={name}
          onMove={onMove}
          onRemove={onRemove}
        />
      </div>

      <div className="route-rule-grid">
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.match}>{t("routing.matchLabel")}</label>
          <select
            id={ids.match}
            value={rule.match}
            aria-invalid={shown("match") ? true : undefined}
            aria-describedby={shown("match") ? errorIds.match : undefined}
            onChange={(event) => setMatch(event.target.value)}
          >
            {ROUTE_MATCHES.map((match) => (
              <option key={match} value={match}>
                {t(`routing.match.${match}`)}
              </option>
            ))}
            {!(ROUTE_MATCHES as readonly string[]).includes(rule.match) && (
              <option value={rule.match}>{rule.match}</option>
            )}
          </select>
          <FieldError id={errorIds.match} text={shown("match")} />
        </div>

        {rule.match === "kind" && (
          <div className="run-field">
            <label className="run-field-label" htmlFor={ids.value}>{t("routing.value.kind")}</label>
            <select
              id={ids.value}
              value={kindValue}
              aria-invalid={shown("value") ? true : undefined}
              aria-describedby={shown("value") ? errorIds.value : undefined}
              onChange={(event) => onChange({ ...rule, value: event.target.value })}
            >
              <option value="" disabled>
                {t("routing.valuePlaceholder.kind")}
              </option>
              {kindOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
              {/* A type that is gone, or not one agents work any more, stays
                  visible as written so the rule is not silently changed. */}
              {staleKind && <option value={staleKind}>{kindLabel(staleKind) ?? staleKind}</option>}
            </select>
            <FieldError id={errorIds.value} text={shown("value")} />
          </div>
        )}

        {rule.match === "role" && (
          <div className="run-field">
            <label className="run-field-label" htmlFor={ids.value}>{t("routing.value.role")}</label>
            <select
              id={ids.value}
              value={kindValue}
              aria-invalid={shown("value") ? true : undefined}
              aria-describedby={shown("value") ? errorIds.value : undefined}
              onChange={(event) => onChange({ ...rule, value: event.target.value })}
            >
              <option value="" disabled>
                {t("routing.valuePlaceholder.role")}
              </option>
              {roleOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
              {staleRole && <option value={staleRole}>{staleRole}</option>}
            </select>
            <FieldError id={errorIds.value} text={shown("value")} />
          </div>
        )}

        {matchTakesValue(rule.match) && rule.match !== "kind" && rule.match !== "role" && (
          <div className="run-field">
            <label className="run-field-label" htmlFor={ids.value}>{t(`routing.value.${rule.match}`)}</label>
            <input
              id={ids.value}
              value={rule.value ?? ""}
              maxLength={MAX_ROUTE_VALUE_CHARS + 16}
              list={valueList.length > 0 ? ids.list : undefined}
              placeholder={t(`routing.valuePlaceholder.${rule.match}`)}
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
              aria-invalid={shown("value") ? true : undefined}
              aria-describedby={shown("value") ? errorIds.value : undefined}
              onChange={(event) => onChange({ ...rule, value: event.target.value })}
            />
            {valueList.length > 0 && (
              <datalist id={ids.list}>
                {valueList.map((item) => (
                  <option key={item} value={item} />
                ))}
              </datalist>
            )}
            <FieldError id={errorIds.value} text={shown("value")} />
          </div>
        )}

        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.scope}>{t("routing.scope")}</label>
          <select
            id={ids.scope}
            value={scope}
            disabled={scopeFixed}
            aria-invalid={shown("project") ? true : undefined}
            aria-describedby={shown("project") ? errorIds.scope : undefined}
            onChange={(event) => onChange({ ...rule, project: event.target.value })}
          >
            {project && <option value={project}>{t("routing.scopeThis", { project })}</option>}
            <option value="">{t("routing.scopeAll")}</option>
            {scope && scope !== project && (
              <option value={scope}>{t("routing.scopeOther", { project: scope })}</option>
            )}
          </select>
          <FieldError id={errorIds.scope} text={shown("project")} />
        </div>

        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.runner}>{t("routing.runner")}</label>
          <select
            id={ids.runner}
            value={rule.runnerId ?? ""}
            onChange={(event) => onChange({ ...rule, runnerId: event.target.value })}
          >
            <option value="">{t("routing.anyRunner")}</option>
            {runners.map((item) => (
              <option key={item.id} value={item.id}>
                {item.online ? item.name : `${item.name} — ${t("runs.runnerOffline")}`}
              </option>
            ))}
            {rule.runnerId && !runner && (
              <option value={rule.runnerId}>{t("routing.unknownRunner")}</option>
            )}
          </select>
        </div>
      </div>

      <div className="run-field">
        <span>{t("routing.target")}</span>
        <AgentModelPicker
          label={t("routing.target")}
          value={rule.target}
          catalog={catalog}
          offline={offline}
          invalid={showErrors ? agentModelProblem(rule.target) : ""}
          onChange={(target) => onChange({ ...rule, target })}
        />
        {offline && catalog.length > 0 && <small className="run-hint">{t("routing.offlineHint")}</small>}
        <FieldError id={errorIds.target} text={shown("target")} />
      </div>

      <details className="route-sub" open={fallbacks.length > 0 || undefined}>
        <summary>{t("routing.fallbacks", { count: fallbacks.length })}</summary>
        <FallbackChainEditor
          value={fallbacks}
          catalog={catalog}
          offline={offline}
          showErrors={showErrors}
          onChange={(next) => onChange({ ...rule, fallbacks: next })}
        />
      </details>
      <FieldError id={errorIds.more} text={shown("fallbacks")} />

      <label className="route-toggle">
        <input
          type="checkbox"
          checked={Boolean(rule.pipeline)}
          onChange={(event) =>
            onChange({
              ...rule,
              pipeline: event.target.checked ? defaultPipeline(rule.target, catalog) : null,
            })
          }
        />
        <span>{t("routing.pipelineToggle")}</span>
      </label>
      {rule.pipeline && (
        <PipelineFields
          value={rule.pipeline}
          catalog={catalog}
          offline={offline}
          showErrors={showErrors}
          onChange={(pipeline) => onChange({ ...rule, pipeline })}
        />
      )}
      <FieldError id={`${errorIds.more}-p`} text={shown("pipeline")} />
    </li>
  );
}

/**
 * The viewer's routing rules: which agent and model a dispatch starts from,
 * by node tag, node kind, project default or global default, tried in order.
 * Nothing here is built in — no rule exists until the owner adds one.
 */
export function RoutingSettings() {
  const { t } = useI18n();
  const viewer = useViewer();
  const saved = useRouting((s) => s.rules);
  const loading = useRouting((s) => s.loading);
  const error = useRouting((s) => s.error);
  const saving = useRouting((s) => s.saving);
  const saveError = useRouting((s) => s.saveError);
  const load = useRouting((s) => s.load);
  const save = useRouting((s) => s.save);
  const allRunners = useRunners((s) => s.runners);
  const project = useApp((s) => s.activeProject) ?? "";
  const nodes = useApp((s) => s.graph?.nodes);
  const [draft, setDraft] = useState<RouteRule[] | null>(null);
  const [attempted, setAttempted] = useState(false);
  const [justSaved, setJustSaved] = useState(false);

  useEffect(() => {
    void load();
  }, [load]);

  // The viewer's own runners: a rule can only name a runner one may dispatch to.
  const runners = useMemo(
    () => (viewer ? allRunners.filter((runner) => runner.ownerId === viewer.id) : allRunners),
    [allRunners, viewer],
  );
  const tags = useMemo(() => {
    const found = new Set<string>();
    for (const node of nodes ?? []) for (const tag of node.tags ?? []) if (tag) found.add(tag);
    return [...found].sort();
  }, [nodes]);

  const rules = draft ?? saved ?? [];
  const problems = useMemo(() => ruleProblems(rules), [rules]);
  const problemCount = problems.filter((item) => Object.keys(item).length > 0).length;
  const dirty =
    draft !== null &&
    JSON.stringify(rulesPayload(draft)) !== JSON.stringify(rulesPayload(saved ?? []));

  const edit = (next: RouteRule[]) => {
    setJustSaved(false);
    useRouting.getState().clearSaveError();
    setDraft(next);
  };

  const submit = async () => {
    if (saving || !dirty) return;
    if (problemCount > 0) {
      setAttempted(true);
      return;
    }
    if (await save(rulesPayload(rules))) {
      setDraft(null);
      setAttempted(false);
      setJustSaved(true);
    }
  };

  if (saved === null) {
    return (
      <div className="routing-settings">
        {error ? (
          <div className="notify-message error" role="alert">
            {error}
          </div>
        ) : (
          <p className="runner-empty">{loading ? t("common.loading") : ""}</p>
        )}
      </div>
    );
  }

  const full = rules.length >= MAX_ROUTE_RULES;
  const refusal = saveError
    ? saveError.kind === "denied"
      ? t("routing.denied", { message: saveError.message })
      : saveError.kind === "invalid"
        ? t("routing.invalid", { message: saveError.message })
        : saveError.message
    : "";

  return (
    <form
      className="routing-settings"
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <p className="runner-hint">{t("routing.description")}</p>

      {rules.length === 0 ? (
        <p className="runner-empty">{t("routing.empty")}</p>
      ) : (
        <ol className="route-rules" aria-label={t("routing.title")}>
          {rules.map((rule, index) => (
            <RuleEditor
              key={rule.id || index}
              rule={rule}
              index={index}
              count={rules.length}
              problems={problems[index]}
              showErrors={attempted}
              project={project}
              runners={runners}
              tags={tags}
              onChange={(next) => edit(rules.map((held, i) => (i === index ? next : held)))}
              onMove={(delta) => edit(moveItem(rules, index, delta))}
              onRemove={() => edit(rules.filter((_, i) => i !== index))}
            />
          ))}
        </ol>
      )}

      <div className="routing-add">
        <button
          type="button"
          className="run-link-button"
          disabled={full}
          onClick={() => edit([...rules, emptyRule(project)])}
        >
          <IconPlus size={12} /> {t("routing.addRule")}
        </button>
        {full && <small className="run-hint">{t("routing.full", { max: MAX_ROUTE_RULES })}</small>}
      </div>

      {attempted && problemCount > 0 && (
        <div className="notify-message error" role="alert">
          {t("routing.fixProblems", { count: problemCount })}
        </div>
      )}
      {refusal && (
        <div className="notify-message error" role="alert">
          {refusal}
        </div>
      )}
      <div className="budget-actions">
        {justSaved && !dirty && (
          <span className="budget-saved" role="status">
            {t("budget.saved")}
          </span>
        )}
        {dirty && (
          <button
            type="button"
            className="routing-discard"
            onClick={() => {
              setDraft(null);
              setAttempted(false);
              useRouting.getState().clearSaveError();
            }}
          >
            {t("routing.discard")}
          </button>
        )}
        <button type="submit" className="primary" disabled={!dirty || saving}>
          {saving ? t("budget.saving") : t("routing.save")}
        </button>
      </div>
    </form>
  );
}

