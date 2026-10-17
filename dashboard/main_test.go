package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBerthier writes a script whose `army` output is a file the test changes.
func fakeBerthier(t *testing.T, json string) (script, data string) {
	dir := t.TempDir()
	data = filepath.Join(dir, "army.json")
	os.WriteFile(data, []byte(json), 0o644)
	script = filepath.Join(dir, "berthier")
	os.WriteFile(script, []byte("#!/bin/sh\ncat "+data+"\n"), 0o755)
	return script, data
}

func TestServesLatestAndPushesChanges(t *testing.T) {
	script, data := fakeBerthier(t, `{"v":1}`)
	a := NewArmy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Poll(ctx, a, script, 20*time.Millisecond)
	srv := httptest.NewServer(Handler(a))
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
	srv := httptest.NewServer(Handler(NewArmy()))
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
