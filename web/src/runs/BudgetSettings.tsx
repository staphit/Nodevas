import { useEffect, useId, useState } from "react";
import type { RunBudget, RunBudgetStatus } from "../api";
import { useI18n } from "../i18n";
import { useBudget } from "./budgetStore";
import { formatCompact, formatCount } from "./runFormat";

type Translate = (key: string, values?: Record<string, string | number>) => string;

export interface BudgetDraft {
  runs: string;
  cost: string;
  tokens: string;
}

export type BudgetField = keyof BudgetDraft;

export function draftFrom(budget: RunBudget | undefined): BudgetDraft {
  return {
    runs: String(budget?.dailyRuns ?? 0),
    cost: String(budget?.dailyCostUsd ?? 0),
    tokens: String(budget?.dailyTokens ?? 0),
  };
}

const WHOLE = /^\d+$/;
const DECIMAL = /^\d+(\.\d{1,2})?$/;

/** Checks a draft the way the hub would: whole, non-negative counts and a
 * non-negative dollar amount with at most two decimals. Returns the budget,
 * or the first problem per field. */
export function parseBudgetDraft(
  draft: BudgetDraft,
): { budget: RunBudget; errors: null } | { budget: null; errors: Partial<Record<BudgetField, "whole" | "money">> } {
  const errors: Partial<Record<BudgetField, "whole" | "money">> = {};
  const runs = draft.runs.trim();
  const cost = draft.cost.trim();
  const tokens = draft.tokens.trim();
  if (!WHOLE.test(runs) || !Number.isSafeInteger(Number(runs))) errors.runs = "whole";
  if (!DECIMAL.test(cost) || !Number.isFinite(Number(cost))) errors.cost = "money";
  if (!WHOLE.test(tokens) || !Number.isSafeInteger(Number(tokens))) errors.tokens = "whole";
  if (Object.keys(errors).length > 0) return { budget: null, errors };
  return {
    budget: { dailyRuns: Number(runs), dailyCostUsd: Number(cost), dailyTokens: Number(tokens) },
    errors: null,
  };
}

function usd(value: number): string {
  return `$${(Number.isFinite(value) ? value : 0).toFixed(2)}`;
}

/** "Today's runs budget is used up" — or "" while nothing blocks a run. */
export function budgetExceededText(t: Translate, status: RunBudgetStatus | null): string {
  const limit = status?.exceeded;
  if (!limit) return "";
  const key = ["runs", "cost", "tokens"].includes(limit) ? `budget.limit.${limit}` : "";
  return t("budget.exceeded", { limit: key ? t(key) : limit });
}

function UsageMeter({
  label,
  used,
  limit,
  format,
}: {
  label: string;
  used: number;
  limit: number;
  format: (value: number) => string;
}) {
  const { t } = useI18n();
  const percent = limit > 0 ? Math.min(100, Math.round((used / limit) * 100)) : 0;
  const over = limit > 0 && used >= limit;
  return (
    <div className={`budget-meter${over ? " is-over" : ""}`}>
      <div className="budget-meter-head">
        <span>{label}</span>
        <span className="budget-meter-value">
          {limit > 0
            ? t("budget.usedOf", { used: format(used), limit: format(limit) })
            : t("budget.usedNoLimit", { used: format(used) })}
        </span>
      </div>
      {limit > 0 && (
        <div
          className="budget-bar"
          role="progressbar"
          aria-label={label}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percent}
        >
          <span style={{ width: `${percent}%` }} />
        </div>
      )}
    </div>
  );
}

/**
 * The viewer's daily limits on runs, estimated cost and tokens (0 = no limit),
 * with today's use (UTC day). The hub refuses new runs past a limit; cost is
 * the CLIs' own estimate, so the budget is a brake, not an invoice.
 */
export function BudgetSettings() {
  const { t } = useI18n();
  const status = useBudget((s) => s.status);
  const loading = useBudget((s) => s.loading);
  const error = useBudget((s) => s.error);
  const saving = useBudget((s) => s.saving);
  const saveError = useBudget((s) => s.saveError);
  const load = useBudget((s) => s.load);
  const save = useBudget((s) => s.save);
  const [draft, setDraft] = useState<BudgetDraft | null>(null);
  const [saved, setSaved] = useState(false);
  const ids = { runs: useId(), cost: useId(), tokens: useId() };

  useEffect(() => {
    void load();
  }, [load]);

  const current = draft ?? draftFrom(status?.budget);
  const parsed = parseBudgetDraft(current);
  const dirty =
    draft !== null &&
    (["runs", "cost", "tokens"] as const).some(
      (field) => draft[field].trim() !== draftFrom(status?.budget)[field],
    );

  const edit = (field: BudgetField, value: string) => {
    setSaved(false);
    setDraft({ ...current, [field]: value });
  };

  const submit = async () => {
    if (!parsed.budget || saving) return;
    if (await save(parsed.budget)) {
      setDraft(null);
      setSaved(true);
    }
  };

  if (!status) {
    return (
      <div className="budget-settings">
        {error ? (
          <div className="notify-message error" role="alert">
            {error}
          </div>
        ) : (
          <p className="runner-empty">{loading ? t("common.loading") : ""}</p>
        )}
      </div>
    );
  }

  const fieldError = (field: BudgetField) => {
    const problem = parsed.errors?.[field];
    return problem ? t(problem === "money" ? "budget.invalidMoney" : "budget.invalidWhole") : "";
  };
  const exceeded = budgetExceededText(t, status);

  const fields: { field: BudgetField; label: string; step: string; inputMode: "numeric" | "decimal" }[] = [
    { field: "runs", label: t("budget.runsPerDay"), step: "1", inputMode: "numeric" },
    { field: "cost", label: t("budget.costPerDay"), step: "0.01", inputMode: "decimal" },
    { field: "tokens", label: t("budget.tokensPerDay"), step: "1000", inputMode: "numeric" },
  ];

  return (
    <form
      className="budget-settings"
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <p className="runner-hint">{t("budget.description")}</p>

      <div className="budget-usage" aria-label={t("budget.today")} role="group">
        <strong className="budget-usage-title">{t("budget.today")}</strong>
        <UsageMeter
          label={t("budget.limit.runs")}
          used={status.used?.runs ?? 0}
          limit={status.budget?.dailyRuns ?? 0}
          format={formatCount}
        />
        <UsageMeter
          label={t("budget.limit.cost")}
          used={status.used?.costUsd ?? 0}
          limit={status.budget?.dailyCostUsd ?? 0}
          format={usd}
        />
        <UsageMeter
          label={t("budget.limit.tokens")}
          used={status.used?.tokens ?? 0}
          limit={status.budget?.dailyTokens ?? 0}
          format={formatCompact}
        />
      </div>
      {exceeded && (
        <div className="notify-message error budget-exceeded" role="status">
          {exceeded}
        </div>
      )}

      <div className="budget-fields">
        {fields.map(({ field, label, step, inputMode }) => {
          const problem = fieldError(field);
          return (
            <label key={field} className="run-field">
              <span>{label}</span>
              <input
                type="number"
                min={0}
                step={step}
                inputMode={inputMode}
                value={current[field]}
                onChange={(event) => edit(field, event.target.value)}
                aria-invalid={problem ? true : undefined}
                aria-describedby={problem ? ids[field] : undefined}
              />
              {problem && (
                <small id={ids[field]} className="run-field-error">
                  {problem}
                </small>
              )}
            </label>
          );
        })}
      </div>
      <p className="run-hint">{t("budget.zeroHint")}</p>

      {saveError && (
        <div className="notify-message error" role="alert">
          {saveError}
        </div>
      )}
      <div className="budget-actions">
        {saved && !dirty && (
          <span className="budget-saved" role="status">
            {t("budget.saved")}
          </span>
        )}
        <button type="submit" className="primary" disabled={!dirty || !parsed.budget || saving}>
          {saving ? t("budget.saving") : t("budget.save")}
        </button>
      </div>
    </form>
  );
}
