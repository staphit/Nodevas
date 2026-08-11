/**
 * Wire/vertex key formats [A-04].
 *
 * These strings are persisted in `graph.yaml → ui.wireVertices` and
 * `ui.edgeLabels`, so the formats are part of the file contract. Build them
 * here, never inline.
 */

/** Direct dependency edge, e.g. `a->b`. */
export function edgeWireKey(from: string, to: string): string {
  return `${from}->${to}`;
}

/** Inline dependency gate drawn in front of a target node. */
export function gateWireKey(targetId: string): string {
  return `gate:${targetId}`;
}

/** First-class logic gate: one wire per input. */
export function logicGateInputWireKey(gateId: string, sourceId: string): string {
  return `logic:${gateId}:in:${sourceId}`;
}

/**
 * First-class logic gate: one wire per output. A boolean gate has a single
 * output and keeps the unsuffixed key its bend points were saved under.
 */
export function logicGateOutputWireKey(gateId: string, targetId?: string): string {
  return targetId ? `logic:${gateId}:out:${targetId}` : `logic:${gateId}:out`;
}

/** Prefix of every wire belonging to one logic gate. */
export function logicGateWirePrefix(gateId: string): string {
  return `logic:${gateId}:`;
}

/** Inverse of {@link edgeWireKey}; `null` for gate/logic keys. */
export function parseEdgeWireKey(
  wireKey: string,
): { from: string; to: string } | null {
  if (wireKey.startsWith("gate:") || wireKey.startsWith("logic:")) return null;
  const separator = wireKey.indexOf("->");
  if (separator <= 0 || separator >= wireKey.length - 2) return null;
  return { from: wireKey.slice(0, separator), to: wireKey.slice(separator + 2) };
}
