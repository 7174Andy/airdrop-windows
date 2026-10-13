# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

"AirDrop for Windows": send files and clipboard text between Mac, Windows, and (later) iPhone over a Tailscale tailnet. Go module `airdrop`. The roadmap lives in `PLAN.md` (phases 1–5); phases 1 (core + CLI), 2 (folder send), and 3 (iPhone upload page) are done.

## Commands

```bash
go test ./core/                          # all tests (one file: core/core_test.go)
go test ./core/ -run TestSafePath        # single test
go vet ./...
go build ./cmd/airdrop                   # native binary (no cgo needed)
GOOS=windows GOARCH=amd64 go build -o airdrop.exe ./cmd/airdrop   # cross-compile from the Mac
go build -o airdrop-app ./app && ./airdrop-app --background        # desktop app (cgo on macOS)
GOOS=windows GOARCH=amd64 go build -ldflags=-H=windowsgui -o airdrop-app.exe ./app
```

CLI: `airdrop serve [-addr host:port] | peers | pair <peer> <code> | send <peer> <file|folder>... | text <peer> [text]`.

## Architecture

- **Tailscale is only "the network", never a library.** The app is a plain HTTP server + client talking to peer IPs. All Tailscale knowledge is confined to `core/discover.go` (shells out to `tailscale status --json`, falling back to the macOS app binary at `/Applications/Tailscale.app/Contents/MacOS/Tailscale`). A stated future goal is dropping Tailscale, so don't leak Tailscale assumptions into other files. Don't add a `Transport` interface until a second transport actually exists.
- **Auth is app-level, independent of Tailscale.** Each device has one random token in its config. Pairing (`POST /pair`) sends the 6-digit code the receiver prints at startup; on success both sides store the *other* side's token under its device name. Requests carry `Authorization: Bearer <receiver's token>` plus an informational `X-From` header. Pairing locks after 3 wrong codes until the receiver restarts.
- **Peer naming:** a device's `Config.Name` defaults to its Tailscale HostName. The CLI resolves what the user types (HostName, MagicDNS label, or IP) via `core.Resolve`, then looks up the token by the resolved HostName, case-insensitively. Changing either side's naming breaks this link.
- **`core` stays UI-free.** Side effects that differ per front-end are hooks on `Server`: `OnText` (the CLI wires it to `golang.design/x/clipboard`), `Accept` (asked before a file/folder body is read; may block on a dialog), and `OnReceive`. Nil hooks mean "just do it", which is what the CLI relies on.
- **Receiving on/off lives in `Config`** (`Off`, `Until`; `Receiving()` is time-based, so no timer is needed). `Server.on` wraps `/pair`, `/file`, `/dir` and `/text` and returns 503 while off.
- **Desktop app (`app/`, Wails v3 beta, pinned to an exact version):** one process that is a tray icon *and* the receiver. The window is created lazily (`ShowWindow`) and destroyed on close (`a.win` goes nil), and receiving carries on. Exported `App` methods are called from JS as `Call.ByName("main.App.<Method>")` via `/wails/runtime.js`, with no generated bindings or npm. Go→JS uses events: `state`, `status`, `received`. Menu updates must run on the UI thread: use `application.InvokeAsync` (`InvokeSync` from the UI thread deadlocks). The Accept prompt only appears while the window is open; otherwise paired peers are trusted. macOS notifications need a bundled `.app`, so unbundled builds fall back to `osascript`.
- **`serve` binds only to this device's Tailscale IP** by default, not 0.0.0.0. The default port is 7878, and peers are assumed to use the same port.
- **`safePath` in `core/server.go` is the trust boundary** for sender-supplied file names. It normalizes `\` to `/`, rejects `:` (drive letters, NTFS streams), and requires `filepath.IsLocal`. Every endpoint that writes files must route through it: `/file` for its name, `/dir` for the folder name *and* every tar entry.
- Received files stream to a `.airdrop-*.part` temp file in the target dir, are length-checked against Content-Length, then renamed. A name that already exists gets a ` (1)` suffix.
- **Folders** (`POST /dir?name=X`): the sender streams a tar through an `io.Pipe` (no temp file, chunked, no Content-Length). The receiver unpacks into a hidden `.airdrop-*.part` dir and renames it into place only after the whole stream is valid, so a bad entry anywhere discards everything. Only regular files and dirs are allowed: the sender skips symlinks/devices, and the receiver rejects the whole transfer if it sees one. File modes are not preserved.
- **iPhone page** (`GET /`, `core/upload.html` via `go:embed`): a static page with no secrets. The token arrives in the URL's `?t=` (from the QR code that `serve` prints, built by `core.UploadURL`) and is kept in `localStorage`, then sent as a normal Bearer header to `/file` and `/text`. So the phone needs no new endpoints, and the QR code is equivalent to the receiver's token. Uploads use XHR rather than fetch, for upload progress.

Config lives at `os.UserConfigDir()/airdrop/config.json` (mode 0600, contains tokens). Note that `LoadConfig` writes this file, and on first run it shells out to Tailscale to pick the name.

## Conventions

Deliberate shortcuts are marked with `ponytail:` comments that name the limitation and when to upgrade it. Keep using that marker.
