// berthier-dashboard serves the Army view model (`berthier army`) as a live,
// read-only console page. It holds no state and no logic of its own: it polls
// the command and pushes each change to open pages over server-sent events.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/exec"
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

func Handler(a *Army) http.Handler {
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
	every := flag.Duration("every", 2*time.Second, "how often to refresh the view model")
	flag.Parse()

	a := NewArmy()
	go Poll(context.Background(), a, *berthier, *every)
	log.Printf("serving the Army console on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, Handler(a)))
}
