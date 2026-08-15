import { useRef, useState, type PointerEvent } from "react";
import type { Point } from "./geometry";

export interface ConnectionDragState {
  pointerId: number;
  sourceId: string;
  sourceGateId: string | null;
  start: Point;
  pointer: Point;
  targetId: string | null;
  targetGateId: string | null;
  originX: number;
  originY: number;
  moved: boolean;
}

export function useConnectionDrag({
  enabled,
  graphPointFromClient,
  connectionPointForNode,
  onSelectGate,
  connectNodes,
  connectNodeToLogicGate,
  connectLogicGateToNode,
}: {
  enabled: boolean;
  graphPointFromClient: (clientX: number, clientY: number) => Point | null;
  connectionPointForNode: (nodeId: string, toward: Point) => Point | null;
  onSelectGate: (gateId: string) => void;
  connectNodes: (sourceId: string, targetId: string) => void;
  connectNodeToLogicGate: (sourceId: string, gateId: string) => void;
  connectLogicGateToNode: (gateId: string, targetId: string) => void;
}) {
  const connectionRef = useRef<ConnectionDragState | null>(null);
  const [state, setState] = useState<ConnectionDragState | null>(null);
  const [hover, setHover] = useState<{ nodeId: string; point: Point } | null>(null);

  const update = (event: PointerEvent<Element>): boolean => {
    const current = connectionRef.current;
    if (!current || current.pointerId !== event.pointerId) return false;
    const pointer = graphPointFromClient(event.clientX, event.clientY);
    if (!pointer) return true;
    const hoveredElement = document.elementFromPoint(event.clientX, event.clientY);
    const hoveredNode = hoveredElement?.closest<HTMLElement>(".col-card[data-node-id]");
    const hoveredGate = hoveredElement?.closest<SVGGElement>(".logic-gate[data-gate-id]");
    const next = {
      ...current,
      pointer,
      targetId: hoveredNode?.dataset.nodeId ?? null,
      targetGateId: current.sourceGateId ? null : hoveredGate?.dataset.gateId ?? null,
      moved:
        current.moved ||
        Math.abs(event.clientX - current.originX) +
          Math.abs(event.clientY - current.originY) >
          5,
    };
    connectionRef.current = next;
    setState(next);
    return true;
  };

  const finish = (event: PointerEvent<Element>, commit: boolean): boolean => {
    const current = connectionRef.current;
    if (!current || current.pointerId !== event.pointerId) return false;
    update(event);
    const completed = connectionRef.current;
    connectionRef.current = null;
    setState(null);
    const target = event.currentTarget as HTMLElement;
    if (target.hasPointerCapture(event.pointerId)) {
      target.releasePointerCapture(event.pointerId);
    }
    if (commit && completed?.moved) {
      if (completed.sourceGateId && completed.targetId) {
        connectLogicGateToNode(completed.sourceGateId, completed.targetId);
      } else if (completed.sourceId && completed.targetGateId) {
        connectNodeToLogicGate(completed.sourceId, completed.targetGateId);
      } else if (completed.sourceId && completed.targetId) {
        connectNodes(completed.sourceId, completed.targetId);
      }
    }
    return true;
  };

  const startNode = (event: PointerEvent<HTMLElement>, nodeId: string): boolean => {
    if (!enabled || event.button !== 0 || !event.altKey) return false;
    const pointer = graphPointFromClient(event.clientX, event.clientY);
    const start = pointer ? connectionPointForNode(nodeId, pointer) : null;
    if (!pointer || !start) return false;
    event.preventDefault();
    event.stopPropagation();
    const next: ConnectionDragState = {
      pointerId: event.pointerId,
      sourceId: nodeId,
      sourceGateId: null,
      start,
      pointer,
      targetId: null,
      targetGateId: null,
      originX: event.clientX,
      originY: event.clientY,
      moved: false,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    connectionRef.current = next;
    setState(next);
    setHover(null);
    return true;
  };

  const startGate = (
    event: PointerEvent<SVGGElement>,
    gateId: string,
    start: Point,
  ): boolean => {
    if (!enabled || event.button !== 0 || !event.altKey) return false;
    const pointer = graphPointFromClient(event.clientX, event.clientY);
    if (!pointer) return false;
    event.preventDefault();
    event.stopPropagation();
    onSelectGate(gateId);
    const next: ConnectionDragState = {
      pointerId: event.pointerId,
      sourceId: "",
      sourceGateId: gateId,
      start,
      pointer,
      targetId: null,
      targetGateId: null,
      originX: event.clientX,
      originY: event.clientY,
      moved: false,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    connectionRef.current = next;
    setState(next);
    setHover(null);
    return true;
  };

  const onPointerMove = (event: PointerEvent<Element>): boolean => {
    if (update(event)) return true;
    if (!enabled) return false;
    const target = event.currentTarget as HTMLElement;
    if (event.altKey) {
      const nodeId = target.dataset.nodeId;
      const pointer = graphPointFromClient(event.clientX, event.clientY);
      const point = pointer && nodeId ? connectionPointForNode(nodeId, pointer) : null;
      setHover(nodeId && point ? { nodeId, point } : null);
    } else {
      setHover(null);
    }
    return false;
  };

  const cancel = () => {
    if (!connectionRef.current && !state && !hover) return false;
    connectionRef.current = null;
    setState(null);
    setHover(null);
    return true;
  };

  const clearHover = () => setHover(null);

  return {
    state,
    hover,
    handlers: {
      onPointerMove,
      onPointerUp: finish,
      onPointerCancel: (event: PointerEvent<Element>) => finish(event, false),
      startNode,
      startGate,
      cancel,
      clearHover,
    },
  };
}
