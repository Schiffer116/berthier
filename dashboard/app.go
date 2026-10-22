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

// eagle is the left half of the icon's eagle, the Grand Army's emblem: head,
// wing with a feathered trailing edge, body and tail, as points of the unit
// square from the crown down the center line. inEagle mirrors it, and it stays
// inside the maskable safe zone.
var eagle = [][2]float64{
	{0.50, 0.19}, {0.45, 0.22}, {0.44, 0.29}, {0.41, 0.35}, {0.32, 0.30}, {0.28, 0.25}, {0.20, 0.25},
	{0.22, 0.32}, {0.16, 0.38}, {0.22, 0.40}, {0.17, 0.47}, {0.25, 0.47}, {0.25, 0.55},
	{0.34, 0.55}, {0.40, 0.62}, {0.37, 0.77}, {0.50, 0.81},
}

// beak is the one asymmetry: the head in profile, hooked to the right.
var beak = [][2]float64{{0.54, 0.22}, {0.62, 0.28}, {0.60, 0.33}, {0.57, 0.30}, {0.54, 0.31}}

func inPoly(poly [][2]float64, x, y float64) bool {
	in := false
	for i, p := range poly {
		q := poly[(i+1)%len(poly)]
		if (p[1] > y) != (q[1] > y) && x < p[0]+(y-p[1])*(q[0]-p[0])/(q[1]-p[1]) {
			in = !in
		}
	}
	return in
}

// inEagle says whether a point of the unit square lies on the eagle.
func inEagle(x, y float64) bool {
	x0 := x
	if x > 0.5 {
		x = 1 - x
	}
	return inPoly(beak, x0, y) || inPoly(eagle, x, y)
}

// Icon draws the icon full-bleed (iOS and Android round it themselves),
// antialiased by 4x4 supersampling.
func Icon(size int) []byte {
	iconBg, iconAccent := themeRGBA("bg"), themeRGBA("accent")
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const n = 4
	mix := func(a, b uint8, t float64) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
	for py := range size {
		for px := range size {
			hits := 0
			for sy := range n {
				for sx := range n {
					if inEagle((float64(px)+(float64(sx)+0.5)/n)/float64(size), (float64(py)+(float64(sy)+0.5)/n)/float64(size)) {
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
		w.Write(themed(manifest))
	})
	// Always revalidated, so a new worker is picked up on the next load.
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(swJS)
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
