package core

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSafePath(t *testing.T) {
	for _, bad := range []string{"", "../x", "a/../../x", `..\..\x`, "/etc/passwd", `C:\Windows\x`, "C:x", "f.txt:stream"} {
		if _, err := safePath("/save", bad); err == nil {
			t.Errorf("safePath(%q) accepted", bad)
		}
	}
	for _, ok := range []string{"a.txt", "dir/a.txt", `dir\a.txt`, "a..b"} {
		if _, err := safePath("/save", ok); err != nil {
			t.Errorf("safePath(%q) rejected: %v", ok, err)
		}
	}
}

func newCfg(t *testing.T, name string) *Config {
	dir := t.TempDir()
	c, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	c.Name, c.SaveDir = name, filepath.Join(dir, "save")
	return c
}

func TestPairSendReceive(t *testing.T) {
	a, b := newCfg(t, "A"), newCfg(t, "B")
	srv := NewServer(a)
	var gotText string
	srv.OnText = func(from, text string) { gotText = from + ":" + text }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	if _, err := Pair(ts.URL, "000000x", b); err == nil {
		t.Fatal("wrong pair code accepted")
	}
	name, err := Pair(ts.URL, srv.PairCode(), b)
	if err != nil || name != "A" {
		t.Fatalf("pair: %q %v", name, err)
	}
	if tok, _ := a.PeerToken("b"); tok != b.Token {
		t.Fatal("A did not store B's token")
	}
	tok, _ := b.PeerToken("A")
	if tok != a.Token {
		t.Fatal("B did not store A's token")
	}

	src := filepath.Join(t.TempDir(), "hello.txt")
	os.WriteFile(src, []byte("hi there"), 0o644)
	if err := SendFile(ts.URL, tok, "B", src); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(a.SaveDir, "hello.txt")); string(got) != "hi there" {
		t.Fatalf("received %q", got)
	}
	if err := SendFile(ts.URL, tok, "B", src); err != nil { // name clash → "hello (1).txt"
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.SaveDir, "hello (1).txt")); err != nil {
		t.Fatal(err)
	}

	if err := SendText(ts.URL, tok, "B", "clip"); err != nil || gotText != "B:clip" {
		t.Fatalf("text: %q %v", gotText, err)
	}
	if err := SendText(ts.URL, "wrong", "B", "x"); err == nil {
		t.Fatal("bad token accepted")
	}

	// Brute-force guard: after 3 wrong codes, even the right one is refused.
	for range 3 {
		Pair(ts.URL, "bad", b)
	}
	if _, err := Pair(ts.URL, srv.PairCode(), b); err == nil {
		t.Fatal("pairing not locked after failures")
	}
}
