package core

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Addr is the base URL of a peer's receiver.
func Addr(host string, port int) string { return fmt.Sprintf("http://%s:%d", host, port) }

func post(ctx context.Context, u, token, from string, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", u, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-From", from)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// Progress is told how many bytes of file content have been sent so far, out of
// total. It runs on the sending goroutine, once per read, so throttle anything slow.
type Progress func(sent, total int64)

// counter is an io.Writer for io.TeeReader: it sees every byte read from a file.
type counter struct {
	sent, total int64
	fn          Progress
}

func (c *counter) Write(p []byte) (int, error) {
	c.sent += int64(len(p))
	if c.fn != nil {
		c.fn(c.sent, c.total)
	}
	return len(p), nil
}

// SendFile sends a file, or a whole folder as a streamed tar.
func SendFile(addr, token, from, path string) error {
	return Send(context.Background(), addr, token, from, path, nil)
}

// Send is SendFile with progress (nil for none); cancelling ctx aborts the transfer.
func Send(ctx context.Context, addr, token, from, path string, progress Progress) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return sendDir(ctx, addr, token, from, path, progress)
	}
	c := &counter{total: st.Size(), fn: progress}
	resp, err := post(ctx, addr+"/file?name="+url.QueryEscape(filepath.Base(path)), token, from, io.TeeReader(f, c), st.Size())
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// sendDir streams the folder as a tar (no temp file). Symlinks and other
// non-regular files are skipped; the receiver would reject them.
func sendDir(ctx context.Context, addr, token, from, dir string, progress Progress) error {
	c := &counter{fn: progress}
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error { // total, for progress
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				c.total += info.Size()
			}
		}
		return nil
	})
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(writeTar(pw, dir, c)) }()
	resp, err := post(ctx, addr+"/dir?name="+url.QueryEscape(filepath.Base(dir)), token, from, pr, -1)
	pr.Close() // unblocks writeTar if the receiver bailed early
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func writeTar(w io.Writer, dir string, c *counter) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // symlink, socket, ...
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		h.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil || d.IsDir() {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, io.TeeReader(f, c))
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func SendText(addr, token, from, text string) error {
	resp, err := post(context.Background(), addr+"/text", token, from, strings.NewReader(text), int64(len(text)))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Pair swaps tokens with the receiver at addr using the code it shows, and saves its token in cfg.
func Pair(addr, code string, cfg *Config) (peerName string, err error) {
	b, _ := json.Marshal(pairMsg{Name: cfg.Name, Token: cfg.Token, Code: code})
	resp, err := post(context.Background(), addr+"/pair", "", cfg.Name, bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var m pairMsg
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&m); err != nil || m.Name == "" || m.Token == "" {
		return "", fmt.Errorf("bad pair response")
	}
	return m.Name, cfg.AddPeer(m.Name, m.Token)
}
