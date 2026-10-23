package core

import (
	"path"
	"strings"
)

// LandServices is the systemd services (by short name: loop, dashboard) whose
// code a landed change touched, so `berthier land` restarts only those. Tests
// and docs restart nothing; the Console's package and unit restart only the
// Console; any other Go code may run in both.
func LandServices(files []string) []string {
	loop, dash := false, false
	for _, f := range files {
		switch {
		case strings.HasSuffix(f, "_test.go"):
		case f == "systemd/berthier-loop.service":
			loop = true
		case f == "systemd/berthier-dashboard.service", strings.HasPrefix(f, "internal/console/"):
			dash = true
		case path.Ext(f) == ".go", f == "go.mod", f == "go.sum":
			loop, dash = true, true
		}
	}
	var l []string
	if loop {
		l = append(l, "loop")
	}
	if dash {
		l = append(l, "dashboard")
	}
	return l
}
