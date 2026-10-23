package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// landRepo is a scratch Berthier repo: a tiny Go module on master with a
// Console package and a test, a fake systemctl that logs its calls (a loop
// restart writes state.json soon after, as the loop's first tick does) and a Console that answers.
func landRepo(t *testing.T) (s *sandbox, root string, run func(args ...string) string) {
	t.Helper()
	s = newSandbox(t)
	root = t.TempDir()
	run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	files := map[string]string{
		".gitignore":                 "/bin/\n",
		"go.mod":                     "module landtest\n\ngo 1.22\n",
		"cmd/berthier/main.go":       "package main\n\nfunc main() {}\n",
		"internal/console/c.go":      "package console\n\nfunc Answer() int { return 42 }\n",
		"internal/console/c_test.go": "package console\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(Answer())\n\t}\n}\n",
	}
	for f, body := range files {
		os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(root, f), []byte(body), 0o644)
	}
	run("git", "init", "-q", "-b", "master")
	run("git", "-c", "user.name=t", "-c", "user.email=t@t", "add", ".")
	run("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")

	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte(`#!/bin/sh
echo "$*" >>"`+bin+`/calls"
[ "$3" = berthier-loop ] && { (sleep 0.2; touch "`+s.home+`/state.json") >/dev/null 2>&1 & }
exit 0
`), 0o755)
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(console.Close)
	s.env["BERTHIER_ROOT"], s.env["BERTHIER_SYSTEMCTL"] = root, filepath.Join(bin, "systemctl")
	s.env["BERTHIER_CONSOLE_URL"], s.env["BERTHIER_LAND_WAIT"] = console.URL, "5"
	s.env["calls"] = filepath.Join(bin, "calls")
	return s, root, run
}

// division commits a change to one file on a new worktree branch.
func division(t *testing.T, root, name, file, body string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), name)
	for _, args := range [][]string{
		{"git", "-C", root, "worktree", "add", "-q", "-b", name, wt},
		{"sh", "-c", "mkdir -p \"$(dirname \"$0\")\" && printf '%s' \"$1\" >\"$0\"", filepath.Join(wt, file), body},
		{"git", "-C", wt, "add", "."},
		{"git", "-C", wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", name},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return wt
}

func TestLandMergesChecksAndRestartsOnlyTheChangedService(t *testing.T) {
	s, root, run := landRepo(t)
	division(t, root, "console-fix", "internal/console/c.go", "package console\n\n// Answer is the answer.\nfunc Answer() int { return 42 }\n")
	out, errs, code := s.sh("", "land", "console-fix")
	if code != 0 || errs != "" || !strings.HasSuffix(out, "PASS\n") {
		t.Fatalf("land: %d %q\n%s", code, errs, out)
	}
	for _, want := range []string{"merged 1 commit(s) into master", "checks: vet, gofmt, test ok", "built bin/berthier", "berthier-dashboard: restarted, up"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	calls, _ := os.ReadFile(s.env["calls"])
	eq(t, "only the Console restarted", strings.TrimSpace(string(calls)), "--user restart berthier-dashboard")
	eq(t, "binary built", fileExists(filepath.Join(root, "bin", "berthier")), true)
	eq(t, "hooks installed", run("git", "config", "core.hooksPath"), ".githooks")
	eq(t, "master has it", run("git", "log", "-1", "--format=%s"), "console-fix")
	eq(t, "Division kept", run("git", "branch", "--list", "console-fix") != "", true)

	out, _, code = s.sh("", "land", "console-fix")
	eq(t, "landing twice is a no-op", code == 0 && strings.Contains(out, "nothing to land"), true)

	// Core code restarts both; the loop counts as up once it writes state.json.
	os.Remove(s.env["calls"])
	division(t, root, "core-change", "internal/core/k.go", "package core\n\nconst K = 1\n")
	out, _, code = s.sh("", "land", "core-change")
	calls, _ = os.ReadFile(s.env["calls"])
	if code != 0 || !strings.Contains(out, "berthier-loop: restarted, up") {
		t.Errorf("both restarted: %d\n%s", code, out)
	}
	eq(t, "systemctl calls", strings.TrimSpace(string(calls)), "--user restart berthier-loop\n--user restart berthier-dashboard")
}

func TestLandRefusesAndLeavesMasterAsItWas(t *testing.T) {
	s, root, run := landRepo(t)
	head := run("git", "rev-parse", "HEAD")
	clean := func(name string) {
		t.Helper()
		eq(t, name+": master unchanged", run("git", "rev-parse", "HEAD"), head)
		eq(t, name+": master clean", run("git", "status", "--porcelain"), "")
		eq(t, name+": no restart", fileExists(s.env["calls"]), false)
	}

	division(t, root, "broken", "internal/console/c.go", "package console\n\nfunc Answer() int { return 41 }\n")
	out, _, code := s.sh("", "land", "broken")
	eq(t, "failing test: FAIL", code == 1 && strings.Contains(out, "go test failed; master rolled back") && strings.HasSuffix(out, "FAIL\n"), true)
	clean("failing test")

	division(t, root, "ugly", "internal/console/u.go", "package console\nfunc  U() {}\n")
	out, _, code = s.sh("", "land", "ugly")
	eq(t, "gofmt: FAIL", code == 1 && strings.Contains(out, "gofmt failed") && strings.Contains(out, "u.go"), true)
	clean("gofmt")

	// A Division and master both change the same line.
	division(t, root, "clash", "internal/console/c.go", "package console\n\nfunc Answer() int { return 6 * 7 }\n")
	os.WriteFile(filepath.Join(root, "internal/console/c.go"), []byte("package console\n\nfunc Answer() int { return 40 + 2 }\n"), 0o644)
	run("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "master moved")
	head = run("git", "rev-parse", "HEAD")
	out, _, code = s.sh("", "land", "clash")
	eq(t, "conflict: refused", code == 1 && strings.Contains(out, "merge refused") && strings.Contains(out, "internal/console/c.go"), true)
	clean("conflict")

	wt := division(t, root, "dirty", "internal/console/d.go", "package console\n")
	os.WriteFile(filepath.Join(wt, "internal/console/d.go"), []byte("package console\n\nvar D = 1\n"), 0o644)
	_, errs, code := s.sh("", "land", "dirty")
	eq(t, "dirty Division: refused", code == 1 && strings.Contains(errs, "uncommitted changes in "+wt), true)

	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module landtest\n\ngo 1.23\n"), 0o644)
	_, errs, code = s.sh("", "land", "clash")
	eq(t, "dirty master: refused", code == 1 && strings.Contains(errs, "uncommitted changes"), true)
	run("git", "checkout", "go.mod")

	_, errs, code = s.sh("", "land", "nope")
	eq(t, "unknown Division", code == 1 && strings.Contains(errs, "no branch 'nope'"), true)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
