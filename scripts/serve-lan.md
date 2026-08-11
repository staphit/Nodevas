# Opening the app on a phone, without building an iOS app

The touch layout, the gestures and the safe-area insets are all web UI. None of
them need a signed app, a Mac, or Xcode — only a phone that can reach this
machine. Use this while iterating on the mobile layout; build the iOS app when
you need the parts a browser cannot show (the app shell, its navigation policy,
the launch behaviour).

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

## A real phone over the LAN

The dev server serves the UI to the network; the Go server stays on loopback and
is reached through the dev server's proxy. That is the whole point of doing it
this way — the backend is never exposed to the network, so no accounts and no
plaintext-over-the-network trade-off are involved.

**One terminal — the backend, loopback only:**

```powershell
go run ./cmd/nodevas serve -project ./examples -port 5666
```

**Another — the dev server, on the network:**

```powershell
cd web
npm run dev -- --host
```

Vite prints a `Network:` URL such as `http://192.168.1.23:5173`. Open that in
Safari on the phone. Both devices must be on the same Wi-Fi, and Windows will
ask to allow Node through the firewall the first time — allow it on private
networks only.

To get the full-screen, no-browser-chrome view the iOS app will have, use
Share → Add to Home Screen and launch it from there: `apple-mobile-web-app-capable`
in `web/index.html` makes it open without Safari's bars, which is the closest a
browser gets to the real thing.

### What this exposes

Anyone on the same network who finds port 5173 gets your workspace, with no
password, for as long as the dev server runs. It is a development server on a
network you trust — a home or office Wi-Fi — not something to leave running on
a café hotspot. Stop it when you are done.

Do not reach for `nodevas serve -listen 0.0.0.0` as a shortcut here. That binds
the real server to the network, where it demands accounts and TLS for good
reasons, and `--allow-plaintext` would put session cookies and passwords on the
wire in clear text. The proxy above avoids the question entirely.

## The iOS app cannot use this

The app accepts `https://` addresses only, so a Vite dev server on
`http://192.168.1.23:5173` is not something it will connect to. That is
deliberate — see `ios/README.md`. Point the app at a server with a real
certificate; use Safari on the phone for the LAN loop above.

## What still needs a Mac

- Compiling and signing the app itself.
- Anything about the app shell: the setup screen, the navigation policy, the
  offline state.

See `ios/README.md`. There is no way to produce a signed `.ipa` from Windows —
it is an Apple toolchain requirement, not a choice this repo makes. A macOS CI
runner (`runs-on: macos-14`) works if you would rather not buy a Mac.
