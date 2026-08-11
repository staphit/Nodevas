#!/usr/bin/env bash
# First-boot provisioning for Ubuntu 24.04 on OCI.
set -euo pipefail
exec > >(tee -a /var/log/nodevas-bootstrap.log) 2>&1

[ "$#" -eq 3 ] || { echo "usage: $0 BUCKET NAMESPACE DATA_DEVICE" >&2; exit 64; }
readonly BUCKET="$1"
readonly NAMESPACE="$2"
readonly DATA_DEVICE="$3"
readonly MOUNTPOINT=/var/lib/nodevas
readonly WORKSPACE=/var/lib/nodevas/workspace
readonly CONFIG=/var/lib/nodevas/config
readonly OCI_CLI_VERSION=3.90.1

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
  ca-certificates curl gnupg debian-keyring debian-archive-keyring \
  apt-transport-https xfsprogs open-iscsi sqlite3 jq python3-venv \
  iptables-persistent fail2ban

# OCI CLI in an isolated virtual environment. Authentication uses the instance
# principal; no API key or ~/.oci/config is created.
if ! command -v oci >/dev/null 2>&1; then
  python3 -m venv /opt/oci-cli
  /opt/oci-cli/bin/pip install --no-cache-dir "oci-cli==$OCI_CLI_VERSION"
  ln -sf /opt/oci-cli/bin/oci /usr/local/bin/oci
fi

# Install Caddy from its official apt repository.
if ! command -v caddy >/dev/null 2>&1; then
  curl -1fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key \
    | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt \
    -o /etc/apt/sources.list.d/caddy-stable.list
  chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg \
    /etc/apt/sources.list.d/caddy-stable.list
  apt-get update
  apt-get install -y caddy
fi
systemctl stop caddy || true

# Terraform uploads these files before launch. Instance-specific dynamic-group
# membership can take about an hour to propagate, so authenticated downloads
# retry instead of racing IAM.
install_object() {
  local object="$1" target="$2" mode="$3" group=root
  local temp err
  [ "$#" -lt 4 ] || group="$4"
  temp="$(mktemp)"
  err="$(mktemp)"
  # "os object get" overwrites its --file without asking, and has no --force.
  for _ in $(seq 1 360); do
    if oci --auth instance_principal os object get \
      --namespace "$NAMESPACE" --bucket-name "$BUCKET" \
      --name "bootstrap/$object" --file "$temp" >/dev/null 2>"$err"; then
      install -o root -g "$group" -m "$mode" "$temp" "$target"
      rm -f "$temp" "$err"
      return 0
    fi
    sleep 10
  done
  # A silent retry loop turns every failure into the same hour-long timeout, so
  # report what the last attempt actually said.
  echo "cannot download bootstrap/$object from $BUCKET" >&2
  cat "$err" >&2
  rm -f "$temp" "$err"
  return 1
}

mkdir -p /etc/nodevas /etc/caddy /etc/systemd/system/caddy.service.d \
  /etc/fail2ban/filter.d /etc/fail2ban/jail.d
install_object nodevas.service /etc/systemd/system/nodevas.service 0644
install_object nodevas-backup.service /etc/systemd/system/nodevas-backup.service 0644
install_object nodevas-backup.timer /etc/systemd/system/nodevas-backup.timer 0644
install_object Caddyfile /etc/caddy/Caddyfile 0644
install_object nodevas.env.example /etc/nodevas/nodevas.env.example 0644
install_object nodevas-backup.sh /usr/local/sbin/nodevas-backup.sh 0755
install_object nodevas-deploy.sh /usr/local/sbin/nodevas-deploy.sh 0755
install_object nodevas-restore.sh /usr/local/sbin/nodevas-restore.sh 0755
install_object nodevas-logs /usr/local/bin/nodevas-logs 0755
# fail2ban reads the server's own journal rather than a web access log: Nodevas
# already records every request as ECS JSON with the client address resolved
# through the trusted-proxy rules, and a second log in Caddy would be the same
# requests stored twice, disagreeing about who the client was.
install_object fail2ban-nodevas.filter /etc/fail2ban/filter.d/nodevas.conf 0644
install_object fail2ban-nodevas.jail /etc/fail2ban/jail.d/nodevas.local 0644

# Volume attachment happens after VM creation. Oracle Cloud Agent performs the
# iSCSI login; wait for its stable path instead of guessing a disk.
systemctl enable --now iscsid.service open-iscsi.service || true
for _ in $(seq 1 240); do
  [ -b "$DATA_DEVICE" ] && break
  sleep 5
done
[ -b "$DATA_DEVICE" ] || {
  echo "data volume did not appear at $DATA_DEVICE within 20 minutes" >&2
  exit 1
}

mkdir -p "$MOUNTPOINT"
fstype="$(lsblk -no FSTYPE "$DATA_DEVICE" | head -n1 | tr -d ' ')"
pttype="$(lsblk -no PTTYPE "$DATA_DEVICE" | head -n1 | tr -d ' ')"
if [ -z "$fstype" ] && [ -z "$pttype" ]; then
  mkfs.xfs -L nodevas "$DATA_DEVICE"
  fstype=xfs
fi
uuid="$(blkid -s UUID -o value "$DATA_DEVICE")"
[ -n "$uuid" ] || { echo "data volume has no mountable filesystem" >&2; exit 1; }
sed -i "\| $MOUNTPOINT |d" /etc/fstab
printf 'UUID=%s %s %s defaults,noatime,_netdev,nofail 0 0\n' \
  "$uuid" "$MOUNTPOINT" "$fstype" >>/etc/fstab
systemctl daemon-reload
mountpoint -q "$MOUNTPOINT" || mount "$MOUNTPOINT"

id -u nodevas >/dev/null 2>&1 || useradd --system --home-dir "$MOUNTPOINT" \
  --no-create-home --shell /usr/sbin/nologin --comment 'Nodevas server' nodevas
mkdir -p "$WORKSPACE" "$CONFIG" "$MOUNTPOINT/backup"
chown -R nodevas:nodevas "$MOUNTPOINT"
chmod 0750 "$MOUNTPOINT"

# E2 micro needs headroom for package maintenance. Default A1 does not need it.
mem_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo)"
if [ "$mem_kb" -lt 1572864 ] && [ ! -f /.swapfile ]; then
  dd if=/dev/zero of=/.swapfile bs=1M count=4096 status=none
  chmod 0600 /.swapfile
  mkswap /.swapfile
  swapon /.swapfile
  grep -q '^/.swapfile ' /etc/fstab || \
    printf '/.swapfile none swap sw,nofail 0 0\n' >>/etc/fstab
  printf 'vm.swappiness=10\n' >/etc/sysctl.d/90-nodevas-swap.conf
  sysctl -p /etc/sysctl.d/90-nodevas-swap.conf || true
fi

# Ubuntu OCI images may retain an INPUT reject rule. Open only HTTP/HTTPS;
# OCI's security list remains the outer firewall.
for port in 80 443; do
  iptables -C INPUT -p tcp --dport "$port" -j ACCEPT 2>/dev/null || \
    iptables -I INPUT 1 -p tcp --dport "$port" -j ACCEPT
done
netfilter-persistent save

# Terraform computes the hostname and uploads it, because the instance has no
# way to find it out. Metadata at /opc/v2/vnics/ reports a public address only
# when one was assigned ephemerally at VNIC creation, and this VNIC is created
# with none so that it can accept the reserved address instead. Waiting on
# metadata for a reserved IP waits forever.
#
# An operator can override by writing the file before the script runs, which is
# also how an instance reached through a real domain avoids sslip.io.
if [ ! -s /etc/nodevas/hostname ]; then
  install_object hostname /etc/nodevas/hostname 0644
fi
site="$(head -n1 /etc/nodevas/hostname | tr -d '[:space:]')"
[ -n "$site" ] || { echo 'uploaded hostname is empty' >&2; exit 1; }

if [ ! -f /etc/nodevas/nodevas.env ]; then
  install -o root -g root -m 0600 \
    /etc/nodevas/nodevas.env.example /etc/nodevas/nodevas.env
fi
sed -i "s|^NODEVAS_HOSTNAME=.*|NODEVAS_HOSTNAME=$site|" /etc/nodevas/nodevas.env
sed -i '/^NODEVAS_BACKUP_BUCKET=/d;/^NODEVAS_OCI_NAMESPACE=/d' /etc/nodevas/nodevas.env
printf '\nNODEVAS_BACKUP_BUCKET=%s\nNODEVAS_OCI_NAMESPACE=%s\n' \
  "$BUCKET" "$NAMESPACE" >>/etc/nodevas/nodevas.env

printf 'NODEVAS_HOSTNAME=%s\nNODEVAS_UPSTREAM=127.0.0.1:5666\n' "$site" \
  >/etc/caddy/caddy.env
chown root:caddy /etc/caddy/caddy.env
chmod 0640 /etc/caddy/caddy.env
printf '[Service]\nEnvironmentFile=/etc/caddy/caddy.env\n' \
  >/etc/systemd/system/caddy.service.d/10-nodevas.conf

systemctl daemon-reload
# Same environment file systemd hands the service. Without it the site address
# placeholder expands to nothing, the site block loses its key, and the adapter
# reports it as a stray second global block rather than a missing variable.
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile \
  --envfile /etc/caddy/caddy.env
systemctl enable --now caddy
systemctl enable nodevas
systemctl enable --now nodevas-backup.timer

# Checked before the service is started, because a bad regex makes fail2ban
# refuse to start and the failure is then a unit that is down rather than a
# jail that is quietly matching nothing.
fail2ban-client --test >/dev/null
systemctl enable --now fail2ban

mkdir -p /etc/motd.d
cat >/etc/motd.d/nodevas <<EOF
Nodevas infrastructure ready: https://$site
Application is stopped until a binary and first account exist.
Deploy the binary from an SSH source allowed by the OCI security list.
Bootstrap log: /var/log/nodevas-bootstrap.log
EOF
