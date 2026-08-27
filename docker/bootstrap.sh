#!/bin/sh
set -eu

project="${NODEVAS_PROJECT:-/var/lib/nodevas/workspace}"
config_home="${XDG_CONFIG_HOME:-/var/lib/nodevas/config}"
user="${NODEVAS_ADMIN_USER:-admin}"
email="${NODEVAS_ADMIN_EMAIL:-admin@example.test}"
pin="${NODEVAS_ADMIN_PIN:?NODEVAS_ADMIN_PIN is required}"
password_file="${NODEVAS_ADMIN_PASSWORD_FILE:-/run/secrets/nodevas_admin_password}"
marker="${NODEVAS_BOOTSTRAP_MARKER:-$config_home/.docker-bootstrap-complete}"

if [ -e "$marker" ]; then
	printf '%s\n' "Nodevas bootstrap already complete"
	exit 0
fi

if [ ! -r "$password_file" ]; then
	echo "admin password secret is not readable: $password_file" >&2
	exit 1
fi

password=$(cat "$password_file")
if [ -z "$password" ]; then
	echo "admin password secret is empty" >&2
	exit 1
fi

mkdir -p "$project" "$config_home"
printf '%s\n' "$password" | nodevas user add \
	--project "$project" \
	--user "$user" \
	--role admin \
	--password-stdin

nodevas user pin \
	--project "$project" \
	--user "$user" \
	--email "$email" \
	--pin "$pin"

touch "$marker"
printf '%s\n' "Nodevas bootstrap complete for $user; OTP delivery is captured by Mailpit"
