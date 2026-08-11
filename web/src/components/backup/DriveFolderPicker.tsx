import { IconFolder } from "../../icons";
import type { DriveConnection } from "./useDriveConnection";

/**
 * One level of the Drive tree at a time. Only folders are listed: this picker
 * chooses where backups go, and a file is never an answer to that.
 */
export function DriveFolderPicker({ drive }: { drive: DriveConnection }) {
  return (
    <div className="drive-folder-picker">
      <div className="drive-folder-picker-head">
        <button
          type="button"
          disabled={drive.busy || drive.stack.length === 0}
          onClick={drive.back}
        >
          上層
        </button>
        <button type="button" disabled={drive.busy} onClick={drive.toRoot}>
          根目錄
        </button>
        <span>{drive.label}</span>
      </div>
      <ul className="drive-folder-picker-list">
        {drive.busy && <li className="drive-folder-picker-empty">讀取中…</li>}
        {drive.folders.map((folder) => (
          <li key={folder.id}>
            <button
              type="button"
              disabled={drive.busy}
              onClick={() => drive.enter(folder)}
            >
              <IconFolder size={13} />
              {folder.name}
            </button>
          </li>
        ))}
        {!drive.busy && drive.folders.length === 0 && (
          <li className="drive-folder-picker-empty">沒有可用子資料夾</li>
        )}
      </ul>
    </div>
  );
}
