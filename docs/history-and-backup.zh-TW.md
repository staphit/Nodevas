# 版本歷史與備份

English: [history-and-backup.md](./history-and-backup.md)

## 版本歷史：每個專案一個 git repo

每個專案的歷史存在它自己的 git repo 裡，位置是 `<專案>/.vised/git`，工作目錄就是專案資料夾本身。這個 repo 和你自己的 repo 完全分開。專案資料夾也可以放在某個程式 repo 裡：Nodevas 不會在那個 repo 裡 commit，還會寫一個 `.vised/.gitignore`，讓那個 repo 忽略 Nodevas 自己的狀態（包括這個歷史 repo）。至於專案本身的檔案要不要 commit，仍然由你決定。

需要 git 2.32 以上：從這個版本起，Nodevas 才能完全不套用你機器上的 git 設定。git 版本較舊時，Nodevas 會改用檔案副本，並在 log 裡說明原因。

- **記錄哪些檔案**：`graph.yaml`、每個節點的文件，以及節點的子頁。Nodevas 自己的狀態（`.vised/`）和執行紀錄（`run/`）不記錄。
- **什麼時候 commit**：
  - 一段編輯中，檔案第一次被修改時，被覆蓋掉的那個版本會立刻 commit。這通常就是你之後想找回的版本。
  - 之後的存檔，每個專案每 5 分鐘合併成一個 commit。
  - 還原、刪除、轉換頁面格式時，會立刻 commit；關閉伺服器時也會。
- **作者是誰**：每個 commit 的作者是存檔的人或 agent。同一個 5 分鐘內其他存過檔的人，會列為 `Co-authored-by`。在 Nodevas 以外改的內容（例如別的編輯器、同步工具），作者記為 `Nodevas`。
- **在哪裡看**：節點面板的「歷程」會列出每個版本的時間和作者，可以預覽和還原。還原本身也是一個新的 commit，所以還原也能再復原。
- **升級時**：專案第一次改用 git 時，原本 `.vised/history/` 裡的舊副本會照原本的順序和時間匯入成 commit。舊副本會保留，不會刪除。
- **沒有 git 時**：
  - 伺服器沒裝 git，就沿用原本的做法，在 `.vised/history/` 存檔案副本。
  - Docker image 已經內建 git。
  - 設定環境變數 `NODEVAS_HISTORY=files` 會強制使用檔案副本；`NODEVAS_HISTORY=git` 則一定要用 git。不設或設為 `auto` 時，有 git 就用 git。

也可以直接用一般的 git 指令看某個專案的歷史：

```bash
git --git-dir=<專案>/.vised/git log --stat
```

Nodevas 執行 git 時，完全不套用這台機器的 git 設定：不讀全域設定，也不跑 hook、不簽章、不使用密碼輔助程式。

垃圾桶仍然在 `.vised/trash/` 另外保留副本。

## 備份到 git 遠端

管理員可以在「設定 → 備份與歷史」裡選擇「Git 遠端」。之後每個專案的歷史，會推到同一個遠端 repo（例如 GitHub 私人 repo）裡各自的分支 `nodevas/<專案>`。

- **網址**：只接受 HTTPS。請使用對該 repo 有寫入權限的 token。使用者名稱預設是 `x-access-token`，GitHub 的 token 可以直接用。
- **token**：
  - 加密存在伺服器上，不放在 workspace 裡，存好之後也不會再顯示。
  - 只透過環境變數交給 git，不會出現在指令列或網址裡。
  - 錯誤訊息裡會被遮蔽。
- **目的地限制**：伺服器如果不是只開給本機，就會擋掉內網、本機和鏈路本地位址。每次推送前都會重新檢查；git 2.37 以上還會把連線鎖定在檢查過的位址。
- **推送時機**：照備份排程推送，也可以按「立即備份」。絕不強制推送。
- **遠端分支被別人推過時**：那個專案會暫停推送，並標示為「分歧」，其他專案照常推送。按「保留本機」會把你的歷史推到另一條分支。
- **測試連線**：第一次備份前，可以先確認網址和 token 是否正確。

原本的資料夾備份和 `.veproj` 匯入／匯出都照舊可以用。
