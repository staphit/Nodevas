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
5. 從 `bootstrap` service log 取出首次產生的 PIN。PIN 只顯示一次，請透過可信任管道保存。

首次初始化成功後，`/data/.nodevas-bootstrap-complete` 會避免更新或重啟時重設 PIN。若需重設，請使用 `nodevas user pin`，不要刪除 marker 後盲目重啟。

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

參考：

- [TrueNAS：Install via YAML](https://www.truenas.com/docs/scale/scaleuireference/apps/installcustomappscreens/)
- [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/)
- [Cloudflare Tunnel firewall](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/)
