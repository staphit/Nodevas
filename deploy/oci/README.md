# 部署到 Oracle Cloud Always Free

這套設定會建立一台 Ubuntu 24.04 VM、保留型公網 IPv4、VCN、防火牆、50 GB 資料磁碟及私有 Object Storage bucket。Caddy 負責 HTTPS，Nodevas 只監聽 `127.0.0.1:5666`。資料庫每 8 小時備份一次。

目前 OCI Always Free 的 A1 總額是每個 tenancy 共 2 OCPU、12 GB RAM；block/boot volume 共 200 GB；Object Storage 共 20 GB。Terraform 只能檢查本部署的用量，不能替你計算 tenancy 裡既有資源。套用前仍要在 OCI Console 檢查總用量。

## 先決條件

- Terraform 1.5 以上
- OCI CLI 瀏覽器 Security Token（互動部署建議）或 API key
- tenancy 的 home region
- 可在 root compartment 建立 dynamic group 與 IAM policy 的帳號
- SSH 公鑰
- SMTP relay；OCI 新 tenancy 通常封鎖 outbound 25，請用 587/STARTTLS 或 465/TLS

自動 bootstrap 必須建立 instance principal IAM。`create_instance_principal_iam` 必須保持 `true`。

短效登入：

```powershell
oci session authenticate --region ap-osaka-1 --profile-name NODEVAS
oci session validate --profile NODEVAS --auth security_token
```

並在 `terraform.tfvars` 設定 `auth_method = "SecurityToken"` 與 `config_file_profile = "NODEVAS"`。Token 約一小時失效；後續套用前執行 `oci session refresh --profile NODEVAS`。

## 1. 建立基礎設施

```bash
cd deploy/oci/terraform
cp terraform.tfvars.example terraform.tfvars
# 編輯 terraform.tfvars，填入所有必填值
terraform init
terraform fmt -check
terraform validate
terraform plan
terraform apply
terraform output app_url
terraform output ssh_command
```

預設 A1 是 `VM.Standard.A1.Flex`、2 OCPU、12 GB。若遇到 `Out of host capacity`，先換 availability domain；仍失敗再改成 `VM.Standard.E2.1.Micro`。切換 shape 後也要把 GitHub repository variable `NODEVAS_DEPLOY_ARCH` 改成對應的 `arm64` 或 `amd64`。

Terraform 會先把 bootstrap 檔案上傳到私有 bucket，再啟動 VM。cloud-init 會：

- 安裝 Caddy、OCI CLI、SQLite、jq、iSCSI 與 XFS 工具
- 等待 Terraform 完成 IAM、資料磁碟及 reserved IP 附加
- 格式化並掛載資料磁碟到 `/var/lib/nodevas`
- 安裝 systemd units、部署/備份/還原/日誌腳本
- 開放主機防火牆的 80/443
- 啟動 Caddy 與備份 timer

等待完成：

```bash
ssh ubuntu@<terraform output public_ip>
sudo cloud-init status --wait
sudo tail -n 200 /var/log/nodevas-bootstrap.log
```

成功時網站已有 HTTPS，但在第一個帳號建立前會回 502；這是預期狀態。

## 2. 安裝第一個 Nodevas binary

最簡單方式是在 `ssh_allowed_cidr` 允許的電腦 cross-compile、上傳 binary，再執行：

```bash
sudo nodevas-deploy.sh --binary /path/to/nodevas --sha256 <sha256>
```

如要使用 `.github/workflows/deploy.yml`，deploy job 刻意要求標籤為 `nodevas-deploy` 的 Linux self-hosted runner；該 runner 的固定出口 IP 必須是 `ssh_allowed_cidr`。不要為 GitHub hosted runner 把 SSH 開成 `0.0.0.0/0`。設定 GitHub Environment `production` 與以下 secrets：

- `DEPLOY_SSH_KEY`
- `DEPLOY_HOST`
- `DEPLOY_USER`：Ubuntu 映像請填 `ubuntu`
- `DEPLOY_SSH_KNOWN_HOSTS`

另設 repository variable `NODEVAS_DEPLOY_ARCH=arm64`；E2 micro 則用 `amd64`。

第一次執行 workflow 只安裝 binary，不啟動 Nodevas，因為此時還沒有帳號。後續部署會自動 restart、HTTP health check，失敗時回滾。

## 3. SMTP、第一個管理員、啟動

```bash
sudoedit /etc/nodevas/nodevas.env
# 修改 NODEVAS_SMTP_HOST/PORT/SECURITY/USER/FROM/PASSWORD

sudo -u nodevas env XDG_CONFIG_HOME=/var/lib/nodevas/config \
  /usr/local/bin/nodevas user add \
  --project /var/lib/nodevas/workspace --user ann \
  --role admin --password-stdin

sudo -u nodevas env XDG_CONFIG_HOME=/var/lib/nodevas/config \
  /usr/local/bin/nodevas user pin \
  --project /var/lib/nodevas/workspace --user ann \
  --email ann@example.com

sudo systemctl start nodevas
sudo systemctl status nodevas caddy --no-pager
```

PIN 只顯示一次。不要用同一個 email 傳 PIN；否則 PIN 與一次性驗證碼落在同一信箱，失去第二因素。

## 4. 從 macOS App 連接 OCI

Terraform 完成後，用 `terraform output -raw app_url` 取得 HTTPS URL。packaged release 不會
內建特定部署的 URL；啟動時把 Terraform 輸出的網址指定給 App：

```bash
open -a "Nodevas" --args --server-url "$(terraform output -raw app_url)"
```

也可以先設定 `NODEVAS_SERVER_URL` 再開啟 App。遠端模式會略過本機 binary 與資料夾選擇器，
直接載入 OCI 的登入頁；工作區與所有檔案都留在 OCI。URL 必須是 Caddy 提供的 HTTPS 網址，
不要把 SSH 位址或 `127.0.0.1:5666` 填進去。

## 5. 備份與還原

Timer 在每天 00:17、08:17、16:17 執行，另有最多 30 分鐘固定隨機延遲：

```bash
systemctl status nodevas-backup.timer
sudo systemctl start nodevas-backup.service
sudo journalctl -u nodevas-backup -n 100 --no-pager
```

每次資料庫備份使用新物件名。Instance principal 可 inspect/read/create object，但不能 overwrite/delete；VM 被入侵時仍不能破壞既有備份。bucket lifecycle 會刪除過期的 `db/` 與 `logs/` 物件。

先演練還原到暫存路徑：

```bash
sudo nodevas-restore.sh --bucket <terraform output backup_bucket> \
  --db /tmp/nodevas-restore-test.db --unit nodevas-restore-rehearsal --yes
```

正式還原：

```bash
sudo systemctl stop nodevas
sudo nodevas-restore.sh --bucket <terraform output backup_bucket>
sudo systemctl start nodevas
```

## 5. 操作與除錯

```bash
nodevas-logs tail
nodevas-logs errors
nodevas-logs slow
sudo journalctl -u nodevas -n 100 --no-pager
sudo journalctl -u caddy -n 100 --no-pager
sudo cloud-init status --long
```

### Audit fallback 對帳

Audit 採 availability-first：內容異動成功後，若 `audit_events` 暫時無法寫入，請求仍會成功，event 會以 error-level structured log 保存。監控應辨識以下穩定 event code；fallback 應告警，recovered／acknowledged 用來追蹤處置進度：

```text
audit.persistence_fallback
audit.persistence_recovered
audit.reconciliation_acknowledged
```

管理員可查 `GET /api/audit/health`；`writeStatus` 表示 DB 寫入目前是否恢復，`unreconciledEvents` 表示仍待人工對帳的 fallback。只有兩者都正常才回 200；write 已恢復但尚未確認對帳仍回 503。Health 是 process-local，重啟會清除，不能取代 journald、log shipper 或備份。OCI 備份工作會依 journal cursor 把 error logs 封存到 `logs/`。

收到 fallback 告警時：

1. 先保留 `nodevas-logs errors` 與 bucket 中對應時段的 `logs/errors-*.jsonl.gz`。
2. 修復 SQLite／磁碟問題；下一次成功稽核寫入會產生 `audit.persistence_recovered` 並讓 `writeStatus` 恢復，但整體 health 仍保持 degraded。
3. 以 log 內的 `audit_at`、`audit_project`、`audit_actor_*`、`audit_action`、`audit_target` 與 `audit_client_ip` 對照同一時段的 `audit_events`，把只存在 log 的 event 納入事故紀錄。
4. 重新抓 health，確認 `writeStatus=healthy`，再以同一份 response 的 counter 呼叫 `POST /api/audit/health/acknowledge`：`{"expectedFallbackEvents": N}`。成功會記 `audit.reconciliation_acknowledged` 並恢復 200；409 表示寫入仍故障或對帳期間出現新的 fallback，重新抓 health 並把新事件納入對帳。Acknowledgement request 本身也要寫 audit DB；若那次寫入失敗，health 會立即再次 degraded。

Fallback 不會自動 backfill，這套 trail 也不宣稱是與 domain mutation 原子提交的 compliance-grade ledger。

資料磁碟受 Terraform `prevent_destroy` 保護；`terraform destroy` 預設會因此失敗。確認已另有可還原備份後，才可暫時移除該保護並明確刪除 volume。

## 安全基線

- 公網只開 80/443；SSH 預設只允許指定 `/32`。Nodevas 僅綁定 loopback，由 Caddy 終結 TLS。
- VM 用 instance principal 存取私有 bucket；無 API key 落地。權限只有 inspect/read/create，不能覆寫或刪除既有備份。
- systemd 啟用 non-root、檔案系統及核心保護；部署驗證 SHA-256、健康檢查失敗會回滾。
- Terraform state 含資源識別資訊，視為敏感檔；勿提交 Git，正式團隊環境應移到有加密與鎖定的遠端 backend。
- `sslip.io` 適合快速啟用，不是完整 production DNS 控制。正式服務建議自有網域、DNSSEC（供應商支援時）及外部可用性監控。
- Always Free 沒有高可用保證；單 VM、單 AD 仍是故障點。Object Storage 備份要定期演練還原，重要資料另做跨區或離線副本。
- Visitor 是 view access，不是 copy protection：持有共享 credential 的人可以複製或儲存所有可見文件與附件，但不能寫入、整案匯出、管理服務、瀏覽主機或使用 operator remote integrations。
