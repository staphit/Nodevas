import { assigneeUsage } from "../../domain";
import { useApp } from "../../store";
import { EmptyState } from "../InteractionPrimitives";
import { runSettingsCommand, type SettingsNotify } from "./notify";
import { useI18n } from "../../i18n";

/** Members are a by-product of assigning work, so this only edits the email. */
export function PeopleEditor({ notify }: { notify: SettingsNotify }) {
  const graph = useApp((s) => s.graph);
  const { t } = useI18n();
  const users = graph?.users ?? [];

  return (
    <section className="settings-section">
      <p className="settings-hint">
        {t("settings.peopleIntro")}
      </p>
      <ul className="settings-list">
        {users.map((user) => (
          <li key={user.id}>
            <span className="settings-user-id mono">{user.id}</span>
            <input defaultValue={user.name} aria-label={t("settings.personNameAria", { name: user.name })} readOnly />
            <input
              type="email"
              defaultValue={user.email ?? ""}
              placeholder={t("settings.emailOptional")}
              aria-label={t("settings.personEmailAria", { name: user.name })}
              onBlur={(event) => {
                const email = event.target.value.trim();
                if (email === (user.email ?? "")) return;
                void runSettingsCommand(
                  notify,
                  useApp.getState().updateNode({
                    type: "node.setUserEmail",
                    userId: user.id,
                    email,
                  }),
                );
              }}
            />
            <small className="settings-usage">
              {t("settings.assignedNodes", { count: graph ? assigneeUsage(graph, user.id).length : 0 })}
            </small>
          </li>
        ))}
      </ul>
      {users.length === 0 && (
        <EmptyState
          title={t("settings.noPeople")}
          description={t("settings.noPeopleHint")}
        />
      )}
    </section>
  );
}
