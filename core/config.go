package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const DefaultPort = 7878

type Config struct {
	Name    string            `json:"name"`     // how peers see this device
	Token   string            `json:"token"`    // peers must present this to talk to us
	SaveDir string            `json:"save_dir"` // where received files land
	Port    int               `json:"port"`
	Peers   map[string]string `json:"peers"` // peer name -> that peer's token

	path string
	mu   sync.Mutex
}

func ConfigPath() (string, error) {
	d, err := os.UserConfigDir()
	return filepath.Join(d, "airdrop", "config.json"), err
}

// LoadConfig reads path, filling in and saving defaults (including a fresh token) on first run.
func LoadConfig(path string) (*Config, error) {
	c := &Config{path: path}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if c.Peers == nil {
		c.Peers = map[string]string{}
	}
	if c.Token == "" {
		c.Token = randHex(16)
	}
	if c.Name == "" {
		if self, err := Self(); err == nil {
			c.Name = self.Name
		} else {
			c.Name, _ = os.Hostname()
		}
	}
	if c.SaveDir == "" {
		home, _ := os.UserHomeDir()
		c.SaveDir = filepath.Join(home, "Downloads")
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	return c, c.save()
}

func (c *Config) save() error {
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o600) // holds secrets
}

func (c *Config) AddPeer(name, token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.Peers { // re-pairing replaces the old entry, whatever its case
		if strings.EqualFold(k, name) {
			delete(c.Peers, k)
		}
	}
	c.Peers[name] = token
	return c.save()
}

// PeerToken looks a peer up case-insensitively.
func (c *Config) PeerToken(name string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.Peers {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
