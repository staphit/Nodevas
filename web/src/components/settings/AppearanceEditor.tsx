import { useApp } from "../../store";
import { confirmAction } from "../ConfirmDialog";
import type { SettingsNotify } from "./notify";

/** Local preferences only — nothing here reaches the project files. */
export function AppearanceEditor({ notify }: { notify: SettingsNotify }) {
  const resetUIPreferences = useApp((s) => s.resetUIPreferences);
  const theme = useApp((s) => s.theme);
  const toggleTheme = useApp((s) => s.toggleTheme);

  return (
    <section className="settings-section">
      <p className="settings-hint">
        以下設定只存在這台機器的瀏覽器，不會寫進專案檔案。
      </p>
      <div className="settings-row">
        <span>主題</span>
        <button type="button" onClick={toggleTheme}>
          {theme === "dark" ? "切換為淺色" : "切換為深色"}
        </button>
      </div>
      <div className="settings-row">
        <span>版面</span>
        <button
          type="button"
          onClick={() => {
            resetUIPreferences("layout");
            notify.onNotice("已重設視窗版面");
          }}
        >
          重設視窗比例與抽屜寬度
        </button>
        <button
          type="button"
          onClick={() => {
            resetUIPreferences("timeline");
            notify.onNotice("已恢復時間軸自動格寬");
          }}
        >
          恢復時間軸自動格寬
        </button>
      </div>
      <div className="settings-row">
        <span>全部</span>
        <button
          type="button"
          className="danger"
          onClick={async () => {
            const confirmed = await confirmAction({
              title: "重設全部本機偏好",
              description:
                "主題、視窗比例、專案總管寬度、時間軸設定都會回到預設值。專案資料不受影響。",
              confirmLabel: "重設",
              tone: "danger",
            });
            if (!confirmed) return;
            resetUIPreferences();
            notify.onNotice("已重設全部本機偏好");
          }}
        >
          重設全部本機偏好
        </button>
      </div>
    </section>
  );
}
