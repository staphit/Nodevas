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

/** Whether `runner` is the one this hub started for itself. The runner list
 * says so from the moment the hub registers it (`hubLocal`); a runner the
 * settings and add-repo routes answer with says it only by `builtIn`. */
export function isHubRunner(runner: Runner): boolean {
  return runner.hubLocal === true || runner.builtIn === true;
}

/**
 * How the part of the app that runs agents is doing (single-user edition):
 * "ready" once the hub's own runner is connected and welcomed as built in,
 * "starting" while it is registered but has not connected since this hub
 * started (every start, for a few seconds), "stopped" for anything else: it
 * was up and dropped, it connected some other way, or there is none.
 */
export type LocalState = "ready" | "starting" | "stopped";

export function localState(runners: readonly Runner[]): LocalState {
  const own = runners.find(isHubRunner);
  if (own?.builtIn && own.online) return "ready";
  return own && !own.builtIn && !own.online ? "starting" : "stopped";
}

/** localState of the loaded runner list. Loading it is the caller's to ask for. */
export function useLocalState(): LocalState {
  return localState(useRunners((s) => s.runners));
}
