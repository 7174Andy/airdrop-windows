# airdrop-windows

AirDrop doesn't work between a Mac and a Windows PC, or from an iPhone to Windows. This project fills that gap. It's a small Go tool that sends files, folders, and clipboard text between your own devices over a [Tailscale](https://tailscale.com) tailnet, so it works at home, on campus, or across the internet.

Tailscale is used only as the network. It finds peers and encrypts traffic with WireGuard. The app itself is a plain HTTP server and client with its own pairing, so Tailscale can be replaced later without a rewrite.

## Features

- **Send files and folders.** Folders stream as a tar archive, with no zip step and no temp files.
- **Send text to the other device's clipboard.** Pass it as an argument or pipe it in: `pbpaste | airdrop text <peer>`.
- **One-time pairing.** Enter the 6-digit code the other device shows. After that, every request is authenticated with a token. Pairing locks after 3 wrong codes.
- **Safe receiving.**
  - Files arrive as `.part` files and appear only once they're complete.
  - Name clashes become `name (1).ext`.
  - Paths that would escape the save folder are rejected: `../`, absolute paths, `C:\`, and symlinks.
- **Tailnet only.** The receiver listens only on the device's Tailscale IP.
- **iPhone → computer.** `airdrop serve` prints a QR code. Scan it with the iPhone's camera (Tailscale must be on) to open an upload page in Safari for photos, videos, files, and text. Use *Share → Add to Home Screen* to keep it one tap away.
- **Mac ↔ Windows.** One codebase, cross-compiled from macOS.

## Usage

```bash
airdrop serve                       # run the receiver; prints a pair code
airdrop peers                       # list tailnet devices
airdrop pair <peer> <code>          # pair with a running receiver
airdrop send <peer> <file|folder>...
airdrop text <peer> "hello"         # or: pbpaste | airdrop text <peer>
```

`<peer>` can be the Tailscale host name (`LAPTOP-M8NLMMGL`), the MagicDNS name (`laptop-m8nlmmgl`), or the device's Tailscale IP.

Received files go to `~/Downloads`. To change that, edit `save_dir` in the config file:
- macOS: `~/Library/Application Support/airdrop/config.json`
- Windows: `%AppData%\airdrop\config.json`

## Roadmap

- [x] **Phase 1:** core and CLI. Pairing, sending files and text, peer discovery.
- [x] **Phase 2:** folder send with a streamed tar, plus tests that unsafe paths are refused.
- [x] **Phase 3:** sending from an iPhone. An upload page, opened by scanning a QR code.
- [ ] **Phase 4:** desktop app (Wails v3).
  - [x] 4a: a background tray receiver with Receive / Receive for 10 minutes / Off, plus an optional window (drag-drop onto a device, clipboard send, pairing, iPhone QR code), notifications, and an Accept/Decline prompt.
  - [ ] 4b: start at login, a proper macOS app bundle and icon, Windows "Send to" and a macOS Finder Quick Action.
- [ ] **Phase 5:** life without Tailscale.
  - Direct LAN connection with mDNS and pinned TLS.
  - A self-hosted relay for networks that block device-to-device traffic.
  - Hotspot mode.
  - Bluetooth for discovery and pairing only.

The full design is in [PLAN.md](PLAN.md). The architecture diagram is in [docs/architecture.html](docs/architecture.html).

## Development

**Requirements**
- Go 1.27+
- Tailscale installed and signed in on every device

On macOS, `brew install go`. Tailscale's CLI is found automatically, even if only the app is installed.

```bash
go build ./cmd/airdrop                                            # build for this machine
GOOS=windows GOARCH=amd64 go build -o airdrop.exe ./cmd/airdrop   # Windows build from macOS
go test ./core/                                                   # run all tests
go test ./core/ -run TestSendDir                                  # run one test
go vet ./...

go build -o airdrop-app ./app                                         # desktop app (macOS needs Xcode command-line tools: cgo)
GOOS=windows GOARCH=amd64 go build -ldflags=-H=windowsgui -o airdrop-app.exe ./app
./airdrop-app --background                                            # start in the tray without opening the window
```

**Layout**

| Path | Purpose |
|---|---|
| `core/discover.go` | The only code that talks to Tailscale. Turns peer names into IPs. |
| `core/server.go` | The receiver (`/`, `/ping`, `/pair`, `/file`, `/dir`, `/text`), plus `safePath`, which every incoming file name passes through. |
| `core/upload.html` | The iPhone upload page, embedded in the binary. |
| `core/send.go` | The sender, and the client side of pairing. |
| `core/config.go` | Device name, token, paired peers, save folder. |
| `cmd/airdrop` | The CLI. |
| `app/` | The desktop app: tray + receiver (`main.go`), window bindings and receiver hooks (`app.go`), window UI (`frontend/index.html`). |

**Testing between two machines:** run `airdrop serve` on one device, then `airdrop pair` and `airdrop send` from the other. To confirm a file arrived intact, compare hashes: `shasum -a 256 <file>` on macOS and `Get-FileHash <file>` on Windows. When Windows asks, allow `airdrop.exe` through the firewall.
