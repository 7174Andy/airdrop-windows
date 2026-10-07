package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"airdrop/core"

	"github.com/skip2/go-qrcode"
	"golang.design/x/clipboard"
)

const usage = `usage:
  airdrop serve [-addr host:port]   run the receiver
  airdrop peers                     list tailnet peers
  airdrop pair <peer> <code>        pair using the code the peer's receiver shows
  airdrop send <peer> <path>...     send files and folders
  airdrop text <peer> [text]        send text to the peer's clipboard (stdin if no text)`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		log.Fatal(usage)
	}
	path, err := core.ConfigPath()
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := core.LoadConfig(path)
	if err != nil {
		log.Fatal(err)
	}
	cmd, args := os.Args[1], os.Args[2:]

	switch {
	case cmd == "serve":
		serve(cfg, args)
	case cmd == "peers":
		peers, err := core.Peers()
		if err != nil {
			log.Fatal(err)
		}
		for _, p := range peers {
			state := "offline"
			if p.Online {
				state = "online"
			}
			paired := ""
			if _, ok := cfg.PeerToken(p.Name); ok {
				paired = "paired"
			}
			fmt.Printf("%-24s %-16s %-8s %-8s %s\n", p.Name, p.IP, p.OS, state, paired)
		}
	case cmd == "pair" && len(args) == 2:
		p := resolve(args[0])
		name, err := core.Pair(core.Addr(p.IP, cfg.Port), args[1], cfg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("paired with", name)
	case cmd == "send" && len(args) >= 2:
		addr, token := target(cfg, args[0])
		for _, f := range args[1:] {
			if err := core.SendFile(addr, token, cfg.Name, f); err != nil {
				log.Fatalf("%s: %v", f, err)
			}
			fmt.Println("sent", f)
		}
	case cmd == "text" && len(args) >= 1:
		text := strings.Join(args[1:], " ")
		if len(args) == 1 {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				log.Fatal(err)
			}
			text = string(b)
		}
		addr, token := target(cfg, args[0])
		if err := core.SendText(addr, token, cfg.Name, text); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatal(usage)
	}
}

func serve(cfg *core.Config, args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "", "listen address (default: this device's Tailscale IP)")
	fs.Parse(args)
	if *addr == "" {
		self, err := core.Self()
		if err != nil || self.IP == "" {
			log.Fatalf("can't find this device's Tailscale IP (%v); is Tailscale running? or pass -addr", err)
		}
		*addr = net.JoinHostPort(self.IP, strconv.Itoa(cfg.Port))
	}

	s := core.NewServer(cfg)
	if err := clipboard.Init(); err != nil {
		log.Printf("clipboard unavailable (%v); received text will only be printed", err)
	} else {
		s.OnText = func(from, text string) {
			if _, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(text)); err != nil {
				log.Printf("clipboard: %v", err)
			}
			log.Printf("text from %s copied to clipboard:\n%s", from, text)
		}
	}
	if s.OnText == nil {
		s.OnText = func(from, text string) { log.Printf("text from %s:\n%s", from, text) }
	}

	log.Printf("%s listening on %s, saving to %s", cfg.Name, *addr, cfg.SaveDir)
	log.Printf("pair code: %s   (on the other device: airdrop pair %q %s)", s.PairCode(), cfg.Name, s.PairCode())
	if qr, err := qrcode.New(core.UploadURL(*addr, cfg.Token), qrcode.Low); err == nil {
		log.Printf("iPhone (with Tailscale on): scan to open the upload page\n%s", qr.ToSmallString(false))
	}
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func resolve(name string) core.Peer {
	p, err := core.Resolve(name)
	if err != nil {
		log.Fatal(err)
	}
	return p
}

func target(cfg *core.Config, name string) (addr, token string) {
	p := resolve(name)
	token, ok := cfg.PeerToken(p.Name)
	if !ok {
		log.Fatalf("not paired with %s; run its receiver and `airdrop pair %s <code>`", p.Name, name)
	}
	return core.Addr(p.IP, cfg.Port), token
}
