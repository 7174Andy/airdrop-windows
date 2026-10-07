package core

import (
	"archive/tar"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestSendDir(t *testing.T) {
	a, b := newCfg(t, "A"), newCfg(t, "B")
	srv := NewServer(a)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	Pair(ts.URL, srv.PairCode(), b)
	tok, _ := b.PeerToken("A")

	src := filepath.Join(t.TempDir(), "photos")
	os.MkdirAll(filepath.Join(src, "sub", "deep"), 0o755)
	os.MkdirAll(filepath.Join(src, "empty"), 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "deep", "b.txt"), []byte("BB"), 0o644)
	os.Symlink("/etc/passwd", filepath.Join(src, "link")) // must not travel

	for _, want := range []string{"photos", "photos (1)"} { // second send must not merge into the first
		if err := SendFile(ts.URL, tok, "B", src); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(a.SaveDir, want)
		for name, body := range map[string]string{"a.txt": "A", "sub/deep/b.txt": "BB"} {
			if got, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(got) != body {
				t.Fatalf("%s/%s: %q %v", want, name, got, err)
			}
		}
		if st, err := os.Stat(filepath.Join(root, "empty")); err != nil || !st.IsDir() {
			t.Fatalf("%s/empty dir missing", want)
		}
		if _, err := os.Lstat(filepath.Join(root, "link")); err == nil {
			t.Fatalf("%s/link: symlink was transferred", want)
		}
	}
}

// A malicious sender hand-crafts the tar; the receiver must refuse it and leave nothing behind.
func TestDirRejectsEvilTar(t *testing.T) {
	a := newCfg(t, "A")
	ts := httptest.NewServer(NewServer(a).Handler())
	defer ts.Close()

	for _, evil := range []tar.Header{
		{Name: "../escape.txt", Typeflag: tar.TypeReg, Size: 1},
		{Name: "ok/../../escape.txt", Typeflag: tar.TypeReg, Size: 1},
		{Name: "/abs.txt", Typeflag: tar.TypeReg, Size: 1},
		{Name: `..\escape.txt`, Typeflag: tar.TypeReg, Size: 1},
		{Name: "lnk", Typeflag: tar.TypeSymlink, Linkname: "/etc"},
	} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(&tar.Header{Name: "fine.txt", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644})
		tw.Write([]byte("x"))
		evil.Mode = 0o644
		tw.WriteHeader(&evil)
		if evil.Size > 0 {
			tw.Write([]byte("x"))
		}
		tw.Close()

		resp, err := post(ts.URL+"/dir?name=evil", a.Token, "mallory", &buf, int64(buf.Len()))
		if err == nil {
			resp.Body.Close()
			t.Errorf("%q accepted", evil.Name)
		}
	}
	if _, err := post(ts.URL+"/dir?name=../up", a.Token, "mallory", strings.NewReader(""), 0); err == nil {
		t.Error("unsafe folder name accepted")
	}
	// Nothing escaped, and no half-written folders remain.
	if _, err := os.Stat(filepath.Join(filepath.Dir(a.SaveDir), "escape.txt")); err == nil {
		t.Error("file escaped the save folder")
	}
	if left, _ := os.ReadDir(a.SaveDir); len(left) != 0 {
		t.Errorf("leftovers in save folder: %v", left)
	}
}

// The iPhone page is public (it holds no secrets); what it uploads still needs the token.
func TestUploadPage(t *testing.T) {
	a := newCfg(t, "A")
	ts := httptest.NewServer(NewServer(a).Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `type="file"`) || strings.Contains(string(body), a.Token) {
		t.Fatalf("GET /: %d, page ok=%v, leaks token=%v", resp.StatusCode, strings.Contains(string(body), `type="file"`), strings.Contains(string(body), a.Token))
	}
	if resp, _ := http.Get(ts.URL + "/nope"); resp.StatusCode != 404 {
		t.Fatalf("GET /nope: %d", resp.StatusCode)
	}
	if !strings.HasSuffix(UploadURL("100.1.2.3:7878", a.Token), "100.1.2.3:7878/?t="+a.Token) {
		t.Fatal(UploadURL("100.1.2.3:7878", a.Token))
	}
}
