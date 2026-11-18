/**
 * With a theme pack on, a draft is presented by one of the pack's
 * characters, in a small visual-novel dialogue box at the top of the card.
 *
 * Who: the character the war room would cast for whoever drafted it
 * (`characterFor`: by model first, then by agent, then the pack's fallback
 * character). A copilot draft is cast by its provider and model; a coding
 * agent's plan by the planner's agent and model. A pack with nobody for it
 * and no fallback presents nothing.
 *
 * What she says: a pack has no line for a draft (its lines are by an agent's
 * state, and the manifest is validated on the server), so she says her
 * "asking-user" line (else the pack's), the state where she wants the
 * person's eye, with {node} as the draft ("8 steps") and {n} as its count.
 * With neither, the app's own line. No art is made: her picture is her
 * speaking scene's still (else her idle one), the pack's own image.
 *
 * The warroom's styles are not loaded in the copilot panel, so the box has
 * its own (15-copilot.css, .draft-presenter).
 */

import { useState } from "react";
import type { CopilotDraftData } from "../../api";
import { figureFor, characterFor } from "../../game/warroom/themeCast";
import { useI18n } from "../../i18n";
import type { ThemeText } from "../../theme-pack/themeManifest";
import { useThemePack } from "../../theme-pack/useThemePack";

function inLanguage(text: ThemeText | undefined, language: string): string | undefined {
  return text && ((language === "zh-TW" && text["zh-TW"]) || text.en);
}

/** A pack's text with its {names} filled in; one it has no value for stays. */
function fill(text: string, values: Record<string, string | number>): string {
  return text.replace(/\{(\w+)\}/g, (whole, name: string) =>
    Object.hasOwn(values, name) ? String(values[name]) : whole,
  );
}

export function DraftPresenter({
  data,
  count,
  model,
}: {
  data: CopilotDraftData;
  /** The draft's node count, as the person has it now. */
  count: number;
  /** Who drafted it, as the card's header names it. */
  model: string;
}) {
  const { t, language } = useI18n();
  const theme = useThemePack();
  // An image that would not load leaves the box without a picture.
  const [lost, setLost] = useState<ReadonlySet<string>>(() => new Set());
  const lose = (url: string) => setLost((was) => (was.has(url) ? was : new Set([...was, url])));
  if (!theme) return null;
  const { manifest, imageUrl } = theme;

  const who = data.origin
    ? { agent: data.origin.agent ?? "", model: data.origin.model ?? "" }
    : { agent: data.provider ?? "", model: data.model ?? "" };
  const id = characterFor(manifest, who.agent, who.model);
  if (!id || !Object.hasOwn(manifest.characters, id)) return null;
  const character = manifest.characters[id];

  const title = inLanguage(character.title, language);
  const name = title ?? (model || id);
  const one = count === 1;
  const node = t("game.story.quoted", {
    text: one ? t("draft.presenter.nodeOne") : t("draft.presenter.node", { n: count }),
  });
  const own = inLanguage(character.lines?.["asking-user"] ?? manifest.lines?.["asking-user"], language);
  const line = own
    ? fill(own, { node, n: count })
    : one
      ? t("draft.presenter.lineOne")
      : t("draft.presenter.line", { n: count });

  const figure = figureFor(manifest, who, "speak");
  const picture = figure ? imageUrl(figure.scene.image) : "";
  const frames = figure?.scene.frames?.count ?? 1;
  const sign = character.sign ? imageUrl(character.sign) : "";

  return (
    <section
      className="draft-presenter"
      data-character={id}
      aria-label={t("draft.presenter.label", { name })}
    >
      {picture && !lost.has(picture) && figure && (
        <div
          className="draft-presenter-face"
          style={{ aspectRatio: `${figure.scene.w} / ${figure.scene.h}` }}
          aria-hidden="true"
        >
          <img
            src={picture}
            alt=""
            draggable={false}
            // A strip: its first frame is the still.
            style={frames > 1 ? { width: `${frames * 100}%` } : undefined}
            onError={() => lose(picture)}
          />
        </div>
      )}
      <div className="draft-presenter-talk">
        <p className="draft-presenter-plate">
          {sign && !lost.has(sign) && (
            <img className="draft-presenter-sign" src={sign} alt="" aria-hidden="true" onError={() => lose(sign)} />
          )}
          {name}
          {title && model && <small>{model}</small>}
        </p>
        <p className="draft-presenter-line">{line}</p>
      </div>
    </section>
  );
}
