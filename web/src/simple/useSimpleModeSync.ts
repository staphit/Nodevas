/**
 * Simple mode keeps finished work on this computer. The runner's own
 * setting decides whether a run opens a pull request (a run cannot opt out
 * for itself), so while the preset is on, the built-in runner's `autoPR` is
 * turned off: one PUT /api/runners/:id/settings {autoPR: false} each time
 * the runner comes online with it on. Leaving Simple never turns it back
 * on, which would publish local work without a word; Settings says so.
 *
 * It only ever sends `autoPR: false`: no permission level, no relay. A
 * refusal is swallowed and not retried until the runner goes offline and
 * comes back. App mounts it once, inside the edition provider.
 */

import { useEffect, useRef } from "react";

import { api } from "../api";
import { holdRunner } from "../local/useAddRepo";
import { useLocalRunner } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useSimpleMode } from "./simpleMode";

export function useSimpleModeSync(): void {
  const simple = useSimpleMode();
  // Single edition only, and only the one this hub started for itself.
  const runner = useLocalRunner();
  const id = runner?.id ?? "";
  const online = Boolean(runner?.online);
  const autoPR = runner?.info?.local?.autoPR === true;
  // The runner this online stretch was already asked of ("" for none).
  const sentTo = useRef("");

  // The runner store loads only when something asks; the preset needs it.
  useEffect(() => {
    if (simple && !useRunners.getState().loaded) void useRunners.getState().refresh();
  }, [simple]);

  // Going offline, or leaving Simple, starts a new stretch.
  useEffect(() => {
    if (!online || !simple) sentTo.current = "";
  }, [online, simple]);

  useEffect(() => {
    if (!simple || !id || !online || !autoPR || sentTo.current === id) return;
    sentTo.current = id;
    void api
      .setRunnerSettings(id, { autoPR: false })
      .then((res) => holdRunner(res.runner))
      .catch(() => undefined);
  }, [simple, id, online, autoPR]);
}
