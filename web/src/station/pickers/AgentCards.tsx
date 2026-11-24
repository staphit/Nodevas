import { useMemo, type ReactNode } from "react";

import type { RunnerAgent } from "../../api";
import { agentName } from "../../game/labels";
import { CastPortrait } from "../../game/warroom/castArt";
import { useI18n } from "../../i18n";
import { AgentPlainAvatar, PictureCards, type PictureCardItem } from "./PictureCards";

export interface AgentCardsProps {
  /** Runner agents (either the runner's full agents list or installed agents). */
  agents: readonly RunnerAgent[];
  /** Optional explicit signed-out agents if `agents` was pre-filtered to installed ones. */
  signedOut?: readonly RunnerAgent[];
  value: string;
  onChange: (kind: string) => void;
  label?: string;
  simple?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  describedBy?: string;
  empty?: ReactNode;
  className?: string;
}

/** Picture cards for installed agents. Signed-out agents never receive a card;
 * in full mode each signed-out agent gets a muted line below the cards. */
export function AgentCards({
  agents,
  signedOut,
  value,
  onChange,
  label,
  simple = false,
  disabled,
  invalid,
  describedBy,
  empty,
  className,
}: AgentCardsProps) {
  const { t } = useI18n();

  const installed = useMemo(
    () => agents.filter((agent) => agent.installed && !agent.signedOut),
    [agents],
  );

  const signedOutAgents = useMemo(() => {
    const seen = new Set<string>();
    const list: RunnerAgent[] = [];
    for (const agent of [...agents, ...(signedOut ?? [])]) {
      if (agent.signedOut && !seen.has(agent.kind)) {
        seen.add(agent.kind);
        list.push(agent);
      }
    }
    return list;
  }, [agents, signedOut]);

  const items: PictureCardItem[] = installed.map((agent) => ({
    id: agent.kind,
    label: agentName(agent.kind),
    detail: !simple && agent.version ? agent.version : undefined,
    visual: (
      <CastPortrait
        agent={agent.kind}
        model="default"
        scene="idle"
        className="picker-card-art"
        fallback={<AgentPlainAvatar agent={agent.kind} />}
      />
    ),
  }));

  const signedOutNotes =
    !simple && signedOutAgents.length > 0 ? (
      <div className="picker-signed-out-list">
        {signedOutAgents.map((agent) => (
          <p key={agent.kind} className="picker-signed-out">
            {t("picker.signedOut", { agent: agentName(agent.kind) })}
          </p>
        ))}
      </div>
    ) : null;

  return (
    <PictureCards
      mode="one"
      label={label ?? t("picker.agent")}
      items={items}
      value={value}
      onChange={onChange}
      disabled={disabled}
      invalid={invalid}
      describedBy={describedBy}
      empty={empty}
      afterGrid={signedOutNotes}
      className={className ? `picker-agents ${className}` : "picker-agents"}
    />
  );
}
