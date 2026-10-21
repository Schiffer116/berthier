package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func get(t *testing.T, url string) (*http.Response, []byte) {
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("GET %s = %d", url, res.StatusCode)
	}
	return res, body
}

// The page links a manifest whose icons are all served at their stated size,
// and every file the service worker keeps exists.
func TestInstallable(t *testing.T) {
	srv := httptest.NewServer(Handler(NewArmy(), "herdr", nil))
	defer srv.Close()

	_, page := get(t, srv.URL+"/")
	for _, want := range []string{`rel="manifest" href="manifest.webmanifest"`, `rel="apple-touch-icon" href="icon-180.png"`,
		`viewport-fit=cover`, `register('sw.js')`} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("page lacks %s", want)
		}
	}

	res, body := get(t, srv.URL+"/manifest.webmanifest")
	if ct := res.Header.Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("manifest Content-Type %q", ct)
	}
	var m struct {
		Name, Display string
		StartURL      string `json:"start_url"`
		ThemeColor    string `json:"theme_color"`
		Icons         []struct{ Src, Sizes, Type, Purpose string }
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "Berthier" || m.Display != "standalone" || m.StartURL == "" {
		t.Errorf("manifest name %q display %q start_url %q", m.Name, m.Display, m.StartURL)
	}
	if m.ThemeColor == "" || !bytes.Contains(page, []byte(`name="theme-color" content="`+m.ThemeColor+`"`)) {
		t.Errorf("manifest theme_color %q differs from the page's", m.ThemeColor)
	}
	srcs := map[string]bool{"icon-180.png": true}
	for _, ic := range m.Icons {
		srcs[ic.Src] = true
		var w, h int
		if _, err := fmt.Sscanf(ic.Sizes, "%dx%d", &w, &h); err != nil {
			t.Fatalf("icon sizes %q", ic.Sizes)
		}
		_, img := get(t, srv.URL+"/"+ic.Src)
		cfg, err := png.DecodeConfig(bytes.NewReader(img))
		if err != nil || cfg.Width != w || cfg.Height != h {
			t.Errorf("%s: %dx%d (%v), manifest says %s", ic.Src, cfg.Width, cfg.Height, err, ic.Sizes)
		}
	}
	if !srcs["icon-192.png"] || !srcs["icon-512.png"] {
		t.Errorf("manifest icons %v lack 192 or 512", srcs)
	}

	res, sw := get(t, srv.URL+"/sw.js")
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("sw.js Content-Type %q", ct)
	}
	shell := regexp.MustCompile(`SHELL = \[([^\]]*)\]`).FindSubmatch(sw)
	if shell == nil {
		t.Fatal("sw.js has no SHELL list")
	}
	for _, p := range regexp.MustCompile(`'([^']*)'`).FindAllSubmatch(shell[1], -1) {
		if path := strings.TrimPrefix(string(p[1]), "./"); strings.HasPrefix(path, "api/") {
			t.Errorf("sw.js caches %s", p[1])
		} else {
			get(t, srv.URL+"/"+path)
		}
	}
}

// The icon is the accent "B" on the page background, centered in the safe zone.
func TestIconDrawsB(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Icon(192)))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 192 {
		t.Fatalf("size %v", img.Bounds())
	}
	at := func(x, y float64) uint32 { r, _, _, _ := img.At(int(x*192), int(y*192)).RGBA(); return r >> 8 }
	if c, stem := at(0.02, 0.02), at(0.37, 0.4); c != uint32(iconBg.R) || stem != uint32(iconAccent.R) {
		t.Errorf("corner %d, stem %d", c, stem)
	}
	// Maskable safe zone: nothing drawn outside the central 80% circle.
	for y := 0.0; y < 1; y += 0.01 {
		for x := 0.0; x < 1; x += 0.01 {
			if inB(x, y) && (x-0.5)*(x-0.5)+(y-0.5)*(y-0.5) > 0.4*0.4 {
				t.Fatalf("B reaches %.2f,%.2f, outside the safe zone", x, y)
			}
		}
	}
}
