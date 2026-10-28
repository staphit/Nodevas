// Settings: the third face, beside Info and Station. Everything that used to
// be a dialog of its own (project settings, deadline reminders, backup,
// runners, usage, budget, routing, account security) is a part of one page:
// four sections in a list on the left, with a search box over them that
// finds any setting by name. Each section keeps what is changed often on its
// first layer and the rest in rows that open in place; Agents keeps the
// rarely changed under Advanced. The dialogs draw themselves inline here
// (InlineDialogContext) and stay dialogs nowhere else.
//
// Where the page is (a section and the row open in it) is remembered as one
// of the old parts (SETTINGS_PLACES), so openSettings(part) keeps working.
//
// Loaded on demand (App's React.lazy).

import { useCallback, useEffect, useRef, useState } from "react";

import { useTeamServer, useViewer } from "../components/SignIn";
import { InlineDialogContext } from "../components/DialogFrame";
import { setProjectMode } from "../station/projectMode";
import { useI18n } from "../i18n";
import { useWorkstationVisible } from "../runners/workstation";
import { useApp, usePreference } from "../store";
import { AgentsSection } from "./AgentsSection";
import {
  ADVANCED_ROWS,
  SETTINGS_PAGES,
  SETTINGS_PLACES,
  sectionFor,
  type SettingsPage,
  type SettingsRow,
  type SettingsSection,
  useSettingsRequest,
} from "./openSettings";
import { searchSettings, type SettingsHit, type SettingsNeed } from "./settingsSearch";
import { AccountSection, GeneralSection, ProjectSection } from "./SettingsSections";

/** What the page has open beyond what the remembered part says: a row
 * without a part of its own, Advanced with no row open. Tied to the part it
 * was set under, so asking for another part from outside starts afresh. */
interface Local {
  section: SettingsSection;
  row: SettingsRow | null;
  advanced: boolean;
}

export default function SettingsView() {
  const { t } = useI18n();
  const viewer = useViewer();
  const teamServer = useTeamServer();
  const workstation = useWorkstationVisible();
  const section = usePreference("settingsSection");
  const activeProject = useApp((s) => s.activeProject);
  const [local, setLocal] = useState<Local | null>(null);
  const [query, setQuery] = useState("");
  const [addRequested, setAddRequested] = useState(false);
  const addOpened = useCallback(() => setAddRequested(false), []);
  // "Add a runner" from the palette or Station: taken once, here.
  const addAsked = useSettingsRequest((s) => s.add);
  useEffect(() => {
    if (!addAsked) return;
    setAddRequested(true);
    useSettingsRequest.getState().clearRequest();
  }, [addAsked]);

  const place = SETTINGS_PLACES[section] ?? SETTINGS_PLACES.project;
  const page = place.page;
  const mine = local?.section === section ? local : null;
  const row = mine ? mine.row : (place.row ?? null);
  const inAdvanced = (candidate: SettingsRow | null) => candidate !== null && ADVANCED_ROWS.includes(candidate);
  const advanced = inAdvanced(row) || Boolean(mine?.advanced);

  // Remembered as the part the row had, or its section's first, so the
  // next openSettings of any part moves the page.
  const go = (nextPage: SettingsPage, nextRow: SettingsRow | null, nextAdvanced = inAdvanced(nextRow)) => {
    const next = sectionFor(nextPage, nextRow);
    useApp.getState().updateUIPreference("settingsSection", next);
    setLocal({ section: next, row: nextRow, advanced: nextPage === "agents" && nextAdvanced });
  };
  const toggleRow = (target: SettingsRow) => go(page, row === target ? null : target, advanced);
  const toggleAdvanced = () => go("agents", null, !advanced);

  const offered = (need: SettingsNeed): boolean => {
    if (need === "workstation") return workstation;
    if (need === "team") return teamServer;
    if (need === "account") return Boolean(teamServer && viewer);
    return Boolean(teamServer && viewer?.role === "admin");
  };
  const hits = searchSettings(query, t, offered);
  const searching = query.trim() !== "";
  // The hit just picked, until focus has moved to it: the search box gives
  // way to its hits' list, so focus follows the reader to what they asked for.
  const [picked, setPicked] = useState<SettingsHit | null>(null);
  const bodyRef = useRef<HTMLDivElement | null>(null);
  const pick = (hit: SettingsHit) => {
    go(hit.entry.page, hit.entry.row ?? null);
    if (hit.entry.add) setAddRequested(true);
    setQuery("");
    setPicked(hit);
  };
  // To the opened row's toggle, or else the section's heading. The add form
  // takes focus itself (its name field).
  useEffect(() => {
    if (!picked) return;
    setPicked(null);
    if (picked.entry.add) return;
    const body = bodyRef.current;
    if (!body) return;
    const toggle = body.querySelector<HTMLElement>(".settings-disclosure.is-open > .settings-disclosure-toggle");
    if (toggle) {
      toggle.focus();
      return;
    }
    const heading = body.querySelector<HTMLElement>("h2");
    if (!heading) return;
    heading.setAttribute("tabindex", "-1");
    heading.focus();
  }, [picked]);

  // A dialog's close, inline: a part that sends you to a node goes back to
  // the board. Escape does nothing here; the mode switch leaves the page.
  const leave = () => setProjectMode("info");
  const sectionProps = { row, toggleRow, go, leave };

  const sub: Record<SettingsPage, string> = {
    general: t("settingsPage.sub.general"),
    agents: t("settingsPage.sub.agents"),
    project: activeProject || t("settingsPage.sub.project"),
    account: teamServer ? t("settingsPage.sub.account") : t("settingsPage.sub.accountStandalone"),
  };

  return (
    <div className="settings-page" role="main" aria-label={t("mode.settings")}>
      <nav className="settings-page-nav" aria-label={t("settingsPage.parts")}>
        <input
          type="search"
          className="settings-search"
          value={query}
          // The placeholder is the short name, so it fits the column at any
          // text size; what the search finds is said on hover.
          placeholder={t("settingsPage.search")}
          title={t("settingsPage.searchHint")}
          aria-label={t("settingsPage.search")}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape" && query) {
              event.stopPropagation();
              setQuery("");
            }
            if (event.key === "Enter" && hits[0]) pick(hits[0]);
          }}
        />
        {searching ? (
          <div className="settings-page-group">
            {hits.map((hit) => (
              <button key={`${hit.entry.label}-${hit.entry.page}`} type="button" onClick={() => pick(hit)}>
                <span>{hit.label}</span>
                <small>{hit.where}</small>
              </button>
            ))}
            {hits.length === 0 && (
              <p className="settings-hint settings-search-empty" role="status">
                {t("settingsPage.noHits")}
              </p>
            )}
          </div>
        ) : (
          <div className="settings-page-group">
            {SETTINGS_PAGES.map((id) => (
              <button
                key={id}
                type="button"
                className={id === page ? "active" : ""}
                aria-current={id === page ? "page" : undefined}
                onClick={() => go(id, null, false)}
              >
                <span>{t(`settingsPage.page.${id}`)}</span>
                {/* Cut with an ellipsis where the list is narrow; whole on hover. */}
                <small title={sub[id]}>{sub[id]}</small>
              </button>
            ))}
          </div>
        )}
      </nav>
      <div className="settings-page-body" ref={bodyRef}>
        <InlineDialogContext.Provider value={true}>
          {page === "general" && <GeneralSection {...sectionProps} />}
          {page === "agents" && (
            <AgentsSection
              row={row}
              toggleRow={toggleRow}
              advanced={advanced}
              toggleAdvanced={toggleAdvanced}
              addRequested={addRequested}
              onAddOpened={addOpened}
              leave={leave}
            />
          )}
          {page === "project" && <ProjectSection {...sectionProps} />}
          {page === "account" && <AccountSection {...sectionProps} />}
        </InlineDialogContext.Provider>
      </div>
    </div>
  );
}
