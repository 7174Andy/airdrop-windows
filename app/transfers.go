package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"airdrop/core"
)

// The window's Transfers list. Sends report byte progress as they go; received
// items arrive already done. Every change is pushed to the window as a "transfer" event.

// ponytail: the oldest entries fall off past this, even unfinished ones (only with 50+ queued).
const keepTransfers = 50

type Transfer struct {
	ID          int
	Up          bool // sent from here; false = received
	Peer, Name  string
	Path        string // the file sent, or where a received one landed; "" for text
	Folder      bool
	State       string // "waiting", "sending", "done", "failed", "cancelled"
	Sent, Total int64
	Error       string
	At          time.Time // when it was queued or received
}

func (a *App) addTransfer(t Transfer) *Transfer {
	a.mu.Lock()
	a.nextID++
	t.ID, t.At = a.nextID, time.Now()
	p := &t
	a.transfers = append(a.transfers, p)
	if len(a.transfers) > keepTransfers {
		a.transfers = a.transfers[1:]
	}
	c := *p
	a.mu.Unlock()
	a.app.Event.Emit("transfer", c)
	return p
}

func (a *App) updateTransfer(t *Transfer, f func(*Transfer)) {
	a.mu.Lock()
	f(t)
	c := *t
	a.mu.Unlock()
	a.app.Event.Emit("transfer", c)
}

func (a *App) Transfers() []Transfer {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Transfer, len(a.transfers))
	for i, t := range a.transfers {
		out[i] = *t
	}
	return out
}

func (a *App) CancelTransfer(id int) {
	a.mu.Lock()
	cancel := a.cancels[id]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ClearFinished drops everything that isn't waiting or sending.
func (a *App) ClearFinished() {
	a.mu.Lock()
	defer a.mu.Unlock()
	keep := a.transfers[:0]
	for _, t := range a.transfers {
		if t.State == "waiting" || t.State == "sending" {
			keep = append(keep, t)
		}
	}
	a.transfers = keep
}

// Send queues files and folders for a peer, then sends them one at a time.
// A failure doesn't stop the rest; the first error is returned.
func (a *App) Send(name string, paths []string) error {
	addr, token, err := a.target(name)
	if err != nil {
		a.status(name, err.Error(), true)
		return err
	}
	ts := make([]*Transfer, len(paths))
	ctxs := make([]context.Context, len(paths))
	for i, p := range paths {
		st, _ := os.Stat(p)
		ts[i] = a.addTransfer(Transfer{Up: true, Peer: name, Name: filepath.Base(p), Path: p, Folder: st != nil && st.IsDir(), State: "waiting"})
		var cancel context.CancelFunc
		ctxs[i], cancel = context.WithCancel(context.Background())
		a.mu.Lock()
		a.cancels[ts[i].ID] = cancel
		a.mu.Unlock()
	}
	var first error
	for i, t := range ts {
		if err := a.sendOne(ctxs[i], addr, token, t); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (a *App) sendOne(ctx context.Context, addr, token string, t *Transfer) error {
	defer func() {
		a.mu.Lock()
		cancel := a.cancels[t.ID]
		delete(a.cancels, t.ID)
		a.mu.Unlock()
		cancel()
	}()
	if ctx.Err() == nil { // not cancelled while waiting
		a.updateTransfer(t, func(t *Transfer) { t.State = "sending" })
	}
	var last time.Time
	err := core.Send(ctx, addr, token, a.cfg.Name, t.Path, func(sent, total int64) {
		if sent < total && time.Since(last) < 150*time.Millisecond { // throttle: a read is ~32 KB
			return
		}
		last = time.Now()
		a.updateTransfer(t, func(t *Transfer) { t.Sent, t.Total = sent, total })
	})
	cancelled := ctx.Err() != nil
	a.updateTransfer(t, func(t *Transfer) {
		switch {
		case cancelled:
			t.State = "cancelled"
		case err != nil:
			t.State, t.Error = "failed", err.Error()
		default:
			t.State, t.Sent = "done", t.Total
		}
	})
	if cancelled {
		return nil
	}
	return err
}
