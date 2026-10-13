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
	"time"
)

const DefaultPort = 7878

type Config struct {
	Name    string            `json:"name"`     // how peers see this device
	Token   string            `json:"token"`    // peers must present this to talk to us
	SaveDir string            `json:"save_dir"` // where received files land
	Port    int               `json:"port"`
	Peers   map[string]string `json:"peers"` // peer name -> that peer's token
	// AutoAccept skips the desktop app's Accept/Decline prompt. The CLI always accepts.
	AutoAccept bool `json:"auto_accept"`
	// Receiving: off, or on until a deadline (zero = no deadline). Checked on every request,
	// so no timer is needed for it to switch off.
	Off   bool      `json:"off,omitempty"`
	Until time.Time `json:"until,omitempty"`

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

func (c *Config) SetAutoAccept(on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AutoAccept = on
	return c.save()
}

func (c *Config) Receiving() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.Off && (c.Until.IsZero() || time.Now().Before(c.Until))
}

// SetReceiving switches receiving on (until the deadline, if non-zero) or off.
func (c *Config) SetReceiving(on bool, until time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Off, c.Until = !on, until
	if !on {
		c.Until = time.Time{}
	}
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
