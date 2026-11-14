// Which edition the app is: single-user (`nodevas serve` on loopback with no
// accounts, or the desktop app in local mode) or a team hub with accounts.
// Every feature that touches runners writes the two paths separately: in the
// single-user edition the runner is part of the app, started by serve on this
// computer, and the word "runner" never reaches the screen; on a team hub
// each member's runner is their own PC's program.
//
// AuthGate decides it from GET /api/auth/status (mode "local" is the
// single-user edition) and provides it here. Outside AuthGate (component
// tests) it reads as a team hub, so nothing an existing test looks for moves.

import { createContext, createElement, useContext, type ReactNode } from "react";

import type { Runner } from "../api";
import { useRunners } from "./runnerStore";

export type Edition = "single" | "team";

const EditionContext = createContext<Edition>("team");

export function EditionProvider({ edition, children }: { edition: Edition; children?: ReactNode }) {
  return createElement(EditionContext.Provider, { value: edition }, children);
}

/** "single" on a loopback server without accounts, else "team". */
export function useEdition(): Edition {
  return useContext(EditionContext);
}

/** This computer's built-in runner among `runners`, by the hub's word. */
export function builtInRunner(runners: readonly Runner[]): Runner | undefined {
  return runners.find((runner) => runner.builtIn === true);
}

/** The built-in runner while the app is the single-user edition; undefined
 * on a team hub, or when the hub names none (it did not start, or the list
 * is not loaded yet). Loading the runner list is the caller's to ask for. */
export function useLocalRunner(): Runner | undefined {
  const single = useEdition() === "single";
  const runners = useRunners((s) => s.runners);
  return single ? builtInRunner(runners) : undefined;
}
