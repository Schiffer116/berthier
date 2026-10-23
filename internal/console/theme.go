package console

// The Console's colours come from the dotfiles Palette (~/.config/theme/palette.css),
// read on every request so a palette change shows on the next load. An embedded
// copy stands in when the file is missing.

import (
	_ "embed"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

//go:embed theme-fallback.css
var themeFallback []byte

var cssVar = regexp.MustCompile(`--([a-z0-9]+):\s*#([0-9a-fA-F]{6})`)

// themeCSS is the palette stylesheet: the dotfiles file, else the fallback.
func themeCSS() []byte {
	path := os.Getenv("BERTHIER_THEME")
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "theme", "palette.css")
	}
	if b, err := os.ReadFile(path); err == nil && len(cssVar.Find(b)) > 0 {
		return b
	}
	return themeFallback
}

// themeColor looks a palette name up as "#rrggbb", falling back per name.
func themeColor(name string) string {
	for _, css := range [][]byte{themeCSS(), themeFallback} {
		for _, m := range cssVar.FindAllSubmatch(css, -1) {
			if string(m[1]) == name {
				return "#" + string(m[2])
			}
		}
	}
	return "#000000"
}

func themeRGBA(name string) color.RGBA {
	n, _ := strconv.ParseUint(themeColor(name)[1:], 16, 32)
	return color.RGBA{uint8(n >> 16), uint8(n >> 8), uint8(n), 0xFF}
}

// themed fills {{name}} placeholders of a served file with palette colours.
func themed(b []byte) []byte {
	return regexp.MustCompile(`\{\{([a-z0-9]+)\}\}`).ReplaceAllFunc(b, func(m []byte) []byte {
		return []byte(themeColor(string(m[2 : len(m)-2])))
	})
}
