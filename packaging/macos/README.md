# macOS 發行

macOS 桌面版以 Electron 顯示既有介面，並在背景啟動內嵌的 Go 服務。專案資料預設儲存在：

```text
~/Library/Application Support/Nodevas/workspace
```

## testflight v1.0

- Git tag：`testflight-v1.0`
- 顯示版本：`testflight v1.0`
- macOS / Electron 版本：`1.0.0`
- 更新頻道：`testflight`
- 安裝檔：`Nodevas-testflight-v1.0-macOS-universal.dmg`
- 架構：Universal，同時支援 Apple Silicon 與 Intel Mac

把專案推到 GitHub 後建立並推送 tag：

```bash
git tag testflight-v1.0
git push origin testflight-v1.0
```

GitHub Actions 會建置 Universal `.app`、DMG、更新用 ZIP、更新描述檔與 SHA-256，並建立 prerelease。之後使用 `testflight-v1.1`、`testflight-v1.2` 等 tag 即可延續相同更新頻道。

也可直接在 Mac 上本機建置：

```bash
bash packaging/macos/build.sh testflight-v1.0
```

輸出會放在 `desktop/dist/`。Windows 無法產生真正的 DMG，因為封裝與簽章依賴 macOS 的 `hdiutil`、`codesign` 與 `lipo`。

## 簽章與自動更新

未設定 Apple 憑證時仍可產生 DMG，但 macOS 會顯示未識別開發者警告，Electron 自動更新也不保證可安裝。正式發佈請在 GitHub repository secrets 設定：

| Secret | 用途 |
|---|---|
| `MAC_CSC_LINK` | Developer ID Application 的 `.p12`，可使用 base64 或安全下載 URL |
| `MAC_CSC_PASSWORD` | `.p12` 密碼 |
| `APPLE_ID` | 公證用 Apple ID |
| `APPLE_APP_SPECIFIC_PASSWORD` | Apple app-specific password |
| `APPLE_TEAM_ID` | Apple Developer Team ID |

簽章完成後，應用程式會在啟動後與每六小時檢查一次 testflight 更新，也可從 macOS 應用程式選單手動執行「檢查更新…」。
