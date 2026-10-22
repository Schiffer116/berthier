package console

import (
	"berthier/internal/herdr"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSource is an army source whose output is a file the test changes.
func fakeSource(t *testing.T, json string) (source func() ([]byte, error), data string) {
	data = filepath.Join(t.TempDir(), "army.json")
	os.WriteFile(data, []byte(json), 0o644)
	return func() ([]byte, error) { return os.ReadFile(data) }, data
}

// fakeClaude copies tests/fake-herdr, a Herdr whose one Claude takes prompts
// the given way, into a temp dir that also holds its state.
func fakeClaude(t *testing.T, mode string) (h herdr.Client, dir string) {
	src, err := os.ReadFile("../../tests/fake-herdr")
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	script := filepath.Join(dir, "herdr")
	os.WriteFile(script, src, 0o755)
	os.WriteFile(filepath.Join(dir, "mode"), []byte(mode), 0o644)
	return herdr.Client{Bin: script, PromptPoll: time.Millisecond}, dir
}

func TestServesLatestAndPushesChanges(t *testing.T) {
	source, data := fakeSource(t, `{"v":1}`)
	a := NewArmy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Poll(ctx, a, source, 20*time.Millisecond)
	srv := httptest.NewServer(Handler(a, herdr.Client{}, nil))
	defer srv.Close()

	for a.Latest() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	res, err := http.Get(srv.URL + "/api/army")
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := io.ReadAll(res.Body); string(body) != `{"v":1}` {
		t.Fatalf("api/army = %q", body)
	}

	ev, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Body.Close()
	lines := bufio.NewScanner(ev.Body)
	next := func() string {
		for lines.Scan() {
			if l := lines.Text(); strings.HasPrefix(l, "data: ") {
				return strings.TrimPrefix(l, "data: ")
			}
		}
		t.Fatal("event stream ended")
		return ""
	}
	if got := next(); got != `{"v":1}` {
		t.Fatalf("first event = %q", got)
	}
	os.WriteFile(data, []byte(`{"v":2}`), 0o644)
	if got := next(); got != `{"v":2}` {
		t.Fatalf("change event = %q", got)
	}
}

func TestServesPage(t *testing.T) {
	srv := httptest.NewServer(Handler(NewArmy(), herdr.Client{}, nil))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET / = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res, _ := http.Get(srv.URL + "/api/army"); res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("api/army before data = %d", res.StatusCode)
	}
}

const promptArmy = `{"corps":[{"agents":[{"pane_id":"wA:p1"}],"divisions":[{"agents":[{"pane_id":"wB:p1"}]}]}],
	"berthier":{"pane_id":"wC:p1"},"berthier_divisions":[{"agents":[{"pane_id":"wD:p1"}]}]}`

func postPrompt(t *testing.T, url, ctype, body string) *http.Response {
	res, err := http.Post(url+"/api/prompt", ctype, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPromptSendsToKnownAgents(t *testing.T) {
	h, dir := fakeClaude(t, "takes")
	a := NewArmy()
	a.Set([]byte(promptArmy))
	srv := httptest.NewServer(Handler(a, h, nil))
	defer srv.Close()
	t.Setenv("BERTHIER_HERDR_SESSION", "")

	for _, pane := range []string{"wA:p1", "wB:p1", "wC:p1", "wD:p1"} {
		text := "hi " + pane + "; $(touch /tmp/x) 'quoted'\nsecond line"
		body, _ := json.Marshal(map[string]string{"pane": pane, "text": text})
		if res := postPrompt(t, srv.URL, "application/json", string(body)); res.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: status %d", pane, res.StatusCode)
		}
		calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
		if !strings.Contains(string(calls), "agent prompt "+pane+" ") {
			t.Fatalf("%s: herdr calls %q", pane, calls)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "text")); string(got) != text {
			t.Fatalf("%s: prompted %q, want %q", pane, got, text)
		}
	}
}

func TestPromptUsesBerthierSession(t *testing.T) {
	h, dir := fakeClaude(t, "takes")
	h.Session = "scratch"
	a := NewArmy()
	a.Set([]byte(promptArmy))
	srv := httptest.NewServer(Handler(a, h, nil))
	defer srv.Close()

	if res := postPrompt(t, srv.URL, "application/json", `{"pane":"wA:p1","text":"go"}`); res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", res.StatusCode)
	}
	if calls, _ := os.ReadFile(filepath.Join(dir, "calls")); !strings.Contains(string(calls), "--session scratch agent prompt wA:p1 go\n") {
		t.Fatalf("herdr calls %q", calls)
	}
}

func TestPromptRejects(t *testing.T) {
	h, dir := fakeClaude(t, "takes")
	a := NewArmy()
	a.Set([]byte(promptArmy))
	srv := httptest.NewServer(Handler(a, h, nil))
	defer srv.Close()

	for _, c := range []struct {
		name, ctype, body string
		status            int
	}{
		{"unknown pane", "application/json", `{"pane":"wZ:p9","text":"hi"}`, http.StatusNotFound},
		{"flag as pane", "application/json", `{"pane":"--help","text":"hi"}`, http.StatusNotFound},
		{"empty text", "application/json", `{"pane":"wA:p1","text":"  "}`, http.StatusBadRequest},
		{"bad json", "application/json", `{"pane":`, http.StatusBadRequest},
		{"form post", "application/x-www-form-urlencoded", `pane=wA:p1&text=hi`, http.StatusUnsupportedMediaType},
	} {
		if res := postPrompt(t, srv.URL, c.ctype, c.body); res.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d", c.name, res.StatusCode, c.status)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil {
		t.Fatal("herdr ran for a rejected prompt")
	}
}

func TestPromptReportsHerdrFailure(t *testing.T) {
	h, _ := fakeClaude(t, "error")
	a := NewArmy()
	a.Set([]byte(promptArmy))
	srv := httptest.NewServer(Handler(a, h, nil))
	defer srv.Close()

	res := postPrompt(t, srv.URL, "application/json", `{"pane":"wA:p1","text":"hi"}`)
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "agent target x not found") {
		t.Fatalf("status %d body %q", res.StatusCode, body)
	}
}
