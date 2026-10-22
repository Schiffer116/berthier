package main

// The Console as an installable app: a web app manifest, an app-shell service
// worker, and icons rasterised here from one SVG so the repo holds no image files.

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"net/http"
	"strconv"
	"sync"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

//go:embed sw.js
var swJS []byte

//go:embed icon.svg
var iconSVG []byte

//go:embed manifest.webmanifest
var manifest []byte

// IconSizes are the square icons served at /icon-<size>.png: Android's two,
// and iOS's home-screen icon.
var IconSizes = []int{180, 192, 512}

// Icon rasterises the themed icon.svg: the imperial eagle full-bleed on the
// palette's crust, shrunk so the wings stay inside the maskable safe zone.
func Icon(size int) []byte {
	svg, err := oksvg.ReadIconStream(bytes.NewReader(themed(iconSVG)))
	if err != nil {
		return nil
	}
	svg.SetTarget(0, 0, float64(size), float64(size))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	svg.Draw(rasterx.NewDasher(size, size, rasterx.NewScannerGV(size, size, img, img.Bounds())), 1)
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func appRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Write(themed(manifest))
	})
	// Always revalidated, so a new worker is picked up on the next load.
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(swJS)
	})
	// The same artwork as the PNGs, for browser tabs.
	mux.HandleFunc("GET /icon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(themed(iconSVG))
	})
	// The palette, never cached stale by the browser or the worker.
	mux.HandleFunc("GET /theme.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(themeCSS())
	})
	for _, size := range IconSizes {
		icon := sync.OnceValue(func() []byte { return Icon(size) })
		mux.HandleFunc("GET /icon-"+strconv.Itoa(size)+".png", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "max-age=86400")
			w.Write(icon())
		})
	}
}
