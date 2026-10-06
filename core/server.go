package core

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxPairFails = 3

type Server struct {
	Cfg    *Config
	OnText func(from, text string) // e.g. put it on the clipboard

	mu        sync.Mutex
	pairCode  string
	pairFails int
}

func NewServer(cfg *Config) *Server {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return &Server{Cfg: cfg, pairCode: fmt.Sprintf("%06d", n)}
}

// PairCode is the 6-digit code another device types to pair with this one. New on each start.
func (s *Server) PairCode() string { return s.pairCode }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"name": s.Cfg.Name})
	})
	mux.HandleFunc("POST /pair", s.pair)
	mux.HandleFunc("POST /file", s.auth(s.file))
	mux.HandleFunc("POST /text", s.auth(s.text))
	return mux
}

type pairMsg struct{ Name, Token, Code string }

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	var m pairMsg
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&m); err != nil || m.Name == "" || m.Token == "" {
		http.Error(w, "bad pair request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pairFails >= maxPairFails {
		http.Error(w, "pairing locked after too many wrong codes; restart the receiver", http.StatusForbidden)
		return
	}
	if subtle.ConstantTimeCompare([]byte(m.Code), []byte(s.pairCode)) != 1 {
		s.pairFails++
		http.Error(w, "wrong pair code", http.StatusForbidden)
		return
	}
	if err := s.Cfg.AddPeer(m.Name, m.Token); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("paired with %s", m.Name)
	json.NewEncoder(w).Encode(pairMsg{Name: s.Cfg.Name, Token: s.Cfg.Token})
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	want := []byte("Bearer " + s.Cfg.Token)
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "not paired", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	dst, err := safePath(s.Cfg.SaveDir, r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".airdrop-*.part")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	n, err := io.Copy(tmp, r.Body)
	tmp.Close()
	if err == nil && r.ContentLength >= 0 && n != r.ContentLength {
		err = fmt.Errorf("got %d of %d bytes", n, r.ContentLength)
	}
	if err == nil {
		dst = uniquePath(dst)
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("received %s from %s (%d bytes)", dst, r.Header.Get("X-From"), n)
}

func (s *Server) text(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if s.OnText != nil {
		s.OnText(r.Header.Get("X-From"), string(b))
	}
}

// safePath joins a sender-supplied relative path onto dir, refusing anything
// that could escape it. This is a trust boundary.
func safePath(dir, name string) (string, error) {
	rel := filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))
	// ':' blocks Windows drive letters ("C:x") and alternate data streams.
	if name == "" || strings.Contains(name, ":") || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("unsafe file name %q", name)
	}
	return filepath.Join(dir, rel), nil
}

// uniquePath turns "a.txt" into "a (1).txt" etc. if it already exists.
// ponytail: check-then-rename race if two senders deliver the same name at once; last one wins.
func uniquePath(p string) string {
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
		p = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
}
