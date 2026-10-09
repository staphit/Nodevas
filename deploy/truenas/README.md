# TrueNAS SCALE 與 Cloudflare Tunnel 部署

此部署以 Docker Compose 啟動 Nodevas、首次帳號初始化服務與 `cloudflared`。Nodevas 不發布 host port；外部流量只能經 Cloudflare Tunnel 進入。

## 前置條件

- TrueNAS SCALE 24.10 或更新版本。
- 可管理 DNS 的 Cloudflare 網域。
- 可用的 SMTP relay。Nodevas 網頁登入需要寄送一次性驗證碼。
- GHCR 上可拉取的 `ghcr.io/staphit/nodevas` 映像。首次發佈後，將 package 設為 public，或在 TrueNAS 設定 registry credentials。

## 1. 建立資料與 secret

以下範例使用 pool `tank`。如使用其他 pool，請同步修改 [`compose.yaml`](./compose.yaml) 的所有 `/mnt/tank` 路徑。

```bash
sudo install -d -m 0700 -o 568 -g 568 \
  /mnt/tank/apps/nodevas/workspace \
  /mnt/tank/apps/nodevas/secrets

printf '%s' 'REPLACE_WITH_A_LONG_ADMIN_PASSWORD' | \
  sudo tee /mnt/tank/apps/nodevas/secrets/admin-password >/dev/null
printf '%s' 'REPLACE_WITH_SMTP_PASSWORD' | \
  sudo tee /mnt/tank/apps/nodevas/secrets/smtp-password >/dev/null
printf '%s' 'REPLACE_WITH_CLOUDFLARE_TUNNEL_TOKEN' | \
  sudo tee /mnt/tank/apps/nodevas/secrets/cloudflare-tunnel-token >/dev/null

sudo chown 568:568 /mnt/tank/apps/nodevas/secrets/*
sudo chmod 0400 /mnt/tank/apps/nodevas/secrets/*
```

不要把 secret 寫入 Compose YAML、Git 或 shell history。上列 `printf` 僅示範檔案格式；實際部署請使用不記錄輸入的 secret 管理方式。

## 2. 建立 Cloudflare Tunnel

1. 在 Cloudflare Zero Trust 建立 remotely-managed Tunnel。
2. 複製 Tunnel token，寫入 `cloudflare-tunnel-token`。
3. 新增 Public Hostname，例如 `nodevas.example.com`。
4. Service type 選 `HTTP`，URL 設為 `http://nodevas:5666`。

Tunnel 為 outbound-only。路由器不需 port forwarding。防火牆需允許 TrueNAS 對外連線至 Cloudflare Tunnel 使用的 TCP/UDP 7844。

## 3. 調整 Compose

編輯 [`compose.yaml`](./compose.yaml)：

- 將所有 `/mnt/tank` 改為實際 pool 路徑。
- 將 `NODEVAS_ADMIN_EMAIL` 改為管理員信箱。
- 將 `NODEVAS_SERVE_HOSTNAME` 改為 Public Hostname。
- 填入 SMTP host、port、user、from、security。
- 若 `172.31.250.0/24` 與現有網路重疊，整組更換 subnet 與三個固定 IP；`NODEVAS_SERVE_TRUSTED_PROXY` 必須保持為 `cloudflared` 的 `/32` IP。
- 正式環境建議把 `latest` 換成固定版本 tag 或 digest。

`NODEVAS_SERVE_ALLOW_PLAINTEXT=true` 只允許 Compose 私有 bridge 上的 Nodevas 到 `cloudflared` HTTP hop。不要新增 `ports:`，否則會把 origin 暴露到 TrueNAS host 或 LAN。

## 4. 安裝 Custom App

1. TrueNAS：`Apps` > `Discover Apps` > 選單 > `Install via YAML`。
2. App name 輸入 `nodevas`。
3. 貼上調整後的 [`compose.yaml`](./compose.yaml)。
4. 儲存並等待 `bootstrap` 完成、`nodevas` healthy、`cloudflared` running。
5. 開啟 Public Hostname，在登入頁輸入 `NODEVAS_ADMIN_EMAIL` 設定的信箱，收到 8 碼一次性驗證碼（5 分鐘內有效、只能使用一次）後輸入，即完成登入。

網頁登入只有一個因素：持有該信箱。能讀取信箱的人就能登入，請保護好管理員信箱，並使用已啟用兩步驟驗證的信箱。登入成功會登出該帳號其他工作階段，只保留最新的裝置。

首次初始化成功後，`/data/.nodevas-bootstrap-complete` 會避免更新或重啟時再次設定帳號。日後修改 `NODEVAS_ADMIN_EMAIL` 不會自動套用；若需更換信箱，請使用 `nodevas user email`（會結束該帳號現有工作階段），不要刪除 marker 後盲目重啟。

### 已有 cloudflared App 時

若 TrueNAS 已安裝社群版 `cloudflared` App，改用 [`compose.existing-tunnel.yaml`](./compose.existing-tunnel.yaml)：Nodevas 加入該 App 的 `ix-cloudflared_default` bridge network，不另跑 `cloudflared`，也不需要 `cloudflare-tunnel-token`。

- 以 `docker network inspect ix-cloudflared_default` 確認 subnet，`NODEVAS_SERVE_TRUSTED_PROXY` 設為該 subnet（`cloudflared` IP 不固定）。
- Cloudflare Tunnel 的 Published application route：`HTTP`、`nodevas:5666`。
- 此網路由 `cloudflared` App 建立；該 App 停止或重建網路後，需重新啟動 Nodevas App。

## 5. 驗證

```bash
curl -fsS https://nodevas.example.com/ >/dev/null
```

登入後確認：

- 瀏覽器只使用 HTTPS。
- WebSocket `/ws` 可保持連線。
- Nodevas request log 的 client IP 不是 `cloudflared` container IP。
- Router 與 TrueNAS 沒有對外發布 5666。

## 備份與更新

備份整個 `/mnt/tank/apps/nodevas/workspace` dataset。它包含專案、SQLite/WAL、歷史、CRDT sidecar、通知 secret 與 `/data/.config` 下的加密 master key。

更新固定 image tag 後重新部署 App。Nodevas 收到 `SIGTERM` 時會先 graceful shutdown；Compose 提供 45 秒停止期限。

從 PIN 登入版本升級時，遷移會清除所有 PIN，並登出所有現有工作階段一次。已設定 email 的帳號可立即改用 email 登入；若多個帳號使用相同 email（不分大小寫），只有最早建立的帳號保留該 email，其餘需以 `nodevas user email` 重新設定。

參考：

- [TrueNAS：Install via YAML](https://www.truenas.com/docs/scale/scaleuireference/apps/installcustomappscreens/)
- [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/)
- [Cloudflare Tunnel firewall](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/)
