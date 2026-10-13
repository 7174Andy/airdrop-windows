package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	pending  []string             // files from the right-click menu, waiting for you to pick a device

	transfers []*Transfer // what the Transfers list shows, oldest first
	nextID    int
	cancels   map[int]context.CancelFunc // transfer ID -> cancel, while waiting or sending
}

type Info struct {
	Name, Addr, PairCode, QR, SaveDir, ServerError string
	AutoAccept, StartAtLogin, ContextMenu          bool
	Pending                                        []string // files waiting to be sent
	Mode                                           string   // "on", "until", "off"
	Until                                          string   // "22:40" when Mode is "until"
}

type PeerView struct {
	Name, OS       string
	Online, Paired bool
}

// start launches the receiver on this device's Tailscale IP. At login Tailscale may
// not be up yet, so it keeps retrying; the 15s tick in main picks up the new state.
func (a *App) start() {
	a.srv = core.NewServer(a.cfg)
	a.srv.Accept = a.accept
	a.srv.OnReceive = a.onReceive
	a.srv.OnText = a.onText
	go func() {
		for {
			if ln := a.listen(); ln != nil {
				log.Printf("receiving on %s", ln.Addr())
				http.Serve(ln, a.srv.Handler())
				return
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

func (a *App) listen() net.Listener {
	var ln net.Listener
	var addr, msg string
	self, err := core.Self()
	if err != nil || self.IP == "" {
		msg = fmt.Sprintf("Can't find this device's Tailscale IP. Is Tailscale running? (%v)", err)
	} else {
		addr = net.JoinHostPort(self.IP, strconv.Itoa(a.cfg.Port))
		if ln, err = net.Listen("tcp", addr); err != nil {
			addr, msg = "", "Can't start the receiver: "+err.Error()
		}
	}
	a.mu.Lock()
	a.addr, a.serverErr = addr, msg
	a.mu.Unlock()
	return ln
}

func (a *App) server() (addr, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr, a.serverErr
}

// ---- window

// ShowWindow opens the window, creating it if it was closed.
func (a *App) ShowWindow() {
	if a.win == nil {
		w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:          "airdrop",
			Width:          900,
			Height:         600,
			MinWidth:       720,
			MinHeight:      480,
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
	if _, e := a.server(); e != "" {
		label = "⚠ Receiver not running"
	}
	if a.trayMenu != nil {
		a.trayStatus.SetLabel(label)
		a.trayOn.SetChecked(mode == "on")
		a.trayTen.SetChecked(mode == "until")
		a.trayOff.SetChecked(mode == "off")
		// On Windows the setters above already patch the tray's live menu, and Update would
		// rebuild into a detached menu that steals the items, freezing the tray from then on.
		if runtime.GOOS != "windows" {
			a.trayMenu.Update()
		}
	}
	a.app.Event.Emit("state", map[string]string{"mode": mode, "until": until})
}

// ---- called from the window

func (a *App) Info() Info {
	mode, until := a.mode()
	addr, serverErr := a.server()
	in := Info{Name: a.cfg.Name, Addr: addr, SaveDir: a.cfg.SaveDir, AutoAccept: a.cfg.AutoAccept, StartAtLogin: loginEnabled(), ContextMenu: contextMenuEnabled(), Pending: a.pendingFiles(), ServerError: serverErr, Mode: mode, Until: until}
	if addr != "" {
		in.PairCode = a.srv.PairCode()
		if png, err := qrcode.Encode(core.UploadURL(addr, a.cfg.Token), qrcode.Medium, 256); err == nil {
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
	a.addTransfer(Transfer{Up: true, Peer: name, Name: "Clipboard text", State: "done", Sent: int64(len(text)), Total: int64(len(text))})
	return nil
}

func (a *App) SetAutoAccept(on bool) error { return a.cfg.SetAutoAccept(on) }

// SetStartAtLogin adds or removes the OS login item that runs this binary with --background.
// ponytail: it and the right-click entry point at wherever this binary is now; switch them
// off and on again after moving it.
func (a *App) SetStartAtLogin(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return setLogin(on, exe)
}

// SetContextMenu adds or removes the right-click entry (Windows "Send to", macOS Quick Action)
// that runs this binary with --send <files>.
func (a *App) SetContextMenu(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return setContextMenu(on, exe)
}

// ---- files from the right-click menu

// queue takes the files after --send in args (this launch's, or a second launch's that the
// single-instance lock forwarded here) and holds them until you click a device.
func (a *App) queue(args []string, dir string) bool {
	i := slices.Index(args, "--send")
	if i < 0 || i == len(args)-1 {
		return false
	}
	files := slices.Clone(args[i+1:])
	for j, f := range files {
		if !filepath.IsAbs(f) {
			files[j] = filepath.Join(dir, f)
		}
	}
	a.mu.Lock()
	a.pending = files
	a.mu.Unlock()
	return true
}

func (a *App) pendingFiles() []string { a.mu.Lock(); defer a.mu.Unlock(); return a.pending }

func (a *App) emitPending() { a.app.Event.Emit("pending", a.pendingFiles()) }

func (a *App) SendPending(name string) error {
	a.mu.Lock()
	files := a.pending
	a.pending = nil
	a.mu.Unlock()
	a.emitPending()
	if len(files) == 0 {
		return nil
	}
	return a.Send(name, files)
}

func (a *App) ClearPending() {
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()
	a.emitPending()
}

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
	yesLabel, noLabel := "Accept", "Decline"
	if runtime.GOOS == "windows" {
		// Wails draws a plain Yes/No MessageBox there and finds the callback by the
		// clicked button's label, so any other labels mean no click ever registers.
		yesLabel, noLabel = "Yes", "No"
		what += "\n\nAccept it?"
	}
	d := a.app.Dialog.Question().
		SetTitle(from + " wants to send you").
		SetMessage(what)
	yes := d.AddButton(yesLabel).OnClick(func() { answer <- true })
	no := d.AddButton(noLabel).OnClick(func() { answer <- false })
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
	t := Transfer{Peer: from, Name: filepath.Base(path), Path: path, State: "done"}
	if st, err := os.Stat(path); err == nil {
		t.Folder = st.IsDir()
		if !t.Folder {
			t.Sent, t.Total = st.Size(), st.Size()
		}
	}
	a.addTransfer(t)
	a.notify("From "+from, filepath.Base(path))
}

func (a *App) onText(from, text string) {
	a.app.Clipboard.SetText(text)
	a.addTransfer(Transfer{Peer: from, Name: "Text copied to clipboard", State: "done", Sent: int64(len(text)), Total: int64(len(text))})
	a.notify("Text from "+from, "Copied to your clipboard")
}

func (a *App) status(peer, text string, isErr bool) {
	a.app.Event.Emit("status", map[string]any{"peer": peer, "text": text, "error": isErr})
}
