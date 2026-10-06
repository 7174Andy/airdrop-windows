# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

"AirDrop for Windows": send files and clipboard text between Mac, Windows, and (later) iPhone over a Tailscale tailnet. Go module `airdrop`. The roadmap lives in `PLAN.md` (phases 1–5); phase 1 (core + CLI) is done.

## Commands

```bash
go test ./core/                          # all tests (one file: core/core_test.go)
go test ./core/ -run TestSafePath        # single test
go vet ./...
go build ./cmd/airdrop                   # macOS binary (cgo, for clipboard)
GOOS=windows GOARCH=amd64 go build -o airdrop.exe ./cmd/airdrop   # cross-compile from the Mac
```

CLI: `airdrop serve [-addr host:port] | peers | pair <peer> <code> | send <peer> <file>... | text <peer> [text]`.

## Architecture

- **Tailscale is only "the network", never a library.** The app is a plain HTTP server + client talking to peer IPs. All Tailscale knowledge is confined to `core/discover.go` (shells out to `tailscale status --json`, falling back to the macOS app binary at `/Applications/Tailscale.app/Contents/MacOS/Tailscale`). A stated future goal is dropping Tailscale, so don't leak Tailscale assumptions into other files. Don't add a `Transport` interface until a second transport actually exists.
- **Auth is app-level, independent of Tailscale.** Each device has one random token in its config. Pairing (`POST /pair`) sends the 6-digit code the receiver prints at startup; on success both sides store the *other* side's token under its device name. Requests carry `Authorization: Bearer <receiver's token>` plus an informational `X-From` header. Pairing locks after 3 wrong codes until the receiver restarts.
- **Peer naming:** a device's `Config.Name` defaults to its Tailscale HostName. The CLI resolves what the user types (HostName, MagicDNS label, or IP) via `core.Resolve`, then looks up the token by the resolved HostName, case-insensitively. Changing either side's naming breaks this link.
- **`core` stays UI-free.** Side effects that differ per front-end are hooks on `Server` (e.g. `OnText`; the CLI wires it to `golang.design/x/clipboard`). The planned Wails GUI (`app/`, phase 4) is meant to call `core` directly the same way.
- **`serve` binds only to this device's Tailscale IP** by default, not 0.0.0.0. The default port is 7878, and peers are assumed to use the same port.
- **`safePath` in `core/server.go` is the trust boundary** for sender-supplied file names. It normalizes `\` to `/`, rejects `:` (drive letters, NTFS streams), and requires `filepath.IsLocal`. Any new endpoint that writes files (e.g. phase 2's tar-based `/dir`) must route through it.
- Received files stream to a `.airdrop-*.part` temp file in the target dir, are length-checked against Content-Length, then renamed. A name that already exists gets a ` (1)` suffix.

Config lives at `os.UserConfigDir()/airdrop/config.json` (mode 0600, contains tokens). Note that `LoadConfig` writes this file, and on first run it shells out to Tailscale to pick the name.

## Conventions

Deliberate shortcuts are marked with `ponytail:` comments that name the limitation and when to upgrade it. Keep using that marker.
