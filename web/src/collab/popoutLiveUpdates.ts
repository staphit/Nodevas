/**
 * The pop-out editor's own socket.
 *
 * The pop-out is a separate page with its own store, and it opened no socket:
 * a save from the main window, another person or an agent never reached it,
 * so it kept a revision that no longer existed and its next keystroke was
 * refused as a version conflict. It listens for the two events that concern
 * it — its document and the graph — the way the main window does, in the room
 * of the project it shows (node ids repeat across projects).
 */

import { useEffect, useState } from "react";

import { connectWS, type LiveConnection } from "../api";
import { reportError, useApp } from "../store";

export function usePopoutLiveUpdates(nodeID: string): void {
  const [live, setLive] = useState<LiveConnection | null>(null);

  useEffect(() => {
    if (!nodeID) return;
    const connection = connectWS((ev) => {
      switch (ev.type) {
        case "node-changed":
          if (ev.id) void useApp.getState().notifyDiskChange(ev.id).catch(reportError);
          break;
        case "graph-changed":
          void useApp.getState().loadAll().catch(reportError);
          break;
      }
    });
    setLive(connection);
    return () => {
      setLive(null);
      connection.close();
    };
  }, [nodeID]);

  const activeProject = useApp((state) => state.activeProject);
  useEffect(() => {
    if (live && activeProject) live.subscribe(activeProject);
  }, [live, activeProject]);
}
