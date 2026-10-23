import { RUN_MODE_INTERACTIVE, type Runner } from "../api";
import { useI18n } from "../i18n";

/** What the dispatch dialog offers; "headless" is sent as no mode at all. */
export type DispatchMode = "headless" | "interactive";

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

/** Headless or interactive, for the dispatch dialog. */
export function RunModePicker({
  runner,
  value,
  onChange,
}: {
  runner: Runner | undefined;
  value: DispatchMode;
  onChange: (mode: DispatchMode) => void;
}) {
  const { t } = useI18n();
  const supported = runnerSupportsInteractive(runner);
  const option = (mode: DispatchMode, label: string, disabled = false) => (
    <label className={`run-mode-option${value === mode ? " selected" : ""}${disabled ? " disabled" : ""}`}>
      <input
        type="radio"
        name="run-mode"
        value={mode}
        checked={value === mode}
        disabled={disabled}
        onChange={() => onChange(mode)}
      />
      <span>{label}</span>
    </label>
  );
  return (
    <fieldset className="run-mode-field">
      <legend>{t("terminal.mode")}</legend>
      <div className="run-mode-options">
        {option("headless", t("terminal.modeHeadless"))}
        {option("interactive", t("terminal.modeInteractive"), !supported)}
      </div>
      {!supported ? (
        <p className="run-hint">{t("terminal.modeUnsupported")}</p>
      ) : (
        value === "interactive" && <p className="run-hint">{t("terminal.modeNote")}</p>
      )}
    </fieldset>
  );
}
