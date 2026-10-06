package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Addr is the base URL of a peer's receiver.
func Addr(host string, port int) string { return fmt.Sprintf("http://%s:%d", host, port) }

func post(u, token, from string, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequest("POST", u, body)
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

func SendFile(addr, token, from, path string) error {
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
		return fmt.Errorf("%s is a folder; folder send comes in phase 2", path)
	}
	resp, err := post(addr+"/file?name="+url.QueryEscape(filepath.Base(path)), token, from, f, st.Size())
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func SendText(addr, token, from, text string) error {
	resp, err := post(addr+"/text", token, from, strings.NewReader(text), int64(len(text)))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Pair swaps tokens with the receiver at addr using the code it shows, and saves its token in cfg.
func Pair(addr, code string, cfg *Config) (peerName string, err error) {
	b, _ := json.Marshal(pairMsg{Name: cfg.Name, Token: cfg.Token, Code: code})
	resp, err := post(addr+"/pair", "", cfg.Name, bytes.NewReader(b), int64(len(b)))
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
