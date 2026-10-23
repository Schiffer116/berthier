package core

import (
	"fmt"
	"testing"
)

func TestLandServices(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  string
	}{
		{nil, "[]"},
		{[]string{"CLAUDE.md", "docs/adr/0001.md", "tests/e2e.sh", "internal/core/tick_test.go"}, "[]"},
		{[]string{"internal/console/index.html", "internal/console/chat.go"}, "[dashboard]"},
		{[]string{"systemd/berthier-dashboard.service"}, "[dashboard]"},
		{[]string{"systemd/berthier-loop.service"}, "[loop]"},
		{[]string{"internal/core/tick.go"}, "[loop dashboard]"},
		{[]string{"go.sum"}, "[loop dashboard]"},
		{[]string{"internal/console/x.go", "systemd/berthier-loop.service"}, "[loop dashboard]"},
	} {
		if got := fmt.Sprint(LandServices(c.files)); got != c.want {
			t.Errorf("LandServices(%v) = %s, want %s", c.files, got, c.want)
		}
	}
}
