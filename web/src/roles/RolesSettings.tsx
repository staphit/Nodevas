// Settings › Agents › Advanced › Roles: who runs each role (yours: agent,
// model, fallbacks, concurrency, budget) and what it may do (this project:
// tools and node types), plus custom roles.
//
// Who runs a role is the viewer's: a routing rule matching the role, kept
// with the other routing rules on the hub. What a role may do is the
// project's (graph.yaml agent_roles), the same for every collaborator's
// agents; the hub enforces it and refuses an agent's change to it.

import { useEffect, useId, useMemo, useState } from "react";

import { api, type AgentModel, type RoleSettings, type RouteRule } from "../api";
import { useViewer } from "../components/SignIn";
import { resolveNodeTypes } from "../domain/graph/nodeTypes";
import {
  BUILTIN_AGENT_ROLES,
  capsForTools,
  isBuiltinRole,
  nextCustomRoleID,
  resolveAgentRoles,
  ROLE_TOOLS,
  toolsetTokens,
  toolTokenLabel,
} from "../domain/graph/roles";
import { useI18n } from "../i18n";
import { nodeTypeLabel } from "../nodeTypeTheme";
import { useRunners } from "../runners/runnerStore";
import { agentCatalog, agentModelProblem, agentModelText, catalogDefaultModel, FallbackChainEditor, trimAgentModel } from "../runs/AgentModelFields";
import { formatCompact } from "../runs/runFormat";
import { newRuleId, rulesPayload } from "../runs/RoutingSettings";
import { useRouting } from "../runs/routingStore";
import { useApp } from "../store";
import type { AgentRole, AgentRoleDef } from "../types";
import { roleName } from "./roleLabels";

type Translate = (key: string, values?: Record<string, string | number>) => string;

const NO_DEFS: AgentRoleDef[] = [];

/** Roles whose work checks the Executor's: they should use another vendor. */
const CHECKERS = new Set(["reviewer", "qa"]);

/** The viewer's rule for a role: this project's first, else the global one. */
export function roleRule(rules: readonly RouteRule[] | null, role: string, project: string): RouteRule | undefined {
  const matching = (rules ?? []).filter((rule) => rule.match === "role" && rule.value === role);
  return matching.find((rule) => project && rule.project === project) ?? matching.find((rule) => rule.project === "");
}

/** Whether a checking role runs on the Executor's vendor. */
export function sameVendorAsExecutor(rules: readonly RouteRule[] | null, role: AgentRole, project: string): boolean {
  const slot = role.builtin ? role.id : (role.basedOn ?? "");
  if (!CHECKERS.has(slot)) return false;
  const mine = roleRule(rules, role.id, project)?.target.agent;
  const executor = roleRule(rules, "executor", project)?.target.agent;
  return Boolean(mine && executor && mine === executor);
}

/** The sparse stored list with one role's definition replaced. A built-in
 * equal to its default leaves the list. */
export function withRoleDef(defs: readonly AgentRoleDef[], next: AgentRoleDef): AgentRoleDef[] {
  let def: AgentRoleDef | null = { ...next };
  const builtin = BUILTIN_AGENT_ROLES.find((role) => role.id === next.id);
  if (builtin) {
    const same = (a: readonly string[] | undefined, b: readonly string[]) =>
      a === undefined || (a.length === b.length && a.every((item) => b.includes(item)));
    if (same(def.tools, builtin.tools)) delete def.tools;
    if (same(def.writeTypes, builtin.writeTypes)) delete def.writeTypes;
    if (same(def.acceptanceTypes, builtin.acceptanceTypes)) delete def.acceptanceTypes;
    if (!def.tools && !def.writeTypes && !def.acceptanceTypes) def = null;
  }
  const rest = defs.filter((held) => held.id !== next.id);
  if (!def) return rest;
  const index = defs.findIndex((held) => held.id === next.id);
  if (index < 0) return [...rest, def];
  return [...defs.slice(0, index), def, ...defs.slice(index + 1)];
}

function defOf(defs: readonly AgentRoleDef[], role: AgentRole): AgentRoleDef {
  return defs.find((def) => def.id === role.id) ?? { id: role.id };
}

async function saveRoles(agentRoles: AgentRoleDef[]): Promise<string> {
  const result = await useApp.getState().runGraphCommand({ type: "workflow.setAgentRoles", agentRoles });
  return result.ok ? "" : result.message;
}

/** "7 tools · ~1.6k tok" */
function toolsSummary(tools: readonly string[], t: Translate): string {
  return t("roles.toolsSummary", { count: tools.length, tokens: `~${(toolsetTokens(tools) / 1000).toFixed(1)}k` });
}

function NumberField({
  label,
  value,
  step = 1,
  onChange,
}: {
  label: string;
  value: number;
  step?: number;
  onChange: (value: number) => void;
}) {
  const id = useId();
  return (
    <div className="run-field">
      <label className="run-field-label" htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        type="number"
        min={0}
        step={step}
        value={value}
        onChange={(event) => {
          const next = Number(event.target.value);
          onChange(Number.isFinite(next) && next > 0 ? next : 0);
        }}
      />
    </div>
  );
}

/** Who runs a role: the viewer's routing rule and role limits. */
function WhoRunsIt({ role, project }: { role: AgentRole; project: string }) {
  const { t } = useI18n();
  const viewer = useViewer();
  const allRunners = useRunners((s) => s.runners);
  const rules = useRouting((s) => s.rules);
  const saving = useRouting((s) => s.saving);
  const runners = useMemo(
    () => (viewer ? allRunners.filter((runner) => runner.ownerId === viewer.id) : allRunners),
    [allRunners, viewer],
  );
  const existing = roleRule(rules, role.id, "");
  // A rule for this role in this project (made under Routing) wins here.
  const local = project ? roleRule(rules, role.id, project) : undefined;
  const shadowed = local && local.project === project ? local : undefined;
  const [target, setTarget] = useState<AgentModel>(existing?.target ?? { agent: "", model: "" });
  const [fallbacks, setFallbacks] = useState<AgentModel[]>(existing?.fallbacks ?? []);
  const [runnerId, setRunnerId] = useState(existing?.runnerId ?? "");
  const [limits, setLimits] = useState<RoleSettings | null>(null);
  const [allLimits, setAllLimits] = useState<RoleSettings[] | null>(null);
  const [limitsDirty, setLimitsDirty] = useState(false);
  const [message, setMessage] = useState("");
  const ids = { agent: useId(), model: useId(), runner: useId() };

  // The rule arrives after the first render when routing loads late.
  const existingKey = JSON.stringify(existing ?? null);
  useEffect(() => {
    setTarget(existing?.target ?? { agent: "", model: "" });
    setFallbacks(existing?.fallbacks ?? []);
    setRunnerId(existing?.runnerId ?? "");
  }, [existingKey]);

  useEffect(() => {
    let cancelled = false;
    api
      .getRoleSettings()
      .then((response) => {
        if (cancelled) return;
        const list = response?.roles ?? [];
        setAllLimits(list);
        setLimits(list.find((item) => item.role === role.id && item.project === "") ?? null);
      })
      .catch(() => {
        // An older hub has no role limits; the fields stay hidden.
      });
    return () => {
      cancelled = true;
    };
  }, [role.id]);

  const catalog = useMemo(() => agentCatalog(runnerId ? runners.filter((r) => r.id === runnerId) : runners), [runners, runnerId]);
  const agent = catalog.find((item) => item.kind === target.agent);
  const shownLimits: RoleSettings = limits ?? {
    role: role.id,
    project: "",
    maxConcurrent: 0,
    dailyCostUsd: 0,
    dailyTokens: 0,
    dailyRuns: 0,
  };
  const setLimit = (key: keyof Omit<RoleSettings, "role" | "project">) => (value: number) => {
    setLimits({ ...shownLimits, [key]: value });
    setLimitsDirty(true);
  };

  const save = async () => {
    setMessage("");
    const base = rules ?? [];
    const others = base.filter((rule) => !(rule.match === "role" && rule.value === role.id && rule.project === ""));
    let next = others;
    if (target.agent.trim()) {
      if (agentModelProblem(target) || fallbacks.some((item) => agentModelProblem(item))) {
        setMessage(t("roles.who.incomplete"));
        return;
      }
      const rule: RouteRule = {
        id: existing?.project === "" ? existing.id : newRuleId(),
        project: "",
        match: "role",
        value: role.id,
        target: trimAgentModel(target),
        runnerId,
        fallbacks: fallbacks.map(trimAgentModel),
        pipeline: null,
      };
      const index = base.findIndex((held) => held.id === existing?.id && held.project === "");
      next = index >= 0 ? base.map((held, i) => (i === index ? rule : held)) : [...others, rule];
    }
    const routed = await useRouting.getState().save(rulesPayload(next));
    if (!routed) return;
    if (limitsDirty && allLimits) {
      const rest = allLimits.filter((item) => !(item.role === role.id && item.project === ""));
      try {
        const response = await api.putRoleSettings([...rest, shownLimits]);
        setAllLimits(response?.roles ?? [...rest, shownLimits]);
        setLimitsDirty(false);
      } catch (error) {
        setMessage(error instanceof Error ? error.message : String(error));
        return;
      }
    }
    setMessage(t("budget.saved"));
  };

  return (
    <div className="role-who">
      <h4>{t("roles.who.title")}</h4>
      {shadowed && (
        <p className="run-hint">
          {t("roles.who.projectRule", { project, target: agentModelText(shadowed.target) })}
        </p>
      )}
      <div className="run-field-row">
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.agent}>
            {t("runs.agent")}
          </label>
          <select
            id={ids.agent}
            value={target.agent}
            onChange={(event) => {
              const next = catalog.find((item) => item.kind === event.target.value);
              setTarget({ agent: event.target.value, model: catalogDefaultModel(next) });
            }}
          >
            <option value="">{t("roles.who.routed")}</option>
            {catalog.map((item) => (
              <option key={item.kind} value={item.kind}>
                {item.kind}
              </option>
            ))}
            {target.agent && !agent && <option value={target.agent}>{target.agent}</option>}
          </select>
        </div>
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.model}>
            {t("runs.model")}
          </label>
          <select
            id={ids.model}
            value={target.model}
            disabled={!target.agent}
            onChange={(event) => setTarget({ ...target, model: event.target.value })}
          >
            {!target.model && <option value="">{t("routing.chooseModel")}</option>}
            {(agent?.models ?? []).map((model) => (
              <option key={model.id} value={model.id}>
                {(model.label || model.id) + (model.default ? t("runs.defaultSuffix") : "")}
              </option>
            ))}
            {target.model && !agent?.models.some((model) => model.id === target.model) && (
              <option value={target.model}>{target.model}</option>
            )}
          </select>
        </div>
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.runner}>
            {t("routing.runner")}
          </label>
          <select id={ids.runner} value={runnerId} onChange={(event) => setRunnerId(event.target.value)}>
            <option value="">{t("routing.anyRunner")}</option>
            {runners.map((runner) => (
              <option key={runner.id} value={runner.id}>
                {runner.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      <details className="route-sub" open={fallbacks.length > 0 || undefined}>
        <summary>{t("routing.fallbacks", { count: fallbacks.length })}</summary>
        <p className="run-hint">{t("roles.who.fallbackHint")}</p>
        <FallbackChainEditor value={fallbacks} catalog={catalog} onChange={setFallbacks} />
      </details>
      {allLimits && (
        <div className="run-field-row role-limits">
          <NumberField label={t("roles.who.maxConcurrent")} value={shownLimits.maxConcurrent} onChange={setLimit("maxConcurrent")} />
          <NumberField label={t("roles.who.dailyCost")} value={shownLimits.dailyCostUsd} step={0.5} onChange={setLimit("dailyCostUsd")} />
          <NumberField label={t("roles.who.dailyTokens")} value={shownLimits.dailyTokens} step={1000} onChange={setLimit("dailyTokens")} />
          <NumberField label={t("roles.who.dailyRuns")} value={shownLimits.dailyRuns} onChange={setLimit("dailyRuns")} />
        </div>
      )}
      <div className="budget-actions">
        {message && (
          <span className="budget-saved" role="status">
            {message}
          </span>
        )}
        <button type="button" className="primary" disabled={saving || rules === null} onClick={() => void save()}>
          {t("common.save")}
        </button>
      </div>
    </div>
  );
}

/** What a role may do in this project: its tools and node types. */
function WhatItMayDo({ role, onError }: { role: AgentRole; onError: (message: string) => void }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const defs = graph?.agentRoles ?? [];
  const types = resolveNodeTypes(graph).filter((type) => type.id !== "start" && type.id !== "end");

  const write = async (patch: Partial<AgentRoleDef>) => {
    onError(await saveRoles(withRoleDef(defs, { ...defOf(defs, role), ...patch })));
  };

  const toggleTool = (tool: string) => {
    const tools = role.tools.includes(tool) ? role.tools.filter((held) => held !== tool) : [...role.tools, tool];
    // Catalog order, so the stored list reads the same however it was built.
    void write({ tools: ROLE_TOOLS.filter((held) => tools.includes(held)) });
  };
  const toggleType = (type: string) => {
    const writeTypes = role.writeTypes.includes(type)
      ? role.writeTypes.filter((held) => held !== type)
      : [...role.writeTypes, type];
    void write({ writeTypes });
  };

  return (
    <div className="role-what">
      <h4>{t("roles.what.title")}</h4>
      <p className="run-hint">{t("roles.what.hint", { summary: toolsSummary(role.tools, t) })}</p>
      <fieldset className="role-tools">
        <legend>{t("roles.what.tools")}</legend>
        {ROLE_TOOLS.map((tool) => (
          <label key={tool} className="route-toggle role-tool">
            <input type="checkbox" checked={role.tools.includes(tool)} onChange={() => toggleTool(tool)} />
            <span>{`${tool} · ${toolTokenLabel(tool)}`}</span>
          </label>
        ))}
      </fieldset>
      <p className="run-hint">{t("roles.what.caps", { caps: capsForTools(role.tools).join(", ") || "—" })}</p>
      <fieldset className="role-tools">
        <legend>{t("roles.what.types")}</legend>
        {types.map((type) => (
          <label key={type.id} className="route-toggle role-tool">
            <input type="checkbox" checked={role.writeTypes.includes(type.id)} onChange={() => toggleType(type.id)} />
            <span>{nodeTypeLabel(type, t)}</span>
          </label>
        ))}
      </fieldset>
    </div>
  );
}

/** A custom role's own name, rank and project policy. */
function CustomRoleFields({ role, onError }: { role: AgentRole; onError: (message: string) => void }) {
  const { t } = useI18n();
  const defs = useApp((s) => s.graph?.agentRoles) ?? NO_DEFS;
  const [label, setLabel] = useState(role.label);
  const [instructions, setInstructions] = useState(role.instructions ?? "");
  const ids = { label: useId(), rank: useId(), instructions: useId() };
  const write = async (patch: Partial<AgentRoleDef>) => {
    onError(await saveRoles(withRoleDef(defs, { ...defOf(defs, role), ...patch })));
  };
  return (
    <div className="role-custom">
      <div className="run-field-row">
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.label}>
            {t("roles.custom.name")}
          </label>
          <input
            id={ids.label}
            value={label}
            maxLength={64}
            onChange={(event) => setLabel(event.target.value)}
            onBlur={() => label.trim() && label.trim() !== role.label && void write({ label: label.trim() })}
          />
        </div>
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.rank}>
            {t("roles.custom.rank")}
          </label>
          <select
            id={ids.rank}
            value={role.rank}
            onChange={(event) => void write({ rank: event.target.value as AgentRole["rank"] })}
          >
            <option value="worker">{t("roles.rank.worker")}</option>
            <option value="orchestrator">{t("roles.rank.orchestrator")}</option>
          </select>
        </div>
      </div>
      <div className="run-field">
        <label className="run-field-label" htmlFor={ids.instructions}>
          {t("roles.custom.instructions")}
        </label>
        <textarea
          id={ids.instructions}
          rows={3}
          maxLength={2048}
          value={instructions}
          onChange={(event) => setInstructions(event.target.value)}
          onBlur={() => instructions !== (role.instructions ?? "") && void write({ instructions: instructions || undefined })}
        />
      </div>
      <button
        type="button"
        className="run-link-button"
        onClick={() => {
          if (!window.confirm(t("roles.custom.removeConfirm", { name: role.label }))) return;
          void saveRoles(defs.filter((def) => def.id !== role.id)).then(onError);
        }}
      >
        {t("roles.custom.remove")}
      </button>
    </div>
  );
}

function RoleItem({ role, project }: { role: AgentRole; project: string }) {
  const { t } = useI18n();
  const rules = useRouting((s) => s.rules);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const bodyId = useId();
  const name = roleName(role, t);
  const rule = roleRule(rules, role.id, project);
  const vendor = sameVendorAsExecutor(rules, role, project);
  const base = role.builtin ? "" : BUILTIN_AGENT_ROLES.find((item) => item.id === role.basedOn);
  return (
    <li className={`role-item${open ? " is-open" : ""}`} aria-label={name}>
      <button
        type="button"
        className="settings-disclosure-toggle role-toggle"
        aria-expanded={open}
        aria-controls={open ? bodyId : undefined}
        onClick={() => setOpen(!open)}
      >
        <span className="settings-disclosure-label">
          {name}
          {base && <small className="role-based">{t("roles.basedOnValue", { role: roleName(base, t) })}</small>}
        </span>
        <span className="settings-disclosure-value">
          {rule ? agentModelText(rule.target) : t("roles.who.routedShort")} · {toolsSummary(role.tools, t)}
        </span>
        <span className="settings-disclosure-chevron" aria-hidden="true">
          ›
        </span>
      </button>
      {!open && <p className="role-job">{t(role.builtin ? `roles.job.${role.id}` : "roles.job.custom")}</p>}
      {vendor && (
        <p className="role-vendor" role="note">
          {t("roles.vendorWarning")}
        </p>
      )}
      {open && (
        <div id={bodyId} className="role-body">
          <p className="role-job">{t(role.builtin ? `roles.job.${role.id}` : "roles.job.custom")}</p>
          <WhoRunsIt role={role} project={project} />
          <WhatItMayDo role={role} onError={setError} />
          {!role.builtin && <CustomRoleFields role={role} onError={setError} />}
          {error && (
            <div className="notify-message error" role="alert">
              {error}
            </div>
          )}
        </div>
      )}
    </li>
  );
}

function AddRole({ onDone }: { onDone: () => void }) {
  const { t } = useI18n();
  const defs = useApp((s) => s.graph?.agentRoles) ?? NO_DEFS;
  const [label, setLabel] = useState("");
  const [basedOn, setBasedOn] = useState("executor");
  const [error, setError] = useState("");
  const ids = { label: useId(), basedOn: useId() };
  const create = async () => {
    const name = label.trim();
    if (!name) return;
    const base = BUILTIN_AGENT_ROLES.find((role) => role.id === basedOn)!;
    const def: AgentRoleDef = {
      id: nextCustomRoleID(defs),
      label: name,
      basedOn,
      rank: base.rank,
      tools: [...base.tools],
      writeTypes: [...base.writeTypes],
    };
    const refusal = await saveRoles([...defs, def]);
    if (refusal) setError(refusal);
    else onDone();
  };
  return (
    <form
      className="role-add"
      onSubmit={(event) => {
        event.preventDefault();
        void create();
      }}
    >
      <div className="run-field-row">
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.label}>
            {t("roles.custom.name")}
          </label>
          <input id={ids.label} value={label} maxLength={64} onChange={(event) => setLabel(event.target.value)} />
        </div>
        <div className="run-field">
          <label className="run-field-label" htmlFor={ids.basedOn}>
            {t("roles.custom.basedOn")}
          </label>
          <select id={ids.basedOn} value={basedOn} onChange={(event) => setBasedOn(event.target.value)}>
            {BUILTIN_AGENT_ROLES.map((role) => (
              <option key={role.id} value={role.id}>
                {roleName(role, t)}
              </option>
            ))}
          </select>
        </div>
      </div>
      {error && (
        <div className="notify-message error" role="alert">
          {error}
        </div>
      )}
      <div className="budget-actions">
        <button type="button" onClick={onDone}>
          {t("common.cancel")}
        </button>
        <button type="submit" className="primary" disabled={!label.trim()}>
          {t("roles.custom.create")}
        </button>
      </div>
    </form>
  );
}

export function RolesSettings() {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const project = useApp((s) => s.activeProject) ?? "";
  const [adding, setAdding] = useState(false);
  const roles = resolveAgentRoles(graph);
  const custom = roles.filter((role) => !isBuiltinRole(role.id)).length;

  useEffect(() => {
    void useRouting.getState().load();
  }, []);

  return (
    <div className="roles-settings">
      <p className="runner-hint">{t("roles.description", { project: project || "—" })}</p>
      <ul className="role-list" aria-label={t("roles.settings.title")}>
        {roles.map((role) => (
          <RoleItem key={role.id} role={role} project={project} />
        ))}
      </ul>
      {adding ? (
        <AddRole onDone={() => setAdding(false)} />
      ) : (
        <button type="button" className="run-link-button" disabled={custom >= 32 || !graph} onClick={() => setAdding(true)}>
          {t("roles.custom.add")}
        </button>
      )}
      <p className="run-hint">{t("roles.costNote", { tokens: formatCompact(toolsetTokens(ROLE_TOOLS)) })}</p>
    </div>
  );
}
