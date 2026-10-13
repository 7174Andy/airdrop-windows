package main

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLoginItem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := setLogin(true, `/Apps/Tom & Jerry's <app>/airdrop`); err != nil || !loginEnabled() {
		t.Fatalf("on: %v", err)
	}
	if out, err := exec.Command("plutil", "-lint", loginItem()).CombinedOutput(); err != nil {
		t.Fatalf("bad plist: %s", out)
	}
	if err := setLogin(false, ""); err != nil || loginEnabled() {
		t.Fatalf("off: %v", err)
	}
	if err := setLogin(false, ""); err != nil {
		t.Fatalf("off twice: %v", err)
	}
}

// Runs the installed Quick Action through automator with a stand-in binary that records its args.
func TestQuickAction(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir() + "/it's here" // a quote and a space, to exercise the shell quoting
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe, got, file := dir+"/fake", dir+"/args", dir+"/a b.txt"
	os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+got+"\"\n"), 0o755)
	os.WriteFile(file, nil, 0o644)
	if err := setContextMenu(true, exe); err != nil || !contextMenuEnabled() {
		t.Fatalf("on: %v", err)
	}
	for _, f := range []string{"Info.plist", "document.wflow"} {
		if out, err := exec.Command("plutil", "-lint", contextMenuItem()+"/Contents/"+f).CombinedOutput(); err != nil {
			t.Fatalf("bad %s: %s", f, out)
		}
	}
	if out, err := exec.Command("automator", "-i", file, contextMenuItem()).CombinedOutput(); err != nil {
		t.Fatalf("automator: %v: %s", err, out)
	}
	var b []byte
	for i := 0; i < 50 && len(b) == 0; i++ { // the script backgrounds the binary
		time.Sleep(100 * time.Millisecond)
		b, _ = os.ReadFile(got)
	}
	if want := "--send\n" + file + "\n"; string(b) != want {
		t.Fatalf("args = %q, want %q", b, want)
	}
	if err := setContextMenu(false, ""); err != nil || contextMenuEnabled() {
		t.Fatalf("off: %v", err)
	}
}
