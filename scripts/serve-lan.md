# Opening the app on a phone

The touch layout, the gestures and the safe-area insets are all web UI. They
need only a phone plugged into this machine. Use this while iterating on the
mobile layout.

## Screenshots, no phone at all

```powershell
cd web
npm run preview:mobile
```

Writes `web/mobile-preview/<device>/*.png` for iPhone SE, iPhone 15, and iPad in
both orientations. Fastest loop for "does the drawer still cover the board".

A device profile is not a device: touch is synthesised, Chromium is not iOS
Safari, and the safe-area insets are zero because there is no notch. For those,
use a real phone below.

## A real Android phone over USB

The dev server serves the UI; the Go server stays on loopback with no accounts
and answers only this machine. A USB port forward makes the phone's own
`localhost:5173` this machine's dev server, so the phone counts as this machine
and nothing is exposed to the network.

**One terminal — the backend, loopback only:**

```powershell
mkdir -Force workspace
go run ./cmd/nodevas serve -project ./workspace -port 5666
```

**Another — the dev server, loopback only:**

```powershell
cd web
npm run dev
```

**A third — forward the phone's port 5173 to this machine** (USB debugging on,
`adb` from the Android platform tools):

```powershell
adb reverse tcp:5173 tcp:5173
```

Open <http://localhost:5173> in Chrome on the phone. `adb reverse --remove-all`
undoes the forward.

To get a full-screen view without browser chrome, use the browser's
Add to Home Screen and launch it from there: `apple-mobile-web-app-capable`
in `web/index.html` asks for it to open without the browser's bars.

### Why not the LAN

`npm run dev -- --host` still serves the page to the network, but the hub
answers every `/api` and `/ws` request the dev server relays for another device
with `401` ("this server has no accounts and answers only this machine"): a
phone on the Wi-Fi gets the app shell and no workspace. That is on purpose. A
hub without accounts trusts whoever reaches it, so it may only be reached from
this machine; the dev server also refuses (`404`) other origins, other Host
names, cross-site pages and a tunnel on this machine.

An iPhone has no USB port forward like `adb reverse`. Test it against a hub with
accounts and HTTPS ([Team server](../docs/getting-started.md#team-server)), or
use the screenshots above.

Do not reach for `nodevas serve -listen 0.0.0.0` as a shortcut here. That binds
the real server to the network, where it demands accounts, mail and TLS for good
reasons, and `--allow-plaintext` would put session cookies and passcodes on the
wire in clear text.
