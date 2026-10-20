// berthier-dashboard serves the Army view model (`berthier army`) as a live
// console page. It holds no state and no logic of its own: it polls the command
// and pushes each change to open pages over server-sent events. The one write
// is POST /api/prompt, which sends the user's text to a known Agent.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

// Army keeps the latest view model and fans changes out to subscribers.
type Army struct {
	mu     sync.Mutex
	latest []byte
	subs   map[chan []byte]struct{}
}

func NewArmy() *Army { return &Army{subs: map[chan []byte]struct{}{}} }

// Set stores a new view model and notifies subscribers if it changed.
func (a *Army) Set(v []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if bytes.Equal(v, a.latest) {
		return
	}
	a.latest = v
	for ch := range a.subs {
		select {
		case ch <- v:
		default: // a slow page catches up on the next change
		}
	}
}

func (a *Army) Latest() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.latest
}

func (a *Army) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 1)
	a.mu.Lock()
	a.subs[ch] = struct{}{}
	a.mu.Unlock()
	return ch, func() {
		a.mu.Lock()
		delete(a.subs, ch)
		a.mu.Unlock()
	}
}

// Poll runs `<berthier> army` every interval until ctx ends.
func Poll(ctx context.Context, a *Army, berthier string, every time.Duration) {
	for {
		out, err := exec.CommandContext(ctx, berthier, "army").Output()
		if err != nil {
			log.Printf("berthier army: %v", err)
		} else {
			a.Set(bytes.TrimSpace(out))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Panes lists the pane ids of every Agent in the latest view model.
func (a *Army) Panes() map[string]bool {
	type agents struct {
		Agents []struct {
			Pane string `json:"pane_id"`
		} `json:"agents"`
	}
	var v struct {
		Corps []struct {
			agents
			Divisions []agents `json:"divisions"`
		} `json:"corps"`
		Berthier *struct {
			Pane string `json:"pane_id"`
		} `json:"berthier"`
		BerthierDivisions []agents `json:"berthier_divisions"`
	}
	panes := map[string]bool{}
	if json.Unmarshal(a.Latest(), &v) != nil {
		return panes
	}
	add := func(u agents) {
		for _, x := range u.Agents {
			panes[x.Pane] = true
		}
	}
	for _, c := range v.Corps {
		add(c.agents)
		for _, d := range c.Divisions {
			add(d)
		}
	}
	if v.Berthier != nil {
		panes[v.Berthier.Pane] = true
	}
	for _, d := range v.BerthierDivisions {
		add(d)
	}
	return panes
}

// Herdr runs `herdr` against the Army's session (BERTHIER_HERDR_SESSION, as
// lib/common.sh's hd does).
func Herdr(herdr string, args ...string) *exec.Cmd {
	if s := os.Getenv("BERTHIER_HERDR_SESSION"); s != "" {
		cmd := exec.Command(herdr, append([]string{"--session", s}, args...)...)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "HERDR_SOCKET_PATH=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		return cmd
	}
	return exec.Command(herdr, args...)
}

func Handler(a *Army, herdr string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/army", func(w http.ResponseWriter, r *http.Request) {
		v := a.Latest()
		if v == nil {
			http.Error(w, "no data yet", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(v)
	})
	// JSON only: a cross-site form cannot send it without a CORS preflight,
	// which this server never answers.
	mux.HandleFunc("POST /api/prompt", func(w http.ResponseWriter, r *http.Request) {
		if t, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); t != "application/json" {
			http.Error(w, "want application/json", http.StatusUnsupportedMediaType)
			return
		}
		var req struct{ Pane, Text string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			http.Error(w, "empty message", http.StatusBadRequest)
			return
		}
		if !a.Panes()[req.Pane] {
			http.Error(w, "no such agent: "+req.Pane, http.StatusNotFound)
			return
		}
		out, err := Herdr(herdr, "agent", "prompt", req.Pane, req.Text).CombinedOutput()
		if err != nil {
			log.Printf("herdr agent prompt %s: %v: %s", req.Pane, err, out)
			http.Error(w, fmt.Sprintf("herdr agent prompt: %v: %s", err, bytes.TrimSpace(out)), http.StatusBadGateway)
			return
		}
		log.Printf("prompted %s (%d bytes)", req.Pane, len(req.Text))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ch, cancel := a.Subscribe()
		defer cancel()
		send := func(v []byte) {
			fmt.Fprintf(w, "data: %s\n\n", v)
			flusher.Flush()
		}
		if v := a.Latest(); v != nil {
			send(v)
		}
		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case v := <-ch:
				send(v)
			case <-keepalive.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	})
	return mux
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8430", "listen address")
	berthier := flag.String("berthier", "berthier", "path to the berthier command")
	herdr := flag.String("herdr", "herdr", "path to the herdr command")
	every := flag.Duration("every", 2*time.Second, "how often to refresh the view model")
	flag.Parse()

	a := NewArmy()
	go Poll(context.Background(), a, *berthier, *every)
	log.Printf("serving the Army console on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, Handler(a, *herdr)))
}
