// The desktop app. It is a tray icon that owns the receiver and stays running in
// the background; the window is created only when you open it and destroyed when
// you close it, so receiving never depends on the window being open.
//
// Run with --background (as the login item does) to start without opening the window, and
// with --send <files> (as the right-click entry does) to open it with those files ready to send.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"airdrop/core"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed frontend
var frontend embed.FS

var (
	//go:embed icons/tray-mac.png
	trayMac []byte
	//go:embed icons/tray.png
	trayWin []byte
)

func main() {
	path, err := core.ConfigPath()
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := core.LoadConfig(path)
	if err != nil {
		log.Fatal(err)
	}
	a := &App{cfg: cfg, accepted: map[string]time.Time{}, cancels: map[int]context.CancelFunc{}}
	services := []application.Service{application.NewService(a)}
	if bundledOrNotMac() {
		a.notes = notifications.New()
		services = append(services, application.NewService(a.notes))
	}
	assets, _ := fs.Sub(frontend, "frontend")

	app := application.New(application.Options{
		Name:        "airdrop",
		Description: "Send files, folders and text between your devices",
		Services:    services,
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:         application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory}, // tray only, no Dock icon
		Windows:     application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{ // opening the app again shows the running one
			UniqueID: "dev.airdrop-windows.app",
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				application.InvokeAsync(func() {
					if a.queue(d.Args, d.WorkingDir) { // right-click → Send with airdrop
						a.emitPending()
					}
					a.ShowWindow()
				})
			},
		},
	})
	a.app = app
	a.buildTray()
	a.start()

	go func() { // keeps the tray label honest when "on for 10 minutes" runs out
		for range time.Tick(15 * time.Second) {
			application.InvokeAsync(a.refreshState)
		}
	}()
	wd, _ := os.Getwd()
	if a.queue(os.Args, wd) || !slices.Contains(os.Args[1:], "--background") {
		a.ShowWindow()
	}
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *App) buildTray() {
	tray := a.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayMac)
	} else {
		tray.SetIcon(trayWin)
	}
	tray.SetTooltip("airdrop")

	m := a.app.Menu.New()
	a.trayStatus = m.Add("").SetEnabled(false)
	a.trayOn = m.AddRadio("Receive", false).OnClick(func(*application.Context) { a.SetReceiving("on") })
	a.trayTen = m.AddRadio("Receive for 10 minutes", false).OnClick(func(*application.Context) { a.SetReceiving("10m") })
	a.trayOff = m.AddRadio("Don't receive", false).OnClick(func(*application.Context) { a.SetReceiving("off") })
	m.AddSeparator()
	m.Add("Open airdrop…").OnClick(func(*application.Context) { a.ShowWindow() })
	m.Add("Open received files").OnClick(func(*application.Context) { a.OpenSaveDir() })
	m.AddSeparator()
	m.Add("Quit airdrop").OnClick(func(*application.Context) { a.app.Quit() })
	a.trayMenu = m
	tray.SetMenu(m)
	a.refreshState()
}

// notify shows a system notification. Without a bundled .app, macOS refuses the
// notifications service, so fall back to osascript, which works from a plain binary.
func (a *App) notify(title, body string) {
	if a.notes != nil {
		id := time.Now().Format("150405.000000000")
		if err := a.notes.SendNotification(notifications.NotificationOptions{ID: id, Title: title, Body: body}); err != nil {
			log.Printf("notification: %v", err)
		}
		return
	}
	if runtime.GOOS == "darwin" {
		script := "display notification " + appleQuote(body) + " with title " + appleQuote(title)
		exec.Command("osascript", "-e", script).Start()
	}
}

// appleQuote makes s a safe AppleScript string literal (file names come from other devices).
func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ").Replace(s) + `"`
}

func bundledOrNotMac() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	exe, _ := os.Executable()
	return strings.Contains(exe, ".app/Contents/MacOS/")
}
