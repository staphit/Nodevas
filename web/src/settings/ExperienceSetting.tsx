// "How you use Nodevas" (Settings, General; single-user edition only):
// Simple or Everything, the `experience` preference. Simple is the preset
// for someone who does not write code (simple/simpleMode). Going back to
// Everything does not turn sending finished work online back on: it says
// so, and where to turn it on.

import { useId, useState } from "react";

import { useI18n } from "../i18n";
import { useEdition } from "../runners/edition";
import { EXPERIENCES, setExperience, type Experience } from "../simple/simpleMode";
import { usePreference } from "../store";

export function ExperienceSetting() {
  const { t } = useI18n();
  const name = useId();
  const single = useEdition() === "single";
  const experience = usePreference("experience");
  const [left, setLeft] = useState(false);
  if (!single) return null;

  const pick = (value: Experience) => {
    if (value === experience) return;
    setLeft(value === "full");
    setExperience(value);
  };

  return (
    <fieldset className="this-computer-permission">
      <legend>{t("simple.setting.title")}</legend>
      {[...EXPERIENCES].reverse().map((value) => (
        <label key={value} className={`this-computer-level${experience === value ? " selected" : ""}`}>
          <input
            type="radio"
            name={name}
            value={value}
            checked={experience === value}
            onChange={() => pick(value)}
          />
          {/* What each one means, under its name. */}
          <span style={{ display: "grid", gap: 2 }}>
            <strong>{t(`simple.setting.${value}`)}</strong>
            <span className="settings-hint">{t(`simple.setting.${value}What`)}</span>
          </span>
        </label>
      ))}
      {left && experience === "full" && (
        <p className="settings-hint" role="status">
          {t("simple.setting.leftNote")}
        </p>
      )}
    </fieldset>
  );
}
