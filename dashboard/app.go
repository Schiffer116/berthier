package main

// The Console as an installable app: a web app manifest, an app-shell service
// worker, and icons drawn here so the repo holds no image files.

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"strconv"
	"sync"
)

//go:embed sw.js
var swJS []byte

//go:embed manifest.webmanifest
var manifest []byte

// IconSizes are the square icons served at /icon-<size>.png: Android's two,
// and iOS's home-screen icon.
var IconSizes = []int{180, 192, 512}

var (
	iconBg     = color.RGBA{0x0A, 0x0D, 0x11, 0xFF} // --bg
	iconAccent = color.RGBA{0xF2, 0xA9, 0x3B, 0xFF} // --accent
)

// inB says whether a point of the unit square lies on the icon's "B": a stem,
// three bars and two half-ring bowls, kept inside the maskable safe zone.
func inB(x, y float64) bool {
	const (
		left, top, bottom, w = 0.34, 0.26, 0.74, 0.075
		r                    = (0.5 + w/2 - top) / 2 // both bowls' outer radius
		upX, lowX            = 0.50, 0.53            // bowl centers
	)
	cy1, cy2 := top+r, bottom-r
	band := func(y0, x1 float64) bool { return y >= y0 && y <= y0+w && x >= left && x <= x1 }
	bowl := func(cx, cy float64) bool {
		d := math.Hypot(x-cx, y-cy)
		return x >= cx && d <= r && d >= r-w
	}
	return (x >= left && x <= left+w && y >= top && y <= bottom) ||
		band(top, upX) || band(0.5-w/2, lowX) || band(bottom-w, lowX) ||
		bowl(upX, cy1) || bowl(lowX, cy2)
}

// Icon draws the icon full-bleed (iOS and Android round it themselves),
// antialiased by 4x4 supersampling.
func Icon(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const n = 4
	mix := func(a, b uint8, t float64) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
	for py := range size {
		for px := range size {
			hits := 0
			for sy := range n {
				for sx := range n {
					if inB((float64(px)+(float64(sx)+0.5)/n)/float64(size), (float64(py)+(float64(sy)+0.5)/n)/float64(size)) {
						hits++
					}
				}
			}
			t := float64(hits) / (n * n)
			img.SetRGBA(px, py, color.RGBA{mix(iconBg.R, iconAccent.R, t), mix(iconBg.G, iconAccent.G, t), mix(iconBg.B, iconAccent.B, t), 0xFF})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func appRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Write(manifest)
	})
	// Always revalidated, so a new worker is picked up on the next load.
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(swJS)
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
