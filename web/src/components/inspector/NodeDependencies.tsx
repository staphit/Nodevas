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
 *
 * References sit between the two: the Notes (character sheets, setting) this
 * node is written against. They never block anything, so they are listed
 * apart from what the node waits for and leave every count above alone.
 */

import { useEffect, useMemo, useState } from "react";

import { edgeRelation } from "../../domain/graph/edgeStyle";
import { nodesWaitingOn } from "../../domain/graph/logicGate";
import { nodeTypeOf } from "../../domain/graph/nodeTypes";
import { requiresNodeRefs } from "../../domain/graph/requires";
import { IconClose, IconPlus } from "../../icons";
import { localizedRelationLabel, useI18n } from "../../i18n";
import { NodeTypeChip } from "../../nodeTypeTheme";
import { reportError, useApp } from "../../store";
import type { EdgeRelation, Graph, GraphNode, LogicGate } from "../../types";
import { withRequiresProjection } from "../canvas/requiresProjection";
import { advancedConditionKind, hasAdvancedCondition } from "../canvas/simpleDependency";
import { useGraphWiring } from "../canvas/useGraphWiring";
import { useNodeConditions } from "../canvas/useNodeConditions";
import { NodeLinkSection } from "./NodeDocumentLinks";

const NO_GATES: LogicGate[] = [];

export function NodeDependencies({ id }: { id: string }) {
  const { t, language } = useI18n();
  const graph = useApp((state) => state.graph);
  const updateNode = useApp((state) => state.updateNode);
  const updateCanvasLayout = useApp((state) => state.updateCanvasLayout);
  const openTab = useApp((state) => state.openTab);
  const openNodeLink = useApp((state) => state.openNodeLink);
  const activeProject = useApp((state) => state.activeProject);
  // graph.yaml cannot be read: the hub refuses every edit here, so the tab
  // only reads. Opening a related node still works.
  const readOnly = useApp((state) => state.graphUnreadable !== null);
  const logicGates = graph?.ui?.logicGates ?? NO_GATES;
  const [notice, setNotice] = useState<{ text: string; kind: "ok" | "error" } | null>(null);
  // Added but not yet answered: the server materialises a dependency before
  // the board shows it, and the picker must not offer it again meanwhile.
  const [adding, setAdding] = useState<ReadonlySet<string>>(() => new Set());
  useEffect(() => {
    setNotice(null);
    setAdding(new Set());
  }, [id]);

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

  // Not on offer: a node already wired in, one the expression names without
  // a wire yet, one just added whose answer is still on its way, and one that
  // already waits on this node (directly or further down), which would close
  // a loop the add is refused for. "Waits on" is read the way the hub reads
  // it: the wires plus every node a `requires` expression names, drawn or not.
  const targetRequires = conditionTarget?.requires;
  const edges = withRequiresProjection(graph)?.edges;
  const choices = useMemo(() => {
    const named = new Set(requiresNodeRefs(targetRequires));
    const downstream = nodesWaitingOn(edges ?? [], id);
    return availableConditionSources.filter(
      (node) => !named.has(node.id) && !adding.has(node.id) && !downstream.has(node.id),
    );
  }, [availableConditionSources, targetRequires, adding, edges, id]);

  // Adding a prerequisite removes it from the choices; the picker moves on to
  // the next one rather than pointing at a node it no longer offers.
  useEffect(() => {
    if (choices.some((node) => node.id === conditionSource)) return;
    setConditionSource(choices[0]?.id ?? "");
  }, [choices, conditionSource, setConditionSource]);

  const addDependency = () => {
    const sourceId = conditionSource;
    if (!sourceId || sourceId === id) return;
    setAdding((current) => new Set(current).add(sourceId));
    const settle = () =>
      setAdding((current) => {
        const next = new Set(current);
        next.delete(sourceId);
        return next;
      });
    void updateNode({
      type: "node.addDependency",
      sourceId,
      targetId: id,
      operator: conditionOperator,
      relation: conditionRelation,
    })
      .then((result) => {
        if (!result.ok) setNotice({ text: result.message, kind: "error" });
      })
      .catch(reportError)
      .finally(settle);
  };

  const outgoing = (edges ?? []).filter((edge) => edge.from === id);
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
                    disabled={readOnly}
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
              addDependency();
            }}
          >
            <select
              value={conditionSource}
              aria-label={t("drawer.deps.pick")}
              disabled={readOnly || choices.length === 0}
              onChange={(event) => setConditionSource(event.target.value)}
            >
              {choices.length === 0 ? (
                <option value="">{t("nodeMenu.noSources")}</option>
              ) : (
                choices.map((node) => (
                  <option key={node.id} value={node.id}>
                    {node.title || node.id}
                  </option>
                ))
              )}
            </select>
            <button type="submit" disabled={readOnly || !conditionSource}>
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

      <ReferencesSection
        graph={graph}
        id={id}
        readOnly={readOnly}
        onOpen={open}
        onError={(text) => setNotice({ text, kind: "error" })}
      />

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
                disabled={readOnly}
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
                    disabled={readOnly}
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
                        disabled={readOnly}
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
                  disabled={readOnly}
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

const byTitle = (a: GraphNode, b: GraphNode) =>
  (a.title || a.id).localeCompare(b.title || b.id);

/**
 * The Notes this node is written against [references]. Stored on this node,
 * so adding or removing one is an edit of this node alone; the note itself is
 * untouched and nothing waits on anything.
 */
function ReferencesSection({
  graph,
  id,
  readOnly,
  onOpen,
  onError,
}: {
  graph: Graph | null;
  id: string;
  readOnly: boolean;
  onOpen: (nodeId: string) => void;
  onError: (message: string) => void;
}) {
  const { t } = useI18n();
  const updateNode = useApp((state) => state.updateNode);
  const nodes = graph?.nodes ?? [];
  const refs = nodes.find((node) => node.id === id)?.refs ?? [];
  const byId = new Map(nodes.map((node) => [node.id, node]));

  // Notes first — they are what a reference is almost always for — then
  // everything else, for the odd scene that leans on another.
  const { notes, others } = useMemo(() => {
    const taken = new Set(graph?.nodes?.find((node) => node.id === id)?.refs ?? []);
    const candidates = (graph?.nodes ?? []).filter(
      (node) => node.id !== id && !taken.has(node.id),
    );
    const isNote = (node: GraphNode) => nodeTypeOf(graph, node).completion === "none";
    return {
      notes: candidates.filter(isNote).sort(byTitle),
      others: candidates.filter((node) => !isNote(node)),
    };
  }, [graph, id]);
  const firstChoice = notes[0]?.id ?? others[0]?.id ?? "";
  const [pick, setPick] = useState(firstChoice);
  useEffect(() => {
    if ([...notes, ...others].some((node) => node.id === pick)) return;
    setPick(firstChoice);
  }, [notes, others, pick, firstChoice]);

  const run = (command: { type: "node.addRef" | "node.removeRef"; ref: string }) =>
    void updateNode({ type: command.type, nodeId: id, ref: command.ref }).then((result) => {
      if (!result.ok) onError(result.message);
    });

  return (
    <section className="node-relations-section node-references">
      <h3>
        {t("relations.references")}
        <span>{refs.length}</span>
      </h3>
      <p className="node-dependencies-hint">{t("drawer.refs.hint")}</p>
      {refs.length === 0 ? (
        <div className="node-relations-empty">{t("drawer.refs.none")}</div>
      ) : (
        <ul className="node-dependency-list" aria-label={t("relations.references")}>
          {refs.map((ref) => {
            const target = byId.get(ref);
            const title = target ? target.title || target.id : ref;
            return (
              <li key={ref}>
                {target ? (
                  <button type="button" className="node-dependency-open" onClick={() => onOpen(ref)}>
                    {title}
                  </button>
                ) : (
                  <span className="node-dependency-open node-reference-missing">
                    {t("drawer.refs.missing", { id: ref })}
                  </span>
                )}
                {target && <NodeTypeChip type={nodeTypeOf(graph, target)} />}
                <button
                  type="button"
                  className="node-dependency-remove"
                  aria-label={t("drawer.refs.remove", { title })}
                  title={t("drawer.refs.remove", { title })}
                  disabled={readOnly}
                  onClick={() => run({ type: "node.removeRef", ref })}
                >
                  <IconClose size={12} />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      <form
        className="node-dependency-add"
        onSubmit={(event) => {
          event.preventDefault();
          if (pick) run({ type: "node.addRef", ref: pick });
        }}
      >
        <select
          value={pick}
          aria-label={t("drawer.refs.pick")}
          disabled={readOnly || !firstChoice}
          onChange={(event) => setPick(event.target.value)}
        >
          {!firstChoice && <option value="">{t("nodeMenu.noSources")}</option>}
          {notes.length > 0 && (
            <optgroup label={t("drawer.refs.groupNotes")}>
              {notes.map((node) => (
                <option key={node.id} value={node.id}>
                  {node.title || node.id}
                </option>
              ))}
            </optgroup>
          )}
          {others.length > 0 && (
            <optgroup label={t("drawer.refs.groupOther")}>
              {others.map((node) => (
                <option key={node.id} value={node.id}>
                  {node.title || node.id}
                </option>
              ))}
            </optgroup>
          )}
        </select>
        <button type="submit" disabled={readOnly || !pick}>
          <IconPlus size={13} />
          {t("drawer.refs.add")}
        </button>
      </form>
    </section>
  );
}
