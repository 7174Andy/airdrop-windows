# Plan: "AirDrop for Windows" over Tailscale (Go + Wails)

## Context
AirDrop doesn't work Mac↔Windows or iPhone→Windows. Both laptops are in one Tailnet, so they can reach each other anywhere. Taildrop exists but the user wants more: **folder send, clipboard/text send, iPhone→Windows**, a desktop app + CLI. Future goal: **drop the Tailscale dependency**, so Tailscale must stay behind one seam, never be woven through the code.

## Key design decision: Tailscale is only "the network", not a library
- Don't use `tsnet` or Taildrop APIs. The app is a plain **HTTP server + client** that talks to peer IPs.
- Tailscale gives us two things now, and each sits behind a small seam:
  1. **Peer discovery**: `discover.go` runs `tailscale status --json` → list of {name, IP, OS, online}. Later: swap in mDNS (`_airdrop._tcp`) + manual IP entry. Only this one file changes.
  2. **Encryption in transit**: WireGuard does this for free today. Later: self-signed TLS with cert pinning set at pairing time.
- **App-level pairing from day 1** (cheap, and needed post-Tailscale anyway): each device makes a random token. Pairing = enter the other device's 6-digit code once → tokens swapped and saved in config. Every request carries `Authorization: Bearer <token>`. Then dropping Tailscale only means adding TLS + discovery, not redoing auth.
- The server listens only on the Tailscale interface IP (100.x) for now.

## Architecture
One Go module, one core package, two front-ends:

```
airdrop/
  go.mod
  core/
    server.go     // HTTP receiver: POST /file, POST /text, GET / (iPhone upload page), GET /ping
    send.go       // client: SendFile, SendDir, SendText
    discover.go   // peers from `tailscale status --json`  ← Tailscale seam
    config.go     // ~/.config/airdrop/config.json: device name, token, paired peers, save dir, auto-accept
    upload.html   // embedded (go:embed) mobile upload page
  cmd/airdrop/main.go   // CLI (stdlib `flag`, subcommands)
  app/                  // Wails app: tray + window, calls core directly
    main.go, app.go
    frontend/           // plain HTML/JS + drag-and-drop zone, no framework
```

### Protocol (plain HTTP, kept simple)
- `POST /file?name=<rel/path>&size=N`: body is the raw file stream, written to `<saveDir>/<name>.part`, renamed when done. `rel/path` is sanitized (no `..`, no absolute paths; this is a trust boundary).
- **Folder send**: the client walks the dir and streams a **tar** (`archive/tar`, stdlib) to `POST /dir?name=X`; the receiver extracts it with the same path checks. No zip step, no temp files.
- `POST /text`: body is UTF-8 text → put on the receiver's clipboard (`golang.design/x/clipboard`, or Wails' clipboard runtime in the GUI) and shown as a notification.
- Accept prompt: the desktop app asks Accept/Decline before reading the body, but only while its window is open and "Ask before receiving" is on. With the window closed, paired peers are trusted and you get a notification. The CLI receiver accepts from paired peers.
- Receiving on/off: off, on, or on until a deadline ("receive for 10 minutes"). Stored in config and checked on every request, so no timer is needed. While off, everything but `/ping` and the upload page gets 503 "<name> is not receiving right now".
- Resume, compression, and parallel streams are skipped. Over WireGuard a single HTTP stream gets close to full link speed.

### iPhone → Windows
- The Windows app serves `GET /` = a mobile upload page (`<input type="file" multiple>` + a textarea for text). The iPhone runs Tailscale and opens `http://<windows-name>:7878/?t=<token>` in Safari, and adds it to the Home Screen.
- The GUI shows a **QR code** of that URL (`github.com/skip2/go-qrcode`) so pairing the phone is a single scan.
- Optional later: an iOS Shortcut "Send to Windows" in the Share Sheet that POSTs to `/file`. It uses the same endpoint, so no server changes are needed.

### CLI
```
airdrop serve                     # run the receiver (headless)
airdrop peers                     # list online peers
airdrop pair <peer>               # pair using the code shown on the other device
airdrop send <peer> <file|dir>... # send files and folders
airdrop text <peer> "hello"       # or: pbpaste | airdrop text <peer>
```

### Desktop app (Wails v3)
Revised during phase 4: receiving must not depend on an app being open.
- **One background process.** The app is a tray icon that owns the receiver and keeps running. It has no window by default (`--background`) and uses about 76 MB idle on macOS (the bare CLI receiver uses about 20 MB). The window is created when opened and destroyed when closed. Receiving carries on.
- **Tray menu:** status line, Receive / Receive for 10 minutes / Don't receive, Open airdrop…, Open received files, Quit.
- **Window (optional):** the same receiving switch, peer list with drag-drop onto a peer, Choose files…, Send clipboard, pairing, a received list, the iPhone QR code, and "Ask before receiving".
- **Single instance:** launching the app again opens the running app's window.
- **Notifications:** the Wails notifications service on Windows. On macOS it needs a bundled `.app`, so until packaging the app falls back to `osascript`.
- **Phase 4b (done):** start at login (LaunchAgent / HKCU `Run` key, running `--background`), a macOS `.app` bundle (`app/bundle.sh`) and app icon, Windows "Send to" and macOS Finder Quick Action. The right-click entry runs the *app* with `--send <files>` rather than the CLI: the running app gets the files and you pick a device in its window, so one entry covers every peer.

## Build phases
1. **Core + CLI**: config, pairing, server `/file` `/text` `/ping`, send, discover. Check Mac↔Windows over the tailnet.
2. **Folder send** (tar stream) + path sanitization tests.
3. **iPhone upload page** + QR code.
4. **Desktop app**: (4a) background tray receiver with on/off/10-minute switch, optional window with drag-drop, notifications, accept dialog. (4b) start at login, .app bundle + icon, Send-to / Quick Action.
5. **Later (removing Tailscale)**: see the section below.

## Life after Tailscale (phase 5+, nothing built for it now)
The problem: institutional LANs (campus, office, café) often turn on **client/AP isolation**, which blocks traffic between devices on the same Wi-Fi. Tailscale gets around this today by falling back to its DERP relays over the internet.

**Why Bluetooth only fits a small role:**
- Bandwidth: Classic BT RFCOMM gives about 1–2 Mbps in practice, so 1 GB takes over an hour. BLE is slower still. Real AirDrop uses BLE only for *discovery* and moves the data over AWDL (peer-to-peer Wi-Fi). AWDL is Apple-only, and macOS doesn't support Wi-Fi Direct, so there is no shared fast radio link between Mac and Windows.
- Effort: there's no cross-platform Go Bluetooth stack that covers RFCOMM on both macOS (IOBluetooth/CoreBluetooth) and Windows (WinRT). It would mean native code on each OS.
- What BT *is* good for: **discovery, pairing (swapping tokens/cert fingerprints), and clipboard text**. Those payloads are tiny and work even with no network at all.

**Fallback chain once Tailscale is gone** (each one is a separate `Transport`, chosen automatically in order):
1. **Direct LAN**: mDNS discovery + HTTPS (pinned certs). Works on home Wi-Fi.
2. **Internet relay / hole-punching**: both devices connect *outbound* to a small self-hosted relay (a tiny VPS, or Cloudflare). Isolated LANs still allow internet access, so this works where direct LAN doesn't. Data stays end-to-end encrypted with the pinned keys, and the relay only forwards bytes. This is the real fix for institutional networks.
3. **Hotspot mode**: one laptop turns on its hotspot (Windows Mobile Hotspot / macOS Internet Sharing), the other joins, and we're back to case 1. Works fully offline at full Wi-Fi speed. The app only needs a "Start hotspot" button plus instructions.
4. **Bluetooth (BLE)**: discovery, pairing, and text/clipboard only. Small files (<~5 MB) at most.

**Seam to keep now so this is cheap later:** `core/send.go` and `core/server.go` deal in an `io.Reader` stream plus metadata, not in "HTTP to a 100.x IP". Leave it at that: no `Transport` interface until a second transport actually exists (phase 5). At that point, HTTP-over-Tailscale becomes the first implementation.

## Verification
- `go test ./core/...`: one test file covering path sanitization (`../`, absolute, Windows `C:\` paths rejected), a tar round-trip, and a send→receive file round-trip over `httptest` with a bad token rejected.
- Manual: `airdrop serve` on Windows, `airdrop send win-laptop ~/somefolder` from the Mac → compare hashes (`shasum` vs `Get-FileHash`). `airdrop text` → paste on the other side.
- iPhone: scan the QR code, upload a photo and a video, confirm they land in the Windows save folder.
- Cross-compile: `GOOS=windows go build ./cmd/airdrop` from the Mac. Wails builds per-OS (`wails build` on each machine, or CI later).
