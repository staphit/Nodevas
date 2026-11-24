import type { ReactNode } from "react";

import { CastPortrait } from "../../game/warroom/castArt";
import { useI18n } from "../../i18n";
import type { DispatchMode } from "../../runs/RunModePicker";
import { AgentPlainAvatar, PictureCards, type PictureCardItem } from "./PictureCards";

export interface ModeCardsProps {
  agent: string;
  model?: string;
  value: DispatchMode;
  onChange: (mode: DispatchMode) => void;
  label?: string;
  interactiveDisabled?: boolean;
  disabled?: boolean;
  note?: ReactNode;
  invalid?: boolean;
  describedBy?: string;
  className?: string;
}

/** Interactive vs Background mode cards: Interactive displays the character's
 * `speak` still, Background displays its `write` still. */
export function ModeCards({
  agent,
  model = "default",
  value,
  onChange,
  label,
  interactiveDisabled = false,
  disabled,
  note,
  invalid,
  describedBy,
  className,
}: ModeCardsProps) {
  const { t } = useI18n();

  const items: PictureCardItem[] = [
    {
      id: "interactive",
      label: t("picker.mode.interactive"),
      disabled: interactiveDisabled,
      visual: (
        <CastPortrait
          agent={agent}
          model={model}
          scene="speak"
          className="picker-card-art"
          fallback={<AgentPlainAvatar agent={agent} />}
        />
      ),
    },
    {
      id: "headless",
      label: t("picker.mode.background"),
      visual: (
        <CastPortrait
          agent={agent}
          model={model}
          scene="write"
          className="picker-card-art"
          fallback={<AgentPlainAvatar agent={agent} />}
        />
      ),
    },
  ];

  const afterGrid = note ? (
    typeof note === "string" ? (
      <p className="picker-note">{note}</p>
    ) : (
      <div className="picker-note">{note}</div>
    )
  ) : null;

  return (
    <PictureCards
      mode="one"
      label={label ?? t("terminal.mode")}
      items={items}
      value={value}
      onChange={(next) => onChange(next as DispatchMode)}
      disabled={disabled}
      invalid={invalid}
      describedBy={describedBy}
      afterGrid={afterGrid}
      className={className ? `picker-modes ${className}` : "picker-modes"}
    />
  );
}
