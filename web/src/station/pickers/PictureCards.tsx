import {
  useId,
  useRef,
  type KeyboardEvent,
  type ReactNode,
} from "react";

import type { Runner } from "../../api";
import { initials } from "../../components/PresenceAvatars";
import { agentColorKey, agentName } from "../../game/labels";
import { useI18n } from "../../i18n";
import { IconComputer } from "../../icons";
import { runnerDisplayName } from "../../runners/runnerName";

export interface PictureCardItem {
  id: string;
  label: string;
  detail?: string;
  detailTitle?: string;
  visual: ReactNode;
  disabled?: boolean;
  title?: string;
}

export interface PictureCardGroup {
  id: string;
  label?: string;
  items: readonly PictureCardItem[];
}

interface PictureCardsCommon {
  label: string;
  items?: readonly PictureCardItem[];
  groups?: readonly PictureCardGroup[];
  disabled?: boolean;
  invalid?: boolean;
  describedBy?: string;
  headerAction?: ReactNode;
  beforeGrid?: ReactNode;
  afterGrid?: ReactNode;
  empty?: ReactNode;
  className?: string;
}

export interface SinglePictureCardsProps extends PictureCardsCommon {
  mode: "one";
  value: string;
  onChange: (id: string) => void;
}

export interface MultiPictureCardsProps extends PictureCardsCommon {
  mode: "many";
  values: readonly string[];
  onChange: (ids: string[]) => void;
}

export type PictureCardsProps = SinglePictureCardsProps | MultiPictureCardsProps;

/** Plain initials badge used when no theme pack is active, no character matches,
 * or a card image fails to load. Never uses vendor logos or the unnamed "?". */
export function AgentPlainAvatar({ agent }: { agent: string }) {
  const name = agentName(agent);
  return (
    <span className={`station-avatar agent-${agentColorKey(agent)} picker-card-avatar`} aria-hidden="true">
      {initials(name)}
    </span>
  );
}

/** Fixed neutral icon for a runner (no theme-pack art: manifest places are text only). */
export function RunnerIcon({
  name,
  size = 22,
  title,
  className,
}: {
  name?: string;
  size?: number;
  title?: string;
  className?: string;
}) {
  const tooltip = title ?? name;
  return (
    <span
      className={className ? `picker-runner-badge ${className}` : "picker-runner-badge"}
      title={tooltip}
      aria-hidden={name ? undefined : true}
    >
      <span className="picker-runner-icon" aria-hidden="true">
        <IconComputer size={size} />
      </span>
      {name && <span className="picker-runner-name">{name}</span>}
    </span>
  );
}

export interface RunnerCardsProps {
  runners: readonly Runner[];
  value: string;
  onChange: (runnerId: string) => void;
  label?: string;
  displayName?: (runner: Runner) => string;
  disabledReason?: (runner: Runner) => string;
  disabled?: boolean;
  invalid?: boolean;
  describedBy?: string;
  className?: string;
}

/** Runner radio cards: always uses the fixed runner icon, never theme-pack art. */
export function RunnerCards({
  runners,
  value,
  onChange,
  label,
  displayName,
  disabledReason,
  disabled,
  invalid,
  describedBy,
  className,
}: RunnerCardsProps) {
  const { t } = useI18n();
  const items: PictureCardItem[] = runners.map((runner) => {
    const name = displayName ? displayName(runner) : runnerDisplayName(t, runner.name);
    const reason = disabledReason ? disabledReason(runner) : "";
    return {
      id: runner.id,
      label: name,
      detail: reason || undefined,
      disabled: Boolean(reason),
      visual: (
        <span className="picker-runner-icon" aria-hidden="true">
          <IconComputer size={26} />
        </span>
      ),
    };
  });

  return (
    <PictureCards
      mode="one"
      label={label ?? t("picker.runner")}
      items={items}
      value={value}
      onChange={onChange}
      disabled={disabled}
      invalid={invalid}
      describedBy={describedBy}
      className={className ? `picker-runners ${className}` : "picker-runners"}
    />
  );
}

function flattenItems(
  items: readonly PictureCardItem[] | undefined,
  groups: readonly PictureCardGroup[] | undefined,
): readonly PictureCardItem[] {
  if (groups && groups.length > 0) {
    return groups.flatMap((group) => group.items);
  }
  return items ?? [];
}

/** Generic picture-card picker supporting single-select radiogroup and
 * multi-select native checkbox fieldset modes. */
export function PictureCards(props: PictureCardsProps) {
  if (props.mode === "many") {
    return <MultiPictureCards {...props} />;
  }
  return <SinglePictureCards {...props} />;
}

function SinglePictureCards({
  label,
  items,
  groups,
  value,
  onChange,
  disabled = false,
  invalid,
  describedBy,
  headerAction,
  beforeGrid,
  afterGrid,
  empty,
  className,
}: SinglePictureCardsProps) {
  const labelId = useId();
  const groupRef = useRef<HTMLDivElement | null>(null);
  const flat = flattenItems(items, groups);
  const enabled = flat.filter((item) => !disabled && !item.disabled);
  const tabTargetId =
    enabled.find((item) => item.id === value)?.id ?? enabled[0]?.id ?? "";

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (enabled.length === 0) return;
    let nextIndex = -1;
    const focusedId =
      document.activeElement instanceof HTMLElement
        ? document.activeElement.dataset.cardId
        : undefined;
    const currentIndex = Math.max(
      0,
      enabled.findIndex((item) => item.id === (focusedId ?? value)),
    );

    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (currentIndex + 1) % enabled.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (currentIndex - 1 + enabled.length) % enabled.length;
    } else if (event.key === "Home") {
      nextIndex = 0;
    } else if (event.key === "End") {
      nextIndex = enabled.length - 1;
    } else {
      return;
    }

    event.preventDefault();
    const next = enabled[nextIndex];
    if (!next) return;
    if (next.id !== value) onChange(next.id);
    const radios = Array.from(
      groupRef.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [],
    );
    radios.find((el) => el.dataset.cardId === next.id)?.focus();
  };

  const renderCard = (item: PictureCardItem) => {
    const isDisabled = disabled || Boolean(item.disabled);
    const selected = !isDisabled && item.id === value;
    const tabIndex = !isDisabled && item.id === tabTargetId ? 0 : -1;
    return (
      <button
        key={item.id}
        type="button"
        role="radio"
        aria-checked={selected}
        tabIndex={tabIndex}
        disabled={isDisabled}
        data-card-id={item.id}
        title={item.title}
        className={`picker-card${selected ? " is-selected" : ""}${isDisabled ? " is-disabled" : ""}`}
        onClick={() => {
          if (!isDisabled) onChange(item.id);
        }}
      >
        <span className="picker-card-visual">{item.visual}</span>
        <span className="picker-card-text">
          <span className="picker-card-title">{item.label}</span>
          {item.detail && (
            <small className="picker-card-sub" title={item.detailTitle}>
              {item.detail}
            </small>
          )}
        </span>
      </button>
    );
  };

  const hasGroups = Boolean(groups && groups.length > 0);
  const rootClass = className ? `picker-field ${className}` : "picker-field";

  return (
    <div className={rootClass}>
      <div className="picker-head">
        <span className="picker-label" id={labelId}>
          {label}
        </span>
        {headerAction && <span className="picker-head-action">{headerAction}</span>}
      </div>
      {beforeGrid}
      {flat.length === 0 ? (
        empty ?? null
      ) : (
        <div
          ref={groupRef}
          className={hasGroups ? "picker-groups" : "picker-grid"}
          role="radiogroup"
          aria-labelledby={labelId}
          aria-invalid={invalid || undefined}
          aria-describedby={describedBy}
          onKeyDown={onKeyDown}
        >
          {hasGroups
            ? groups!.map((group) => (
                <div key={group.id} className="picker-group">
                  {group.label && <div className="picker-group-title">{group.label}</div>}
                  <div className="picker-grid">{group.items.map(renderCard)}</div>
                </div>
              ))
            : flat.map(renderCard)}
        </div>
      )}
      {afterGrid}
    </div>
  );
}

function MultiPictureCards({
  label,
  items,
  groups,
  values,
  onChange,
  disabled = false,
  invalid,
  describedBy,
  headerAction,
  beforeGrid,
  afterGrid,
  empty,
  className,
}: MultiPictureCardsProps) {
  const { t } = useI18n();
  const flat = flattenItems(items, groups);
  const selectedSet = new Set(values);
  const enabledIds = flat
    .filter((item) => !disabled && !item.disabled)
    .map((item) => item.id);
  const pickedCount = flat.filter((item) => selectedSet.has(item.id)).length;
  const allPicked = enabledIds.length > 0 && enabledIds.every((id) => selectedSet.has(id));

  const toggle = (id: string) => {
    const next = new Set(selectedSet);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    onChange(flat.filter((item) => next.has(item.id)).map((item) => item.id));
  };

  const rootClass = className ? `picker-field picker-fieldset ${className}` : "picker-field picker-fieldset";

  return (
    <fieldset
      className={rootClass}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
    >
      <legend className="picker-legend">{label}</legend>
      <div className="picker-head picker-many-bar">
        <span className="picker-picked" aria-live="polite">
          {t("picker.picked", { count: pickedCount })}
        </span>
        <span className="picker-many-actions">
          <button
            type="button"
            className="picker-link-btn"
            disabled={disabled || enabledIds.length === 0 || allPicked}
            onClick={() => onChange(enabledIds)}
          >
            {t("picker.all")}
          </button>
          <button
            type="button"
            className="picker-link-btn"
            disabled={disabled || pickedCount === 0}
            onClick={() => onChange([])}
          >
            {t("picker.none")}
          </button>
          {headerAction}
        </span>
      </div>
      {beforeGrid}
      {flat.length === 0 ? (
        empty ?? null
      ) : (
        <div className="picker-grid">
          {flat.map((item) => {
            const isDisabled = disabled || Boolean(item.disabled);
            const checked = selectedSet.has(item.id);
            return (
              <label
                key={item.id}
                title={item.title}
                className={`picker-card picker-card-check${checked ? " is-selected" : ""}${isDisabled ? " is-disabled" : ""}`}
              >
                <input
                  type="checkbox"
                  className="picker-checkbox"
                  checked={checked}
                  disabled={isDisabled}
                  onChange={() => toggle(item.id)}
                />
                <span className="picker-card-visual">{item.visual}</span>
                <span className="picker-card-text">
                  <span className="picker-card-title">{item.label}</span>
                  {item.detail && (
                    <small className="picker-card-sub" title={item.detailTitle}>
                      {item.detail}
                    </small>
                  )}
                </span>
              </label>
            );
          })}
        </div>
      )}
      {afterGrid}
    </fieldset>
  );
}
