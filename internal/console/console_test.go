package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"berthier/internal/herdr"
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
	srv := httptest.NewServer(Handler(a, herdr.Client{}, nil, nil))
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
	srv := httptest.NewServer(Handler(NewArmy(), herdr.Client{}, nil, nil))
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
	srv := httptest.NewServer(Handler(a, h, nil, nil))
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
	srv := httptest.NewServer(Handler(a, h, nil, nil))
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
	srv := httptest.NewServer(Handler(a, h, nil, nil))
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
	srv := httptest.NewServer(Handler(a, h, nil, nil))
	defer srv.Close()

	res := postPrompt(t, srv.URL, "application/json", `{"pane":"wA:p1","text":"hi"}`)
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "agent target x not found") {
		t.Fatalf("status %d body %q", res.StatusCode, body)
	}
}

func TestBudgetEndpoint(t *testing.T) {
	type call struct {
		corps string
		pct   *float64
	}
	var calls []call
	set := func(corps string, pct *float64) error {
		calls = append(calls, call{corps, pct})
		if corps == "full" {
			return fmt.Errorf("disk full")
		}
		return nil
	}
	a := NewArmy()
	a.Set([]byte(`{"budget":{"corps":[{"name":"skipli"},{"name":"full"}]}}`))
	srv := httptest.NewServer(Handler(a, herdr.Client{}, set, nil))
	defer srv.Close()
	post := func(ctype, body string) int {
		res, err := http.Post(srv.URL+"/api/budget", ctype, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode
	}
	for _, c := range []struct {
		name, ctype, body string
		status            int
	}{
		{"set", "application/json", `{"corps":"skipli","pct":25}`, http.StatusNoContent},
		{"drop", "application/json", `{"corps":"skipli","pct":null}`, http.StatusNoContent},
		{"top of range", "application/json", `{"corps":"skipli","pct":100}`, http.StatusNoContent},
		{"unknown corps", "application/json", `{"corps":"nope","pct":5}`, http.StatusNotFound},
		{"zero", "application/json", `{"corps":"skipli","pct":0}`, http.StatusBadRequest},
		{"over 100", "application/json", `{"corps":"skipli","pct":101}`, http.StatusBadRequest},
		{"negative", "application/json", `{"corps":"skipli","pct":-1}`, http.StatusBadRequest},
		{"string pct", "application/json", `{"corps":"skipli","pct":"5"}`, http.StatusBadRequest},
		{"bad json", "application/json", `{"corps":`, http.StatusBadRequest},
		{"form post", "application/x-www-form-urlencoded", `corps=skipli&pct=5`, http.StatusUnsupportedMediaType},
		{"write failure", "application/json", `{"corps":"full","pct":5}`, http.StatusInternalServerError},
	} {
		if got := post(c.ctype, c.body); got != c.status {
			t.Errorf("%s: status %d, want %d", c.name, got, c.status)
		}
	}
	// Only the three accepted requests and the failing write reached the setter.
	if len(calls) != 4 || *calls[0].pct != 25 || calls[1].pct != nil || *calls[2].pct != 100 {
		t.Errorf("setter calls = %+v", calls)
	}
}

func TestProfileRoute(t *testing.T) {
	var asked []string
	src := func(week int, session string) ([]byte, error) {
		asked = append(asked, fmt.Sprintf("%d %q", week, session))
		return []byte(`{"week":{}}`), nil
	}
	srv := httptest.NewServer(HandlerWith(NewArmy(), herdr.Client{}, nil, nil, src, nil, nil))
	defer srv.Close()
	for _, tc := range []struct {
		q    string
		code int
	}{{"", 200}, {"?week=1&session=abc", 200}, {"?week=x", 400}, {"?week=-1", 400}} {
		resp, err := http.Get(srv.URL + "/api/profile" + tc.q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.code {
			t.Errorf("%s: %d, want %d", tc.q, resp.StatusCode, tc.code)
		}
	}
	if got := strings.Join(asked, ","); got != `0 "",1 "abc"` {
		t.Errorf("asked %s", got)
	}
	none := httptest.NewServer(Handler(NewArmy(), herdr.Client{}, nil, nil))
	defer none.Close()
	if resp, _ := http.Get(none.URL + "/api/profile"); resp.StatusCode != 404 {
		t.Errorf("without a Profile: %d", resp.StatusCode)
	}
}

type refusal string

func (r refusal) Error() string { return string(r) }
func (refusal) Bad() bool       { return true }

func TestThaw(t *testing.T) {
	var got []string
	thaw := func(pane, how string) error {
		if how != "revive" && how != "fresh" {
			return refusal("want revive or fresh")
		}
		got = append(got, pane+" "+how)
		return nil
	}
	a := NewArmy()
	a.Set([]byte(`{"corps":[{"agents":[{"pane_id":"w1:p1"}],"divisions":[]}]}`))
	srv := httptest.NewServer(HandlerWith(a, herdr.Client{}, nil, nil, nil, thaw, nil))
	defer srv.Close()
	for _, c := range []struct {
		name, ctype, body string
		status            int
	}{
		{"fresh", "application/json", `{"pane":"w1:p1","how":"fresh"}`, http.StatusNoContent},
		{"revive", "application/json", `{"pane":"w1:p1","how":"revive"}`, http.StatusNoContent},
		{"unknown agent", "application/json", `{"pane":"w9:p1","how":"fresh"}`, http.StatusNotFound},
		{"refused", "application/json", `{"pane":"w1:p1","how":"melt"}`, http.StatusBadRequest},
		{"form post", "application/x-www-form-urlencoded", `pane=w1:p1`, http.StatusUnsupportedMediaType},
	} {
		res, err := http.Post(srv.URL+"/api/thaw", c.ctype, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d", c.name, res.StatusCode, c.status)
		}
	}
	if strings.Join(got, ",") != "w1:p1 fresh,w1:p1 revive" {
		t.Errorf("thawed: %v", got)
	}
}

func TestBroadcastEndpoints(t *testing.T) {
	var got []string
	bc := func(kind, extra string) (int, int, error) {
		got = append(got, kind+":"+extra)
		if extra == "boom" {
			return 0, 0, fmt.Errorf("no state")
		}
		return 3, 1, nil
	}
	srv := httptest.NewServer(HandlerWith(NewArmy(), herdr.Client{}, nil, nil, nil, nil, bc))
	defer srv.Close()
	for _, c := range []struct {
		path, ctype, body string
		status            int
		resp              string
	}{
		{"shutdown", "application/json", `{"text":"back at 9"}`, http.StatusOK, `{"sent":3,"skipped":1}`},
		{"wake", "application/json", `{}`, http.StatusOK, `{"sent":3,"skipped":1}`},
		{"wake", "application/json", ``, http.StatusOK, `{"sent":3,"skipped":1}`},
		{"shutdown", "application/json", `{"text":"boom"}`, http.StatusInternalServerError, ""},
		{"wake", "text/plain", `{}`, http.StatusUnsupportedMediaType, ""},
		{"wake", "application/json", `{"text":`, http.StatusBadRequest, ""},
	} {
		res, err := http.Post(srv.URL+"/api/"+c.path, c.ctype, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != c.status || (c.resp != "" && strings.TrimSpace(string(b)) != c.resp) {
			t.Errorf("%s %q: %d %q, want %d %q", c.path, c.body, res.StatusCode, b, c.status, c.resp)
		}
	}
	if want := "shutdown:back at 9 wake: wake: shutdown:boom"; strings.Join(got, " ") != want {
		t.Errorf("calls %q, want %q", got, want)
	}
}
