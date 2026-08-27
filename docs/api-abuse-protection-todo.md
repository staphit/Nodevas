# API 惡意流量防護 TODO

## 目標

即使外部使用者已知道所有 API routing，或已取得自己的合法 session，也不能用大量 HTTP／WebSocket 請求耗盡 Nodevas 的 CPU、記憶體、檔案 I/O、SQLite、SMTP 或遠端同步資源。

這份清單只列能在本專案內落地、測試及維護的工作。隱藏 API URL、混淆 JavaScript、禁止 F12 不屬於安全邊界，也不列入實作。

## 現有基線（保留，不需重做）

- [x] `internal/server/abuse.go` 已限制每位 actor 的寫入請求：30 req/s、burst 60。
- [x] `internal/server/abuse.go` 已限制 search、export、import、remote 等昂貴路由：4 req/s、burst 8，並限制同時執行數。
- [x] 登入與 OTP 已有 global、IP、帳號等不同維度的限流。
- [x] WebSocket 已限制 global／actor／IP 連線數、frame 大小、訊息數及 payload bytes。
- [x] JSON、附件、import 已有 request body 大小上限。
- [x] HTTP server 已設定 `ReadHeaderTimeout`、`IdleTimeout`、`MaxHeaderBytes`，一般 request 另有 read/write deadline。
- [x] 超過現有限制時會回傳 `429` 與 `Retry-After`。

## P0：必須先完成

### 1. 增加驗證前的 IP／global limiter

- [x] 在 `internal/server/abuse.go` 新增獨立的 pre-auth limiter，不與登入／OTP limiter共用額度。
- [x] limiter 僅涵蓋 `/api/*` 與 `/ws` upgrade；SPA、JS、CSS、圖片等靜態資源不計入。
- [x] 同一請求同時扣除 global bucket 與來源 IP bucket；任一不足即拒絕。
- [x] IP 一律由 `auth.ClientIP(request)` 取得，不直接信任 `X-Forwarded-For`。
- [x] bucket map 必須有 TTL、最大 entries 與清理策略，不能被大量來源填滿記憶體。
- [x] 在 `internal/server/routes.go` 將 middleware 放在 `secureHTTP` 之後、`withAuth` 之前，讓未登入洪水不必進入 session 驗證。
- [x] `/api/auth/login` 與 `/api/auth/otp/request` 仍需通過既有專用 limiter；pre-auth limiter 是外層總預算，不取代既有防爆破限制。
- [x] 被拒絕時回傳 `429`、固定格式錯誤及 `Retry-After`，且不洩漏 limiter key 或內部狀態。

驗收條件：

- [x] 無 Cookie 的 `/api/graph` 洪水會在 session 查詢前被拒絕。
- [x] 偽造 `X-Forwarded-For` 無法輪替 limiter bucket。
- [x] 經設定的 trusted proxy 時，真正的 client IP 仍能正確分桶。
- [x] 超過最大 bucket entries 時記憶體保持有界，服務不 panic。
- [x] 一般 SPA 首頁及靜態檔案不受 API limiter 影響。

預計修改：

- `internal/server/abuse.go`
- `internal/server/routes.go`
- `internal/server/abuse_test.go`
- `internal/server/auth_test.go`
- 必要時補 `internal/auth/auth_test.go`

### 2. 所有一般讀取 API 都套用 actor + IP + global 限流

- [x] 為 `/api/*` 的 GET／HEAD 建立預設 read budget，不再只限制 `expensivePath`。
- [x] 已登入請求同時扣除 actor、IP、global 三個 bucket。
- [x] local-only 模式仍要有 global budget；不能因 actor 是固定 local identity 而完全略過。
- [x] public auth endpoints 由 pre-auth／auth 專用 bucket 管理，避免重複扣除造成正常登入失敗。
- [x] `429` 必須附 `Retry-After`，前端不可無限立即重試。
- [x] 檢查 MCP client、前端 refetch/coalescing 與 WebSocket reconnect，確認遇到 429 會退避而不是放大流量。

至少覆蓋以下現有路由：

- [x] `/api/graph`
- [x] `/api/state`
- [x] `/api/nodes/:id`
- [x] `/api/nodes/:id/pages/*`
- [x] `/api/history` 與 `/api/history/version`
- [x] `/api/projects` 與跨專案讀取
- [x] `/api/search`
- [x] `/api/audit`、notify、remote 等管理讀取

驗收條件：

- [x] 同一 actor 換 IP 仍受 actor limit。
- [x] 同一 IP 使用多帳號仍受 IP limit。
- [x] 多 actor、多 IP 同時發送仍受 global limit。
- [x] 一般 GET 超限會回 429；正常 UI 啟動、切換專案與開啟節點不會誤觸。

預計修改：

- `internal/server/abuse.go`
- `internal/server/abuse_test.go`
- `web/src/api/http.ts`（只在需要統一 429 行為時修改）
- `internal/mcp/client.go`（確認既有有限次退避適用新增的 429）

### 3. 將二元 expensive 判斷改成 route cost 分級

- [x] 定義集中式 request class／cost 表，至少包含 `public-auth`、`read`、`write`、`expensive`、`heavy`、`ws-upgrade`。
- [x] 未知的新 `/api/*` 路由採安全預設值，不得因未列入表內就完全不限流。
- [x] token bucket 支援一次扣除大於 1 的 cost。
- [x] search、history content、graph validation、export/import、DOCX、remote sync、mail test 使用較高 cost 或獨立 quota。
- [x] 保留既有 semaphore；rate limit 控制頻率，semaphore 控制同時執行數，兩者不可互相取代。
- [x] 在 route registration test 中檢查所有已註冊 API 都能被分類。

驗收條件：

- [x] 新增 API 但未明確分類時，測試失敗或自動落入受限的預設 class。
- [x] heavy request 能比普通 read 更快耗盡 budget。
- [x] 一個已飽和的 heavy pool 不會阻塞普通健康檢查或輕量讀取。

預計修改：

- `internal/server/abuse.go`
- `internal/server/routes.go`
- `internal/server/abuse_test.go`
- 各 `internal/httpapi/*/routes.go`（僅在採用顯式 route metadata 時）

### 4. 限制 WebSocket upgrade 洪水

- [x] `/ws` upgrade 嘗試在進入 websocket.Accept 前套用 global + IP attempt limiter。
- [x] 保留既有 global 128、per-actor 8、per-IP 16 個存活連線上限。
- [x] 驗證失敗、無效 Origin、超限 upgrade 都需快速釋放 reservation。
- [x] 前端 reconnect 使用 exponential backoff 加 jitter，並設定最大等待時間。
- [x] server policy close 後，前端不可固定頻率永遠重連。

驗收條件：

- [x] 同一 IP 快速建立／斷開連線會收到 429。
- [x] 多 IP upgrade 洪水仍受 global attempt limit。
- [x] 正常斷線重連可以恢復，且不會形成同步重連尖峰。
- [x] reservation、actorConns、ipConns 在所有失敗分支都回到原值。

預計修改：

- `internal/server/abuse.go`
- `internal/realtime/hub.go`
- `internal/realtime/hub_http.go`
- `internal/realtime/hub_test.go`
- `web/src/api/ws.ts`

## P1：穩定性與可營運性

### 5. 讓 limiter 參數可設定但有安全範圍

- [x] 在 server config 增加 rate、burst、global concurrency 等設定。
- [x] 預設值維持安全，設定值必須驗證上下限；`0` 不得意外代表完全停用。
- [x] CLI、YAML、environment 的 precedence 延續現有 config 規則。
- [x] 啟動時記錄生效的 limiter 數值，但不得記錄密鑰或 Cookie。
- [x] 文件說明只有受信任管理員能修改這些設定。

預計修改：

- `internal/config/*`
- `cmd/nodevas/serve.go`
- `internal/server/server.go`
- `internal/server/abuse.go`
- config 與 CLI 測試

### 6. 擴充昂貴操作的 concurrency gate

- [x] 量測 graph/state/history/node/search 各 handler 的 CPU、SQLite、檔案 I/O 成本。
- [x] 為實測昂貴的讀取加入獨立或共享 semaphore。
- [x] 排隊策略採 fail-fast；滿載時回 429/503 與 `Retry-After`，不建立無界等待隊列。
- [x] request context 取消時立即釋放 slot。
- [x] 不讓 export、DOCX、remote sync 共用一個會互相餓死的單一 pool，除非量測證明合理。

驗收條件：

- [x] 壓力下 goroutine、記憶體與 SQLite 等待數保持有界。
- [x] client 中止 request 後 slot 不會洩漏。
- [x] heavy pool 飽和時一般讀取仍可完成。

### 7. 增加可觀測性與告警訊號

- [x] 每次 429 記錄 limiter class、route template、actor 類型、可信 client IP 與 retry seconds。
- [x] 不將完整 query、request body、Cookie、CSRF、PIN、OTP 寫入 log。
- [x] 對重複 401/403/404、429、WebSocket policy close 增加可聚合的事件名稱。
- [x] 防止攻擊期間每個 request 都產生高成本或無界 log；加入 sampling 或每 key/window 聚合。
- [x] 增加 admin-only 的 limiter health/summary，或提供可被 metrics collector 讀取的內部介面。
- [x] 建立短期封鎖功能前，先提供人工可判讀的證據；不要自動永久封鎖 IP。

預計修改：

- `internal/logging/*`
- `internal/server/abuse.go`
- `internal/server/middleware.go`
- 必要時新增 `internal/httpapi/system` 的 admin-only endpoint

### 8. 建立 deployment-layer 防護範例

- [x] 在 `deploy/` 或 `docker/` 提供 reverse proxy 範例，Nodevas 不直接暴露到公網。
- [x] 範例包含 TLS、每 IP request/connection limit、WebSocket upgrade、body/header 大小及 timeout。
- [x] proxy 到 Nodevas 時使用固定內網／loopback listener。
- [x] 清楚設定 `--behind-proxy` 與 `--trusted-proxy`；不可使用任意來源 CIDR。
- [x] 文件說明大型 volumetric DDoS 必須由上游 WAF/CDN/供應商處理，應用程式 limiter 不能取代網路層清洗。
- [x] 提供驗證步驟，確認偽造 forwarding headers 不會改變 `auth.ClientIP`。

預計修改：

- `deploy/` 或 `docker/` 下的範例設定
- `docs/getting-started.md` 或新增 security deployment 文件

## P2：規模擴大時再做

### 9. 多 instance 共用 limiter

- [ ] 只有在 Nodevas 支援多 instance／水平擴展後才啟動此項。
- [ ] 抽象 limiter storage，保留單機 in-memory implementation。
- [ ] 新增共享 atomic token bucket／sliding window implementation。
- [ ] actor、IP、global budget 在不同 instance 間一致。
- [ ] 共享 limiter 故障時採明確策略：敏感／heavy route fail-closed，必要輕量讀取可依風險決定降級。
- [ ] 測試 clock skew、storage timeout、partial outage 與 retry storm。

## 測試清單

### 單元測試

- [x] token refill、burst、cost > 1、時間倒退及零 elapsed。
- [x] global、IP、actor 任一 bucket 耗盡即拒絕。
- [x] bucket TTL cleanup 與最大 entries。
- [x] trusted proxy／偽造 forwarding header。
- [x] `Retry-After` 與固定錯誤格式。
- [x] route class 完整性與未知 API 的安全預設。
- [x] semaphore 在成功、error、panic recovery、context cancel 時都釋放。

### 整合測試

- [x] 未登入 API flood 在 auth 前被拒絕。
- [x] 同 actor 多 IP、同 IP 多 actor、多 actor 多 IP 三種情境。
- [x] visitor、member、admin 各自的正常 request 不互相共用 actor bucket。
- [x] GET graph/node/history 超限。
- [x] write 與 expensive limiter 保留既有行為。
- [x] WebSocket upgrade、存活連線、訊息與 byte budget。
- [x] 429 後前端／MCP client 會有限次退避，不形成 retry storm。

### 負載與回歸測試

- [x] 記錄正常 UI cold start、切換專案、開啟節點、多人協作的峰值 request rate。
- [x] 初始 limiter 門檻至少容納正常實測尖峰與合理裕度，再以攻擊測試確認能保護資源。
- [x] 壓力測試期間記錄 CPU、RSS、goroutine、open files、SQLite latency 與 429 比例。
- [x] 執行 Go tests、Web tests、Playwright 核心流程。
- [x] 確認限流狀態不會隨失敗 request 無界成長。

## 完成定義

- [x] 知道 routing 的未登入攻擊者會先被 IP/global edge budget 擋下。
- [x] 持有合法 session 的惡意使用者仍受 actor/IP/global 與 route cost 限制。
- [x] 一般 GET、昂貴 HTTP 操作與 WebSocket 都有獨立且有界的防護。
- [x] 任何新 API 都不會因忘記登記而變成 unlimited。
- [x] limiter 不信任未經核准 proxy 提供的 forwarding headers。
- [x] 429 可觀測但不造成 log amplification 或洩漏敏感資料。
- [x] 正常 UI、MCP 與協作流程通過回歸測試。
- [x] 公網部署文件明確要求 reverse proxy/WAF；服務本身不宣稱能抵擋頻寬型 DDoS。
