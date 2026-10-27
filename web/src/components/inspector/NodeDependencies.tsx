/**
 * Dependencies tab [B-06]: what this node waits for, what it blocks, and how
 * its conditions combine.
 *
 * The plain case — "starts when these are done" — is the whole default view:
 * the prerequisites with a remove button each, a picker to add one, and the
 * nodes this one blocks. Everything beyond waiting for all of them (OR/XOR/
 * NAND/NOR, optional or deprecated relations, standalone logic gates) sits
 * under "Advanced conditions", which opens by itself whenever the node already
 * uses one of them, so a stored condition is never tucked out of sight.
 *
 * The edits are the board's: the same hooks its node menu uses, driven with
 * this node instead of a menu position, so both places obey the same rules.
 */

import { useEffect, useMemo, useState } from "react";

import { api } from "../../api";
import { edgeRelation } from "../../domain/graph/edgeStyle";
import { IconClose, IconPlus } from "../../icons";
import { localizedRelationLabel, useI18n } from "../../i18n";
import { reportError, useApp } from "../../store";
import type { EdgeRelation, LogicGate } from "../../types";
import { advancedConditionKind, hasAdvancedCondition } from "../canvas/simpleDependency";
import { useGraphWiring } from "../canvas/useGraphWiring";
import { useNodeConditions } from "../canvas/useNodeConditions";

const NO_GATES: LogicGate[] = [];

export function NodeDependencies({ id }: { id: string }) {
  const { t, language } = useI18n();
  const graph = useApp((state) => state.graph);
  const updateNode = useApp((state) => state.updateNode);
  const updateCanvasLayout = useApp((state) => state.updateCanvasLayout);
  const openTab = useApp((state) => state.openTab);
  const openNodeLink = useApp((state) => state.openNodeLink);
  const activeProject = useApp((state) => state.activeProject);
  const logicGates = graph?.ui?.logicGates ?? NO_GATES;
  const [notice, setNotice] = useState<{ text: string; kind: "ok" | "error" } | null>(null);
  useEffect(() => setNotice(null), [id]);

  const nodeTitle = (nodeId: string | undefined) =>
    (nodeId && graph?.nodes?.find((node) => node.id === nodeId)?.title) || nodeId || "";

  const runCanvasCommand = (command: Parameters<typeof updateCanvasLayout>[0]) => {
    void updateCanvasLayout(command).then((result) => {
      if (!result.ok) setNotice({ text: result.message, kind: "error" });
    });
  };

  // The board's menu hooks, pointed at this node. The menu they expect is
  // only read for which node it is about; its position is never used here.
  const menu = useMemo(() => ({ kind: "node" as const, nodeId: id, x: 0, y: 0 }), [id]);
  const conditions = useNodeConditions({
    graph,
    logicGates,
    contextMenu: menu,
    setContextMenu: () => undefined,
    setGraphSelection: () => undefined,
    setGraphNotice: setNotice,
    updateNode,
    updateCanvasLayout,
    runCanvasCommand,
  });
  const wiring = useGraphWiring({
    graph,
    nodeTitle,
    selectNode: () => undefined,
    setGraphNotice: setNotice,
    setGraphSelection: () => undefined,
    setContextMenu: () => undefined,
    updateNode,
    updateCanvasLayout,
    runCanvasCommand,
  });

  const {
    incomingConditions,
    availableConditionSources,
    conditionSource,
    setConditionSource,
    conditionOperator,
    conditionRelation,
    setConditionRelation,
    controllingLogicGate,
    assignableLogicGates,
    hasEditableGateOperator,
    conditionTarget,
  } = conditions;

  // Adding a prerequisite removes it from the choices; the picker moves on to
  // the next one rather than pointing at a node it no longer offers.
  useEffect(() => {
    if (availableConditionSources.some((node) => node.id === conditionSource)) return;
    setConditionSource(availableConditionSources[0]?.id ?? "");
  }, [availableConditionSources, conditionSource, setConditionSource]);

  const outgoing = (graph?.edges ?? []).filter((edge) => edge.from === id);
  const requires = conditionTarget?.requires?.trim() ?? "";
  const conditionKind = advancedConditionKind(requires);
  const advanced =
    Boolean(controllingLogicGate) || hasAdvancedCondition(requires, incomingConditions);
  const [advancedOpen, setAdvancedOpen] = useState(advanced);
  useEffect(() => {
    if (advanced) setAdvancedOpen(true);
  }, [advanced]);
  const advancedBadge = controllingLogicGate
    ? t(`logicGate.op.${controllingLogicGate.operator}`)
    : conditionKind === "custom"
      ? t("dependency.customCondition")
      : conditionKind
        ? t(`gate.label.${conditionKind}`)
        : incomingConditions.some((edge) => edgeRelation(edge) !== "")
          ? t("dependency.relationsChanged")
          : null;

  const removeDependency = (from: string) => runCanvasCommand({ type: "canvas.removeEdge", from, to: id });
  const open = (nodeId: string) => void openTab(nodeId).catch(reportError);

  return (
    <div className="node-dependencies" role="tabpanel" aria-label={t("drawer.dependencies")}>
      {notice && (
        <div className={`node-dependencies-notice ${notice.kind}`} role={notice.kind === "error" ? "alert" : "status"}>
          <span>{notice.text}</span>
          <button type="button" aria-label={t("common.close")} onClick={() => setNotice(null)}>
            <IconClose size={12} />
          </button>
        </div>
      )}

      <section className="node-relations-section">
        <h3>
          {t("relations.prerequisites")}
          <span>{incomingConditions.length}</span>
        </h3>
        <p className="node-dependencies-hint">{t("drawer.deps.hint")}</p>
        {controllingLogicGate ? (
          <div className="controlled-by-standalone-gate">
            <b>{t("nodeMenu.gateManaged")}</b>
            <span>
              {t(`logicGate.op.${controllingLogicGate.operator}`)} · {controllingLogicGate.id}
            </span>
            <small>{t("drawer.deps.gateManagedHint")}</small>
          </div>
        ) : incomingConditions.length === 0 ? (
          <div className="node-relations-empty">{t("drawer.deps.none")}</div>
        ) : (
          <ul className="node-dependency-list">
            {incomingConditions.map((edge) => {
              const relation = edgeRelation(edge);
              return (
                <li key={`${edge.from}->${edge.to}`}>
                  <button type="button" className="node-dependency-open" onClick={() => open(edge.from)}>
                    {nodeTitle(edge.from)}
                  </button>
                  {relation !== "" && (
                    <small className={`dependency-badge relation-${relation}`}>
                      {localizedRelationLabel(relation, language)}
                    </small>
                  )}
                  <button
                    type="button"
                    className="node-dependency-remove"
                    aria-label={t("drawer.deps.remove", { title: nodeTitle(edge.from) })}
                    title={t("drawer.deps.remove", { title: nodeTitle(edge.from) })}
                    onClick={() => removeDependency(edge.from)}
                  >
                    <IconClose size={12} />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {!controllingLogicGate && (
          <form
            className="node-dependency-add"
            onSubmit={(event) => {
              event.preventDefault();
              conditions.addDependency(id);
            }}
          >
            <select
              value={conditionSource}
              aria-label={t("drawer.deps.pick")}
              disabled={availableConditionSources.length === 0}
              onChange={(event) => setConditionSource(event.target.value)}
            >
              {availableConditionSources.length === 0 ? (
                <option value="">{t("nodeMenu.noSources")}</option>
              ) : (
                availableConditionSources.map((node) => (
                  <option key={node.id} value={node.id}>
                    {node.title || node.id}
                  </option>
                ))
              )}
            </select>
            <button type="submit" disabled={!conditionSource}>
              <IconPlus size={13} />
              {t("drawer.deps.add")}
            </button>
          </form>
        )}
      </section>

      <section className="node-relations-section">
        <h3>
          {t("relations.dependents")}
          <span>{outgoing.length}</span>
        </h3>
        {outgoing.length === 0 ? (
          <div className="node-relations-empty">{t("drawer.deps.blocksNone")}</div>
        ) : (
          <ul className="node-dependency-list">
            {outgoing.map((edge) => (
              <li key={`${edge.from}->${edge.to}`}>
                <button type="button" className="node-dependency-open" onClick={() => open(edge.to)}>
                  {nodeTitle(edge.to)}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <details
        className="dependency-advanced"
        open={advancedOpen}
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
      >
        <summary>
          <span>{t("dependency.advanced")}</span>
          {advancedBadge && <small className="dependency-badge">{advancedBadge}</small>}
        </summary>
        <div className="dependency-advanced-body">
          <p className="lane-context-field-help">{t("dependency.advancedHint")}</p>
          {assignableLogicGates.length > 0 && (
            <label className="lane-context-field">
              <span className="lane-context-label">{t("nodeMenu.existingGate")}</span>
              <select
                value={controllingLogicGate?.id ?? ""}
                onChange={(event) => wiring.assignExistingLogicGateToNode(id, event.target.value)}
              >
                <option value="">{t("nodeMenu.noStandaloneGate")}</option>
                {assignableLogicGates.map((gate) => (
                  <option key={gate.id} value={gate.id}>
                    {t(`logicGate.op.${gate.operator}`)} · {gate.id} · {t("logicGate.input")} {gate.inputs.length}
                  </option>
                ))}
              </select>
              <small className="lane-context-field-help">{t("nodeMenu.existingGateHint")}</small>
            </label>
          )}
          {!controllingLogicGate && (
            <>
              {conditionKind !== null && (
                <div className="lane-context-field">
                  <span className="lane-context-label">{t("dependency.expression")}</span>
                  <code className="dependency-expression">{requires}</code>
                </div>
              )}
              {(hasEditableGateOperator || incomingConditions.length > 0) && (
                <label className="lane-context-field">
                  <span className="lane-context-label">{t("nodeMenu.conditionMode")}</span>
                  <select
                    value={conditionOperator}
                    onChange={(event) => conditions.changeGateOperator(id, event.target.value)}
                  >
                    <option value="and">{t("nodeMenu.and")}</option>
                    <option value="or">{t("nodeMenu.or")}</option>
                    <option value="xor">{t("nodeMenu.xor")}</option>
                    <option value="nand">{t("nodeMenu.nand")}</option>
                    <option value="nor">{t("nodeMenu.nor")}</option>
                  </select>
                </label>
              )}
              {incomingConditions.length > 0 && (
                <div className="lane-context-summary">
                  <b>{t("nodeMenu.currentPrereqs")}</b>
                  <p>{t("nodeMenu.relationHint")}</p>
                  {incomingConditions.map((edge) => (
                    <label className="lane-context-relation" key={`${edge.from}->${edge.to}`}>
                      <span>{nodeTitle(edge.from)}</span>
                      <select
                        value={edgeRelation(edge)}
                        aria-label={t("nodeMenu.relationAria", { title: nodeTitle(edge.from) })}
                        onChange={(event) =>
                          conditions.setDependencyRelation(
                            id,
                            edge.from,
                            event.target.value as EdgeRelation,
                          )
                        }
                      >
                        {(["", "optional", "deprecated"] as EdgeRelation[]).map((relation) => (
                          <option key={relation} value={relation}>
                            {localizedRelationLabel(relation, language)}
                          </option>
                        ))}
                      </select>
                    </label>
                  ))}
                </div>
              )}
              <label className="lane-context-field">
                <span className="lane-context-label">{t("nodeMenu.newRelation")}</span>
                <select
                  value={conditionRelation}
                  onChange={(event) => setConditionRelation(event.target.value as EdgeRelation)}
                >
                  <option value="">{t("nodeMenu.required")}</option>
                  <option value="optional">{t("nodeMenu.optional")}</option>
                  <option value="deprecated">{t("nodeMenu.deprecated")}</option>
                </select>
              </label>
            </>
          )}
        </div>
      </details>

      <NodeLinkSection project={activeProject} nodeId={id} openNodeLink={openNodeLink} />
      <div className="node-relations-help">{t("relations.help")}</div>
    </div>
  );
}

/**
 * Document links, in both directions [B-04].
 *
 * These are references written in the markdown, not dependencies: they may
 * point into another project, and they never gate anything. A link whose
 * target is gone is listed as broken rather than quietly dropped.
 */
function NodeLinkSection({
  project,
  nodeId,
  openNodeLink,
}: {
  project: string;
  nodeId: string;
  openNodeLink: (target: { project: string; nodeId: string }) => Promise<void>;
}) {
  const { t } = useI18n();
  const [links, setLinks] = useState<{
    backlinks: { fromProject: string; fromNode: string; fromTitle?: string }[];
    outgoing: { toProject: string; toNode: string; label?: string; missing: boolean }[];
  } | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!project || !nodeId) return;
    let cancelled = false;
    setLinks(null);
    setFailed(false);
    void api
      .getNodeLinks(project, nodeId)
      .then((result) => {
        if (!cancelled) setLinks(result);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [project, nodeId]);

  const open = (target: { project: string; nodeId: string }) =>
    void openNodeLink(target).catch(reportError);

  return (
    <section className="node-relations-section">
      <h3>{t("relations.documentLinks")}</h3>
      {failed && <p className="node-relations-empty">{t("relations.readFailed")}</p>}
      {!failed && !links && <p className="node-relations-empty">{t("relations.loading")}</p>}
      {links && (
        <div className="node-relations-list">
          {links.outgoing.map((link) => (
            <button
              type="button"
              className="node-relation-item"
              key={`out-${link.toProject}/${link.toNode}`}
              onClick={() => open({ project: link.toProject, nodeId: link.toNode })}
            >
              <span className="node-relation-direction">{t("relations.linkOut")}</span>
              <span className="node-relation-copy">
                <b>{link.label || link.toNode}</b>
                <code>
                  {link.toProject === project
                    ? link.toNode
                    : `${link.toProject} / ${link.toNode}`}
                </code>
              </span>
              {link.missing && (
                <span className="node-relation-style relation-broken">{t("relations.broken")}</span>
              )}
            </button>
          ))}
          {links.backlinks.map((link) => (
            <button
              type="button"
              className="node-relation-item"
              key={`in-${link.fromProject}/${link.fromNode}`}
              onClick={() =>
                open({ project: link.fromProject, nodeId: link.fromNode })
              }
            >
              <span className="node-relation-direction">{t("relations.linkIn")}</span>
              <span className="node-relation-copy">
                <b>{link.fromTitle || link.fromNode}</b>
                <code>
                  {link.fromProject === project
                    ? link.fromNode
                    : `${link.fromProject} / ${link.fromNode}`}
                </code>
              </span>
            </button>
          ))}
          {links.outgoing.length === 0 && links.backlinks.length === 0 && (
            <p className="node-relations-empty">
              {t("relations.noLinks")}
            </p>
          )}
        </div>
      )}
    </section>
  );
}
