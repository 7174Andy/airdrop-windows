package core

// The only file that knows about Tailscale. Swap for mDNS when Tailscale goes.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Peer struct {
	Name   string // Tailscale HostName, e.g. "LAPTOP-M8NLMMGL"
	DNS    string // first MagicDNS label, e.g. "laptop-m8nlmmgl"
	IP     string
	OS     string
	Online bool
}

type tsNode struct {
	HostName     string
	DNSName      string
	OS           string
	Online       bool
	TailscaleIPs []string
}

func (n tsNode) peer() Peer {
	p := Peer{Name: n.HostName, DNS: strings.SplitN(n.DNSName, ".", 2)[0], OS: n.OS, Online: n.Online}
	if len(n.TailscaleIPs) > 0 {
		p.IP = n.TailscaleIPs[0]
	}
	return p
}

func status() (self Peer, peers []Peer, err error) {
	bin := "tailscale"
	if _, err := exec.LookPath(bin); err != nil {
		if mac := "/Applications/Tailscale.app/Contents/MacOS/Tailscale"; fileExists(mac) {
			bin = mac
		}
	}
	out, err := exec.Command(bin, "status", "--json").Output()
	if err != nil {
		return self, nil, fmt.Errorf("tailscale status: %w", err)
	}
	var st struct {
		Self tsNode
		Peer map[string]tsNode
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return self, nil, err
	}
	for _, n := range st.Peer {
		peers = append(peers, n.peer())
	}
	return st.Self.peer(), peers, nil
}

func Self() (Peer, error) {
	self, _, err := status()
	return self, err
}

func Peers() ([]Peer, error) {
	_, peers, err := status()
	return peers, err
}

// Resolve finds a peer by host name, MagicDNS label, or IP.
func Resolve(name string) (Peer, error) {
	peers, err := Peers()
	if err != nil {
		return Peer{}, err
	}
	for _, p := range peers {
		if strings.EqualFold(p.Name, name) || strings.EqualFold(p.DNS, name) || p.IP == name {
			return p, nil
		}
	}
	return Peer{}, fmt.Errorf("no peer named %q (try `airdrop peers`)", name)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
