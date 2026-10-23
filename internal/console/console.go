// Package console serves the Grand Army view model (`berthier army`) as a
// live console page. It holds no state and no logic of its own: it polls the
// view model and pushes each change to open pages over server-sent events. Its writes are POST /api/prompt, which sends the user's text to a known Agent, and POST /api/budget, which sets a Corps's weekly budget; GET
// /api/chat shows that Agent's conversation (chat.go) and GET /api/skills lists
// what its Claude Code can run as /<name> (skills.go). app.go makes the page an
// installable app.
package console

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"berthier/internal/herdr"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

// BudgetSetter sets a Corps's weekly budget (% of the week), or drops it for a
// nil pct; a request-caused refusal has a Bad() method reporting true.
type BudgetSetter func(corps string, pct *float64) error

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

// Poll reads the view model from source every interval until ctx ends.
func Poll(ctx context.Context, a *Army, source func() ([]byte, error), every time.Duration) {
	for {
		out, err := source()
		if err != nil {
			log.Printf("army: %v", err)
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

// Corps lists the Corps in the latest view model's quota panel.
func (a *Army) Corps() map[string]bool {
	var v struct {
		Budget struct {
			Corps []struct {
				Name string `json:"name"`
			} `json:"corps"`
		} `json:"budget"`
	}
	names := map[string]bool{}
	if json.Unmarshal(a.Latest(), &v) != nil {
		return names
	}
	for _, c := range v.Budget.Corps {
		names[c.Name] = true
	}
	return names
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

// unscrolledLines caps a read of pane at want lines to what its screen and
// host scrollback hold: asked for more, Herdr scrolls an idle alt-screen
// Agent's view up and back to collect its history, and the user sees it jump.
func unscrolledLines(h herdr.Client, pane string, want int) int {
	var got struct {
		Result struct {
			Pane struct {
				Scroll struct {
					Rows int `json:"viewport_rows"`
					Back int `json:"max_offset_from_bottom"`
				}
			}
		}
	}
	out, err := h.Command("pane", "get", pane).Output()
	if err != nil || json.Unmarshal(out, &got) != nil || got.Result.Pane.Scroll.Rows == 0 {
		return want
	}
	return min(want, got.Result.Pane.Scroll.Rows+got.Result.Pane.Scroll.Back)
}

// Agent is what Herdr says about the Agent in a pane.
type Agent struct{ Kind, Status, Session string }

// GetAgent is `herdr agent get <pane>`.
func GetAgent(h herdr.Client, pane string) (Agent, error) {
	var got struct {
		Result struct {
			Agent struct {
				Agent        string `json:"agent"`
				AgentStatus  string `json:"agent_status"`
				AgentSession struct {
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"agent"`
		} `json:"result"`
	}
	if err := h.JSON(&got, "agent", "get", pane); err != nil {
		return Agent{}, fmt.Errorf("herdr agent get: %v", err)
	}
	ag := got.Result.Agent
	return Agent{ag.Agent, ag.AgentStatus, ag.AgentSession.Value}, nil
}

func Handler(a *Army, h herdr.Client, setBudget BudgetSetter, tr *Transcripts) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(themed(indexHTML))
	})
	appRoutes(mux)
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
		if err := h.PromptAgent(req.Pane, req.Text); err != nil {
			log.Printf("prompt %s: %v", req.Pane, err)
			http.Error(w, "prompt: "+err.Error(), http.StatusBadGateway)
			return
		}
		log.Printf("prompted %s (%d bytes)", req.Pane, len(req.Text))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/budget", func(w http.ResponseWriter, r *http.Request) {
		if t, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); t != "application/json" {
			http.Error(w, "want application/json", http.StatusUnsupportedMediaType)
			return
		}
		var req struct {
			Corps string
			Pct   *float64
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if setBudget == nil || !a.Corps()[req.Corps] {
			http.Error(w, "no such corps: "+req.Corps, http.StatusNotFound)
			return
		}
		if req.Pct != nil && !(*req.Pct > 0 && *req.Pct <= 100) {
			http.Error(w, "pct must be in (0, 100], or null to drop", http.StatusBadRequest)
			return
		}
		if err := setBudget(req.Corps, req.Pct); err != nil {
			code := http.StatusInternalServerError
			if b, ok := err.(interface{ Bad() bool }); ok && b.Bad() {
				code = http.StatusBadRequest
			}
			http.Error(w, err.Error(), code)
			return
		}
		if req.Pct == nil {
			log.Printf("budget %s dropped", req.Corps)
		} else {
			log.Printf("budget %s: %g%%", req.Corps, *req.Pct)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/chat", func(w http.ResponseWriter, r *http.Request) {
		pane := r.URL.Query().Get("pane")
		if !a.Panes()[pane] {
			http.Error(w, "no such agent: "+pane, http.StatusNotFound)
			return
		}
		ag, err := GetAgent(h, pane)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		res := map[string]any{"status": ag.Status, "kind": ag.Kind}
		// A Claude Code Agent shows its transcript; anything else, its terminal.
		if ag.Kind == "claude" && ag.Session != "" {
			if path := tr.Path(ag.Session); path != "" {
				if items, rev, err := tr.Read(path); err == nil {
					res["source"], res["session"], res["rev"], res["items"] = "transcript", strings.TrimSuffix(filepath.Base(path), ".jsonl"), rev, items
					res["info"] = tr.Info(path)
				}
			}
		}
		if res["source"] == nil {
			text, err := h.Command("agent", "read", pane, "--source", "recent-unwrapped", "--lines", strconv.Itoa(unscrolledLines(h, pane, 200))).Output()
			if err != nil {
				http.Error(w, fmt.Sprintf("herdr agent read: %v", err), http.StatusBadGateway)
				return
			}
			h := fnv.New64a()
			h.Write(text)
			res["source"], res["rev"], res["text"] = "terminal", h.Sum64()>>1, string(text)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	})
	mux.HandleFunc("GET /api/skills", func(w http.ResponseWriter, r *http.Request) {
		pane := r.URL.Query().Get("pane")
		if !a.Panes()[pane] {
			http.Error(w, "no such agent: "+pane, http.StatusNotFound)
			return
		}
		var got struct {
			Result struct {
				Agent struct {
					Cwd string `json:"cwd"`
				} `json:"agent"`
			} `json:"result"`
		}
		if out, err := h.Command("agent", "get", pane).Output(); err == nil {
			json.Unmarshal(out, &got)
		}
		claudeDir := ""
		if tr != nil {
			claudeDir = tr.dir
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"skills": Skills(claudeDir, got.Result.Agent.Cwd)})
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

// Serve runs the Console on addr until it fails.
func Serve(addr string, source func() ([]byte, error), h herdr.Client, setBudget BudgetSetter, claudeDir string, every time.Duration) error {
	a := NewArmy()
	go Poll(context.Background(), a, source, every)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	// The bound address, so -addr 127.0.0.1:0 says which port it got.
	log.Printf("serving the Grand Army console on http://%s", l.Addr())
	return http.Serve(l, Handler(a, h, setBudget, NewTranscripts(claudeDir)))
}
