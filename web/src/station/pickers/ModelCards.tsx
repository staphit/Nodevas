import { useMemo, useState, type ReactNode } from "react";

import type { RunnerModel } from "../../api";
import { agentName } from "../../game/labels";
import { CastPortrait } from "../../game/warroom/castArt";
import { useActiveThemeCast } from "../../game/warroom/themeCastContext";
import { useI18n } from "../../i18n";
import { CLI_DEFAULT_MODEL_LABEL, runnerModelLabel } from "../../runners/runnerName";
import {
  AgentPlainAvatar,
  PictureCards,
  type PictureCardGroup,
  type PictureCardItem,
} from "./PictureCards";

/** Beyond this many models, ModelCards shows a filter input, collapses extra
 * cards behind a "more models" button, and groups by pack character. */
export const MODEL_COLLAPSE_THRESHOLD = 8;

export interface ModelCardsProps {
  agent: string;
  models: readonly RunnerModel[];
  value: string;
  onChange: (modelId: string) => void;
  label?: string;
  simple?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  describedBy?: string;
  empty?: ReactNode;
  className?: string;
}

function orderModels(models: readonly RunnerModel[]): RunnerModel[] {
  const defaultIdx = models.findIndex((m) => m.id === "default");
  const firstIdx = defaultIdx >= 0 ? defaultIdx : models.findIndex((m) => m.default);
  if (firstIdx <= 0) return [...models];
  return [models[firstIdx], ...models.slice(0, firstIdx), ...models.slice(firstIdx + 1)];
}

/** Picture cards for an agent's models. Always shows a visible model name,
 * places "default" first with the agent's character, matches model ids against
 * the active theme pack, and adds filtering + character grouping above 8. */
export function ModelCards({
  agent,
  models,
  value,
  onChange,
  label,
  simple = false,
  disabled,
  invalid,
  describedBy,
  empty,
  className,
}: ModelCardsProps) {
  const { t, language } = useI18n();
  const cast = useActiveThemeCast(true);
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState(false);

  const ordered = useMemo(() => orderModels(models), [models]);
  const large = ordered.length > MODEL_COLLAPSE_THRESHOLD;
  const trimmed = query.trim().toLowerCase();

  const entries = useMemo(() => {
    return ordered.map((model) => {
      const isCliDefault =
        model.id === "default" && (!model.label || model.label === CLI_DEFAULT_MODEL_LABEL);
      const displayLabel = isCliDefault
        ? t(simple ? "picker.simple.defaultModel" : "picker.defaultModel")
        : runnerModelLabel(t, model);
      const figure = cast?.figureOf({ agent, model: model.id }, "idle");
      const characterId = figure?.character ?? "";
      const characterTitle = characterId ? cast?.title(characterId, language) : undefined;
      return {
        model,
        displayLabel,
        characterId,
        characterTitle,
      };
    });
  }, [ordered, agent, simple, cast, t, language]);

  const filtered = useMemo(() => {
    if (!large || !trimmed) return entries;
    return entries.filter(({ model, displayLabel, characterId, characterTitle }) => {
      const haystack = `${model.id} ${model.label ?? ""} ${displayLabel} ${characterId} ${characterTitle ?? ""}`.toLowerCase();
      return haystack.includes(trimmed);
    });
  }, [entries, large, trimmed]);

  const visible = useMemo(() => {
    if (!large || expanded || trimmed) return filtered;
    const head = filtered.slice(0, MODEL_COLLAPSE_THRESHOLD);
    if (value && !head.some((entry) => entry.model.id === value)) {
      const selected = filtered.find((entry) => entry.model.id === value);
      if (selected) return [...head, selected];
    }
    return head;
  }, [filtered, large, expanded, trimmed, value]);

  const hiddenCount = large && !expanded && !trimmed ? Math.max(0, filtered.length - visible.length) : 0;

  const toCard = ({ model, displayLabel }: (typeof entries)[number]): PictureCardItem => ({
    id: model.id,
    label: displayLabel,
    visual: (
      <CastPortrait
        agent={agent}
        model={model.id}
        scene="idle"
        className="picker-card-art"
        fallback={<AgentPlainAvatar agent={agent} />}
      />
    ),
  });

  const groups = useMemo<PictureCardGroup[] | undefined>(() => {
    if (!large || !cast) return undefined;
    const buckets = new Map<string, { label: string; items: PictureCardItem[] }>();
    for (const entry of visible) {
      const key = entry.characterId || "__plain__";
      const existing = buckets.get(key);
      if (existing) {
        existing.items.push(toCard(entry));
      } else {
        buckets.set(key, {
          label: entry.characterTitle ?? (entry.characterId || agentName(agent)),
          items: [toCard(entry)],
        });
      }
    }
    if (buckets.size < 2) return undefined;
    return Array.from(buckets.entries()).map(([id, bucket]) => ({
      id,
      label: bucket.label,
      items: bucket.items,
    }));
  }, [large, cast, visible, agent]);

  const flatItems = groups ? undefined : visible.map(toCard);

  const filterBar = large ? (
    <input
      type="search"
      className="picker-filter"
      value={query}
      onChange={(event) => setQuery(event.target.value)}
      placeholder={t("picker.filter")}
      aria-label={t("picker.filter")}
      disabled={disabled}
    />
  ) : null;

  const moreButton =
    hiddenCount > 0 ? (
      <button
        type="button"
        className="picker-more-btn"
        disabled={disabled}
        onClick={() => setExpanded(true)}
      >
        {t("picker.more", { count: hiddenCount })}
      </button>
    ) : null;

  const emptyNode =
    trimmed && visible.length === 0 ? (
      <p className="picker-empty">{t("settingsPage.noHits")}</p>
    ) : (
      empty
    );

  return (
    <PictureCards
      mode="one"
      label={label ?? t("picker.model")}
      items={flatItems}
      groups={groups}
      value={value}
      onChange={onChange}
      disabled={disabled}
      invalid={invalid}
      describedBy={describedBy}
      beforeGrid={filterBar}
      afterGrid={moreButton}
      empty={emptyNode}
      className={className ? `picker-models ${className}` : "picker-models"}
    />
  );
}
