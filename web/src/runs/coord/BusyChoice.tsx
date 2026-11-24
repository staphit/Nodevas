import { useId, useRef, useState, type KeyboardEvent } from "react";
import type { BusyOption, BusyResponse, RunHolder } from "../../api";
import { initials } from "../../components/PresenceAvatars";
import { agentColorKey, agentName } from "../../game/labels";
import { useCastCard } from "../../game/warroom/castArt";
import { formatLocalizedTime, useI18n } from "../../i18n";

export interface BusyChoiceProps {
  busy: BusyResponse;
  simple?: boolean;
  maxConcurrent?: number;
  onChoose: (choice: BusyOption | string) => void;
  onCancel: () => void;
}

function minutesSince(iso: string | undefined): number {
  if (!iso) return 1;
  const ms = Date.now() - Date.parse(iso);
  if (!Number.isFinite(ms) || ms <= 0) return 1;
  return Math.max(1, Math.round(ms / 60_000));
}

function formatSinceTime(iso: string | undefined): string {
  if (!iso) return "";
  return formatLocalizedTime(iso) || iso;
}

function HolderCard({ holder, simple }: { holder: RunHolder; simple: boolean }) {
  const { t } = useI18n();
  const art = useCastCard(holder.agent, holder.model || "default", "idle");
  const className = [
    "coord-holder-card",
    holder.waiting ? "is-waiting" : "",
    art ? "has-art" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const titleText =
    holder.repoName && !holder.sameNode
      ? t("busy.holderRepo", { title: holder.title, repo: holder.repoName })
      : holder.title;

  return (
    <div className={className}>
      {art ? (
        <span className="coord-holder-portrait" aria-hidden="true" data-character={art.figure.character}>
          <img src={art.url} alt="" draggable={false} style={art.imgStyle} onError={art.onError} />
        </span>
      ) : (
        <span className={`station-avatar agent-${agentColorKey(holder.agent)}`} aria-hidden="true">
          {initials(agentName(holder.agent))}
        </span>
      )}
      <div className="coord-holder-body">
        <div className="coord-holder-title">{titleText}</div>
        {!simple && !art && (
          <div className="coord-holder-meta">
            {t("busy.holder", {
              agent: agentName(holder.agent),
              model: holder.model || "default",
              time: formatSinceTime(holder.since),
              who: holder.by ?? "",
            })}
          </div>
        )}
        {holder.waiting && <span className="coord-holder-waiting">{t("busy.waiting")}</span>}
      </div>
    </div>
  );
}

function ContinuePortrait({ agent, model }: { agent: string; model: string }) {
  const art = useCastCard(agent, model || "default", "idle");
  if (!art) return null;
  return (
    <span className="coord-choice-portrait" aria-hidden="true" data-character={art.figure.character}>
      <img src={art.url} alt="" draggable={false} style={art.imgStyle} onError={art.onError} />
    </span>
  );
}

export function BusyChoice({
  busy,
  simple = false,
  maxConcurrent,
  onChoose,
  onCancel,
}: BusyChoiceProps) {
  const { t } = useI18n();
  const titleId = useId();
  const holders = busy.holders ?? [];
  const choices = busy.choices ?? [];
  const [selected, setSelected] = useState<string>(() => choices[0] ?? "");
  const radioRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const activeChoice = choices.includes(selected) ? selected : (choices[0] ?? "");

  const oldestSince = holders[0]?.since;
  const heading = simple
    ? t("busy.simple.title", { minutes: minutesSince(oldestSince) })
    : t("busy.title");

  const showAskOwner =
    !simple &&
    !choices.includes("takeOver") &&
    (busy.others > 0 || holders.some((h) => !h.mayStop));

  const showOutdated =
    !simple &&
    !choices.includes("queue") &&
    !choices.includes("both") &&
    choices.includes("takeOver");

  const contAgentKind = busy.continue?.agent || holders[0]?.agent || "";
  const contModelId = busy.continue?.model || holders[0]?.model || "default";

  const takeOverDetail = (): string => {
    if (simple) return "";
    const hasInteractiveRunning = holders.some(
      (h) => h.mode === "interactive" && h.status === "running",
    );
    if (hasInteractiveRunning) {
      const isNode = holders.some((h) => Boolean(h.nodeId) && h.sameNode !== false);
      return isNode ? t("busy.takeOverKeepsNoPR") : t("busy.takeOverKeeps");
    }
    return t("busy.takeOverLoses");
  };

  const choiceLabel = (choice: string): { label: string; detail?: string } => {
    switch (choice) {
      case "queue":
        return { label: simple ? t("busy.simple.queue") : t("busy.queue") };
      case "continue":
        return {
          label: simple
            ? t("busy.simple.continue")
            : t("busy.continue", {
                agent: agentName(contAgentKind),
                model: contModelId,
              }),
        };
      case "takeOver": {
        const detail = takeOverDetail();
        return {
          label: simple ? t("busy.simple.takeOver") : t("busy.takeOver"),
          detail: detail || undefined,
        };
      }
      case "both":
        return {
          label: simple ? t("busy.simple.both") : t("busy.both"),
          detail: maxConcurrent === 1 ? t("busy.bothSerial") : undefined,
        };
      case "skip":
        return { label: t("busy.skip") };
      default:
        return { label: choice };
    }
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (choices.length === 0) return;
    const currentIndex = Math.max(0, choices.indexOf(activeChoice));
    let nextIndex = -1;
    if (event.key === "ArrowDown" || event.key === "ArrowRight") {
      nextIndex = (currentIndex + 1) % choices.length;
    } else if (event.key === "ArrowUp" || event.key === "ArrowLeft") {
      nextIndex = (currentIndex - 1 + choices.length) % choices.length;
    } else if (event.key === "Home") {
      nextIndex = 0;
    } else if (event.key === "End") {
      nextIndex = choices.length - 1;
    }
    if (nextIndex >= 0) {
      event.preventDefault();
      const nextChoice = choices[nextIndex];
      setSelected(nextChoice);
      radioRefs.current[nextIndex]?.focus();
    }
  };

  return (
    <div className={`coord-busy${simple ? " is-simple" : ""}`} role="region" aria-labelledby={titleId}>
      <h3 id={titleId} className="coord-busy-title">
        {heading}
      </h3>

      <div className="coord-holders">
        {holders.map((holder) => (
          <HolderCard key={holder.runId} holder={holder} simple={simple} />
        ))}
        {busy.others > 0 && (
          <div className="coord-holder-card is-hidden">
            <span className="station-avatar is-muted" aria-hidden="true">
              ?
            </span>
            <div className="coord-holder-body">
              <div className="coord-holder-title">{t("busy.others", { count: busy.others })}</div>
            </div>
          </div>
        )}
      </div>

      {showAskOwner && <p className="coord-busy-note">{t("busy.askOwner")}</p>}
      {showOutdated && <p className="coord-busy-note">{t("busy.outdated")}</p>}

      <div
        className="coord-busy-choices"
        role="radiogroup"
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
      >
        {choices.map((choice, index) => {
          const checked = choice === activeChoice;
          const { label, detail } = choiceLabel(choice);
          return (
            <button
              key={choice}
              ref={(el) => {
                radioRefs.current[index] = el;
              }}
              type="button"
              role="radio"
              aria-checked={checked}
              tabIndex={checked ? 0 : -1}
              className={`coord-choice-card${checked ? " is-selected" : ""}`}
              onClick={() => setSelected(choice)}
            >
              {choice === "continue" && !simple && contAgentKind && (
                <ContinuePortrait agent={contAgentKind} model={contModelId} />
              )}
              <span className="coord-choice-text">
                <span className="coord-choice-label">{label}</span>
                {detail && <span className="coord-choice-detail">{detail}</span>}
              </span>
            </button>
          );
        })}
      </div>

      <div className="coord-busy-actions">
        <button type="button" className="coord-btn" onClick={onCancel}>
          {t("common.cancel")}
        </button>
        <button
          type="button"
          className="coord-btn is-primary"
          disabled={!activeChoice}
          onClick={() => activeChoice && onChoose(activeChoice)}
        >
          {t("common.confirm")}
        </button>
      </div>
    </div>
  );
}
