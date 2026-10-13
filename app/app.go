package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"airdrop/core"

	"github.com/skip2/go-qrcode"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// After you accept something from a sender, more from them within this window
// is accepted without asking, so a batch of iPhone photos means one prompt, not twenty.
const acceptGrace = 5 * time.Minute

// App is bound to the frontend: its exported methods are callable from JS as "main.App.<Method>".
type App struct {
	app   *application.App
	cfg   *core.Config
	srv   *core.Server
	notes *notifications.NotificationService // nil when the OS won't allow the notifications service

	addr      string
	serverErr string

	win                                  *application.WebviewWindow // nil while closed
	trayMenu                             *application.Menu
	trayStatus, trayOn, trayTen, trayOff *application.MenuItem

	askMu    sync.Mutex // one Accept/Decline dialog at a time
	mu       sync.Mutex
	accepted map[string]time.Time // sender -> last time the user accepted them
}

type Info struct {
	Name, Addr, PairCode, QR, SaveDir, ServerError string
	AutoAccept                                     bool
	Mode                                           string // "on", "until", "off"
	Until                                          string // "22:40" when Mode is "until"
}

type PeerView struct {
	Name, OS       string
	Online, Paired bool
}

// start launches the receiver on this device's Tailscale IP.
func (a *App) start() {
	a.srv = core.NewServer(a.cfg)
	a.srv.Accept = a.accept
	a.srv.OnReceive = a.onReceive
	a.srv.OnText = a.onText

	self, err := core.Self()
	if err != nil || self.IP == "" {
		a.serverErr = fmt.Sprintf("Can't find this device's Tailscale IP. Is Tailscale running? (%v)", err)
		return
	}
	a.addr = net.JoinHostPort(self.IP, strconv.Itoa(a.cfg.Port))
	ln, err := net.Listen("tcp", a.addr)
	if err != nil {
		a.serverErr = "Can't start the receiver: " + err.Error()
		return
	}
	log.Printf("receiving on %s", a.addr)
	go http.Serve(ln, a.srv.Handler())
}

// ---- window

// ShowWindow opens the window, creating it if it was closed.
func (a *App) ShowWindow() {
	if a.win == nil {
		w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:          "airdrop",
			Width:          380,
			Height:         660,
			EnableFileDrop: true,
		})
		w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { a.setWin(nil) }) // destroyed; receiving carries on
		w.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
			if d := e.Context().DropTargetDetails(); d != nil && d.Attributes["data-peer"] != "" {
				go a.Send(d.Attributes["data-peer"], e.Context().DroppedFiles())
			}
		})
		a.setWin(w)
	}
	a.win.Show()
	a.win.Focus()
}

func (a *App) setWin(w *application.WebviewWindow) { a.mu.Lock(); a.win = w; a.mu.Unlock() }

func (a *App) windowOpen() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.win != nil }

// ---- receiving on / off

// SetReceiving switches receiving: "on", "10m" (on for 10 minutes), or "off".
func (a *App) SetReceiving(mode string) error {
	var err error
	switch mode {
	case "on":
		err = a.cfg.SetReceiving(true, time.Time{})
	case "10m":
		err = a.cfg.SetReceiving(true, time.Now().Add(10*time.Minute))
	case "off":
		err = a.cfg.SetReceiving(false, time.Time{})
	default:
		err = fmt.Errorf("unknown mode %q", mode)
	}
	application.InvokeAsync(a.refreshState) // menus must be touched on the UI thread
	return err
}

func (a *App) mode() (mode, until string) {
	switch {
	case !a.cfg.Receiving():
		return "off", ""
	case !a.cfg.Until.IsZero():
		return "until", a.cfg.Until.Format("15:04")
	}
	return "on", ""
}

// refreshState pushes the receiving state to the tray menu and the window.
func (a *App) refreshState() {
	mode, until := a.mode()
	label := map[string]string{"on": "● Receiving", "until": "● Receiving until " + until, "off": "○ Not receiving"}[mode]
	if a.serverErr != "" {
		label = "⚠ Receiver not running"
	}
	if a.trayMenu != nil {
		a.trayStatus.SetLabel(label)
		a.trayOn.SetChecked(mode == "on")
		a.trayTen.SetChecked(mode == "until")
		a.trayOff.SetChecked(mode == "off")
		a.trayMenu.Update()
	}
	a.app.Event.Emit("state", map[string]string{"mode": mode, "until": until})
}

// ---- called from the window

func (a *App) Info() Info {
	mode, until := a.mode()
	in := Info{Name: a.cfg.Name, Addr: a.addr, SaveDir: a.cfg.SaveDir, AutoAccept: a.cfg.AutoAccept, ServerError: a.serverErr, Mode: mode, Until: until}
	if a.srv != nil && a.addr != "" {
		in.PairCode = a.srv.PairCode()
		if png, err := qrcode.Encode(core.UploadURL(a.addr, a.cfg.Token), qrcode.Medium, 256); err == nil {
			in.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	return in
}

func (a *App) Peers() ([]PeerView, error) {
	peers, err := core.Peers()
	if err != nil {
		return nil, err
	}
	out := make([]PeerView, 0, len(peers))
	for _, p := range peers {
		_, paired := a.cfg.PeerToken(p.Name)
		out = append(out, PeerView{Name: p.Name, OS: p.OS, Online: p.Online, Paired: paired})
	}
	sort.Slice(out, func(i, j int) bool { // paired + online first
		if out[i].Paired != out[j].Paired {
			return out[i].Paired
		}
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (a *App) Pair(name, code string) error {
	p, err := core.Resolve(name)
	if err != nil {
		return err
	}
	_, err = core.Pair(core.Addr(p.IP, a.cfg.Port), code, a.cfg)
	return err
}

func (a *App) target(name string) (addr, token string, err error) {
	p, err := core.Resolve(name)
	if err != nil {
		return "", "", err
	}
	token, ok := a.cfg.PeerToken(p.Name)
	if !ok {
		return "", "", fmt.Errorf("not paired with %s", p.Name)
	}
	return core.Addr(p.IP, a.cfg.Port), token, nil
}

// Send sends files and folders to a peer, reporting progress as "status" events.
func (a *App) Send(name string, paths []string) error {
	addr, token, err := a.target(name)
	if err != nil {
		a.status(name, err.Error(), true)
		return err
	}
	for i, p := range paths {
		a.status(name, fmt.Sprintf("Sending %d of %d · %s", i+1, len(paths), filepath.Base(p)), false)
		if err := core.SendFile(addr, token, a.cfg.Name, p); err != nil {
			a.status(name, filepath.Base(p)+": "+err.Error(), true)
			return err
		}
	}
	a.status(name, fmt.Sprintf("✓ Sent %d item%s", len(paths), plural(len(paths))), false)
	return nil
}

// ChooseAndSend opens a file picker and sends what was picked.
func (a *App) ChooseAndSend(name string) error {
	paths, err := a.app.Dialog.OpenFile().
		SetTitle("Send to " + name).
		CanChooseFiles(true).
		CanChooseDirectories(true).
		PromptForMultipleSelection()
	if err != nil || len(paths) == 0 {
		return err
	}
	return a.Send(name, paths)
}

func (a *App) SendClipboard(name string) error {
	text, ok := a.app.Clipboard.Text()
	if !ok || text == "" {
		a.status(name, "Your clipboard is empty", true)
		return nil
	}
	addr, token, err := a.target(name)
	if err == nil {
		err = core.SendText(addr, token, a.cfg.Name, text)
	}
	if err != nil {
		a.status(name, err.Error(), true)
		return err
	}
	a.status(name, "✓ Clipboard sent", false)
	return nil
}

func (a *App) SetAutoAccept(on bool) error { return a.cfg.SetAutoAccept(on) }

func (a *App) OpenSaveDir() error { return a.app.Browser.OpenFile(a.cfg.SaveDir) }

func (a *App) Open(path string) error { return a.app.Browser.OpenFile(path) }

// ---- receiver hooks (called on HTTP goroutines)

// accept asks only while the window is open and "Ask before receiving" is on.
// With the window closed, paired devices are trusted and you get a notification instead.
func (a *App) accept(from, what string) bool {
	if a.cfg.AutoAccept || !a.windowOpen() {
		return true
	}
	a.askMu.Lock()
	defer a.askMu.Unlock()
	a.mu.Lock()
	ok := time.Since(a.accepted[from]) < acceptGrace
	a.mu.Unlock()
	if !ok {
		ok = a.ask(from, what)
	}
	if ok {
		a.mu.Lock()
		a.accepted[from] = time.Now()
		a.mu.Unlock()
	}
	return ok
}

func (a *App) ask(from, what string) bool {
	answer := make(chan bool, 1)
	d := a.app.Dialog.Question().
		SetTitle(from + " wants to send you").
		SetMessage(what)
	yes := d.AddButton("Accept").OnClick(func() { answer <- true })
	no := d.AddButton("Decline").OnClick(func() { answer <- false })
	d.SetDefaultButton(yes)
	d.SetCancelButton(no)
	d.Show()
	select {
	case ok := <-answer:
		return ok
	case <-time.After(time.Minute): // nobody at the computer
		return false
	}
}

func (a *App) onReceive(from, path string) {
	a.app.Event.Emit("received", map[string]string{"from": from, "path": path, "name": filepath.Base(path)})
	a.notify("From "+from, filepath.Base(path))
}

func (a *App) onText(from, text string) {
	a.app.Clipboard.SetText(text)
	a.app.Event.Emit("received", map[string]string{"from": from, "name": "Text copied to clipboard"})
	a.notify("Text from "+from, "Copied to your clipboard")
}

func (a *App) status(peer, text string, isErr bool) {
	a.app.Event.Emit("status", map[string]any{"peer": peer, "text": text, "error": isErr})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
