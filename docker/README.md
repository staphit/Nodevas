# Docker security lab

This compose file simulates the networked deployment path locally:

- Caddy terminates HTTPS on `https://nodevas.localhost:8443`.
- Nodevas runs as a non-root container and is not published to the host.
- Mailpit captures OTP mail at `http://127.0.0.1:8025`.
- The workspace and server config are Docker volumes, so the image contains no user data.

PowerShell setup:

```powershell
New-Item -ItemType Directory -Force docker/secrets | Out-Null
Set-Content -NoNewline docker/secrets/admin-password.txt 'use-a-long-local-test-password'
$env:NODEVAS_ADMIN_PIN = 'local-test-pin-2026'

docker build -t nodevas:security-lab .
docker compose -f compose.security.yaml up -d --build
docker compose -f compose.security.yaml ps
```

Request an OTP, then read the code in Mailpit:

```powershell
Set-Content -NoNewline docker/otp-request.json '{"pin":"local-test-pin-2026"}'
curl.exe -k -X POST https://nodevas.localhost:8443/api/auth/otp/request `
  -H 'Content-Type: application/json' `
  --data-binary @docker/otp-request.json
Remove-Item docker/otp-request.json
```

Repeat the request immediately. The second request must return HTTP 429; a new OTP can be requested only after 30 seconds. The broader global, source, and hourly budgets remain active too.

The Caddy certificate is intentionally local (`tls internal`), so `curl -k` is expected for this lab. Do not reuse the sample password, PIN, Mailpit, or the plaintext SMTP setup for a public deployment. For a real registry push, retag the image with the registry path and run `docker push` after authenticating.
