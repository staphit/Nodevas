import { RUN_MODE_INTERACTIVE, type Actor, type Runner, type RunnerAgent } from "../api";
import { agentName } from "../game/labels";
import { useI18n } from "../i18n";
import { ModeCards } from "../station/pickers/ModeCards";
import { useApp } from "../store";
import { modeAllowed } from "./autoAccept";

/** What the dispatch dialog offers; "headless" is sent as no mode at all. */
export type DispatchMode = "headless" | "interactive";

/** Why defaultDispatchMode chose background instead of interactive (§8.1). */
export type ModeFallbackReason =
  | "capability"
  | "agent"
  | "untrusted"
  | "teammate"
  | "autoAccept"
  | "noTyping"
  | "staged"
  | "pipeline"
  | "group"
  | "contest";

/** Inputs for the single-source interactive-by-default rule (§8.1). */
export interface DefaultDispatchModeContext {
  runner: Runner | undefined;
  agent: RunnerAgent | undefined;
  viewer?: Actor | null;
  edition?: "single" | "team";
  staged?: boolean;
  pipeline?: boolean;
  fallbacks?: boolean;
  group?: boolean;
  contest?: boolean;
}

export interface DefaultDispatchModeResult {
  mode: DispatchMode;
  reason?: ModeFallbackReason;
}

/** Whether a runner can run an agent's TUI in a PTY (runnerwire.CapInteractive). */
export function runnerSupportsInteractive(runner: Runner | undefined): boolean {
  return Boolean(runner?.info?.capabilities?.includes(RUN_MODE_INTERACTIVE));
}

/** The mode a dispatch will use: the pick, unless the runner cannot do it. */
export function effectiveDispatchMode(
  choice: DispatchMode,
  runner: Runner | undefined,
): DispatchMode {
  return choice === "interactive" && runnerSupportsInteractive(runner) ? "interactive" : "headless";
}

/**
 * Pure single-source decision rule for whether a new run defaults to
 * interactive or background (§8.1).
 */
export function defaultDispatchMode(ctx: DefaultDispatchModeContext): DefaultDispatchModeResult {
  if (ctx.contest) return { mode: "headless", reason: "contest" };
  if (ctx.group) return { mode: "headless", reason: "group" };
  if (!runnerSupportsInteractive(ctx.runner)) return { mode: "headless", reason: "capability" };
  if (!ctx.agent?.interactive) return { mode: "headless", reason: "agent" };
  if (ctx.agent.untrusted) return { mode: "headless", reason: "untrusted" };
  if (
    ctx.edition !== "single" &&
    ctx.viewer &&
    ctx.runner?.ownerId &&
    ctx.runner.ownerId !== ctx.viewer.id
  ) {
    return { mode: "headless", reason: "teammate" };
  }
  const policy = ctx.runner?.info?.autoAccept;
  if (policy?.enabled) {
    if (!modeAllowed(policy, RUN_MODE_INTERACTIVE)) {
      return { mode: "headless", reason: "autoAccept" };
    }
    if (!policy.allowRemoteTyping) {
      return { mode: "headless", reason: "noTyping" };
    }
  }
  if (ctx.staged) return { mode: "headless", reason: "staged" };
  if (ctx.pipeline || ctx.fallbacks) return { mode: "headless", reason: "pipeline" };
  return { mode: "interactive" };
}

/**
 * The §8.1 reasons no pick or saved preference can override: the run could
 * not work interactively (no capability, an agent without a TUI, a folder its
 * CLI has not trusted, auto-accept with nobody able to type) or the hub runs
 * it in the background anyway (a teammate's runner, a staged node, a
 * pipeline or fallbacks, a group, a contest). Only "auto-accept covers
 * background only" stays soft: a person may still pick interactive and
 * answer the prompts themselves.
 */
export function hardBackgroundReason(ctx: DefaultDispatchModeContext): ModeFallbackReason | undefined {
  if (ctx.contest) return "contest";
  if (ctx.group) return "group";
  if (!runnerSupportsInteractive(ctx.runner)) return "capability";
  // The same test as the default rule: an agent the runner does not list
  // (uninstalled since) or does not say can run on screen is a hard reason.
  // The wire omits interactive:false, so "=== false" would never match.
  if (!ctx.agent?.interactive) return "agent";
  if (ctx.agent.untrusted) return "untrusted";
  if (
    ctx.edition !== "single" &&
    ctx.viewer &&
    ctx.runner?.ownerId &&
    ctx.runner.ownerId !== ctx.viewer.id
  ) {
    return "teammate";
  }
  const policy = ctx.runner?.info?.autoAccept;
  if (policy?.enabled && modeAllowed(policy, RUN_MODE_INTERACTIVE) && !policy.allowRemoteTyping) {
    return "noTyping";
  }
  if (ctx.staged) return "staged";
  if (ctx.pipeline || ctx.fallbacks) return "pipeline";
  return undefined;
}

/** Whether interactive mode is hard-blocked and its card must be disabled. */
export function isInteractiveDisabled(ctx: DefaultDispatchModeContext): boolean {
  return hardBackgroundReason(ctx) !== undefined;
}

/** Resolves the active dispatch mode from explicit click, saved runMode
 * preference, and default rule. A hard §8.1 reason always wins. */
export function resolveDispatchMode(
  choice: DispatchMode | null,
  pref: "auto" | "interactive" | "background",
  def: DefaultDispatchModeResult,
  ctx: DefaultDispatchModeContext,
): DispatchMode {
  if (isInteractiveDisabled(ctx)) return "headless";
  if (choice !== null) return choice;
  if (pref === "background") return "headless";
  if (pref === "interactive") return "interactive";
  return def.mode;
}

/**
 * The mode a follow-up or a "new work" start sends (§8.3): the same rule as a
 * first start, read for the prior run's runner and agent, with the saved
 * preference; a queued reply is always background, so it sends no mode.
 * Returns the request's `mode` value ("" for background).
 */
export function followUpMode(
  ctx: DefaultDispatchModeContext,
  pref: "auto" | "interactive" | "background" = "auto",
  queued = false,
): "" | "interactive" {
  if (queued) return "";
  const mode = resolveDispatchMode(null, pref, defaultDispatchMode(ctx), ctx);
  return mode === "interactive" ? "interactive" : "";
}

/** Interactive vs Background mode picture cards for dispatch and global-agent forms. */
export function RunModePicker({
  runner,
  agent,
  model = "default",
  value,
  onChange,
  fallbackReason,
  interactiveDisabled,
  simple = false,
  remember = true,
  invalid,
  describedBy,
}: {
  runner: Runner | undefined;
  agent?: RunnerAgent | undefined;
  model?: string;
  value: DispatchMode;
  onChange: (mode: DispatchMode) => void;
  fallbackReason?: ModeFallbackReason;
  interactiveDisabled?: boolean;
  simple?: boolean;
  remember?: boolean;
  invalid?: boolean;
  describedBy?: string;
}) {
  const { t } = useI18n();
  const supported = runnerSupportsInteractive(runner);
  const local = Boolean(runner?.builtIn);
  const disabledInteractive = interactiveDisabled ?? !supported;

  const agentLabel = agentName(agent?.kind ?? "");
  let note: string | undefined;
  if (fallbackReason === "group") {
    note = t("picker.mode.group");
  } else if (!supported) {
    note = t(local ? "terminal.modeUnsupportedLocal" : "terminal.modeUnsupported");
  } else if (value === "interactive") {
    note = t(local ? "terminal.modeNoteLocal" : "terminal.modeNote");
  } else if (fallbackReason) {
    switch (fallbackReason) {
      case "agent":
        note = t("picker.mode.fallback.agent", { agent: agentLabel });
        break;
      case "untrusted":
        note = t(
          simple ? "picker.simple.fallback.untrusted" : "picker.mode.fallback.untrusted",
          { agent: agentLabel },
        );
        break;
      case "teammate":
        note = t("picker.mode.fallback.teammate");
        break;
      case "autoAccept":
        note = t("picker.mode.fallback.autoAccept");
        break;
      case "noTyping":
        note = t("picker.mode.fallback.noTyping");
        break;
      case "staged":
        note = t("picker.mode.fallback.staged");
        break;
      case "pipeline":
        note = t("picker.mode.fallback.pipeline");
        break;
      case "capability":
        note = t(local ? "terminal.modeUnsupportedLocal" : "terminal.modeUnsupported");
        break;
      case "contest":
        break;
    }
  }

  const handleChange = (next: DispatchMode) => {
    if (remember) {
      useApp
        .getState()
        .updateUIPreference("runMode", next === "interactive" ? "interactive" : "background");
    }
    onChange(next);
  };

  return (
    <ModeCards
      agent={agent?.kind ?? ""}
      model={model || "default"}
      value={value}
      onChange={handleChange}
      interactiveDisabled={disabledInteractive}
      note={note}
      invalid={invalid}
      describedBy={describedBy}
    />
  );
}
