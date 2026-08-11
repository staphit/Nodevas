import { assigneeUsage } from "../../domain";
import { useApp } from "../../store";
import { EmptyState } from "../InteractionPrimitives";
import { runSettingsCommand, type SettingsNotify } from "./notify";

/** Members are a by-product of assigning work, so this only edits the email. */
export function PeopleEditor({ notify }: { notify: SettingsNotify }) {
  const graph = useApp((s) => s.graph);
  const users = graph?.users ?? [];

  return (
    <section className="settings-section">
      <p className="settings-hint">
        成員在指派負責人時自動建立。信箱用於截止提醒。
      </p>
      <ul className="settings-list">
        {users.map((user) => (
          <li key={user.id}>
            <span className="settings-user-id mono">{user.id}</span>
            <input defaultValue={user.name} aria-label={`${user.name} 名稱`} readOnly />
            <input
              type="email"
              defaultValue={user.email ?? ""}
              placeholder="信箱（選填）"
              aria-label={`${user.name} 信箱`}
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
              指派 {graph ? assigneeUsage(graph, user.id).length : 0} 個節點
            </small>
          </li>
        ))}
      </ul>
      {users.length === 0 && (
        <EmptyState
          title="尚無成員"
          description="在節點的「負責人」欄輸入名字即可建立。"
        />
      )}
    </section>
  );
}
