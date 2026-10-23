package app

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"berthier/internal/core"
)

// cmdLand lands a Division of Berthier itself: merge its branch into the
// branch checked out in Berthier's repo (master), run the checks, rebuild
// bin/berthier, restart only the services whose code changed and wait until
// they answer. A conflict or a failed check leaves master as it was. It never
// closes the Division.
func cmdLand(e *Env, args []string, io IO) error {
	if len(args) != 1 {
		return die("usage: berthier land <division>")
	}
	branch := core.Slug(args[0])
	root := e.Root
	git := func(a ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", root}, a...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	say := func(format string, a ...any) { fmt.Fprintf(io.Out, format+"\n", a...) }
	fail := func(format string, a ...any) error {
		say(format, a...)
		say("FAIL")
		return exitCode(1)
	}

	if _, err := git("rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return die("no branch '%s' in %s", branch, root)
	}
	master, err := git("symbolic-ref", "--short", "HEAD")
	if err != nil || master == branch {
		return die("%s must have its main branch checked out", root)
	}
	if dirty, _ := git("status", "--porcelain", "--untracked-files=no"); dirty != "" {
		return die("%s has uncommitted changes; land needs it clean:\n%s", root, dirty)
	}
	if wt := worktreeOf(root, branch); wt != "" {
		if out, _ := exec.Command("git", "-C", wt, "status", "--porcelain").Output(); len(out) > 0 {
			return die("Division '%s' has uncommitted changes in %s; commit them first", branch, wt)
		}
	}
	// The pre-commit gate is versioned in the repo; make sure git runs it.
	if hp, _ := git("config", "core.hooksPath"); hp == "" {
		git("config", "core.hooksPath", ".githooks")
	}
	if _, err := git("merge-base", "--is-ancestor", branch, "HEAD"); err == nil {
		say("land %s: already in %s, nothing to land", branch, master)
		return nil
	}

	old, _ := git("rev-parse", "HEAD")
	n, _ := git("rev-list", "--count", "HEAD.."+branch)
	merge := exec.Command("git", "-C", root, "merge", "--no-edit", branch)
	merge.Env = append(os.Environ(), "GIT_EDITOR=true")
	if out, err := merge.CombinedOutput(); err != nil {
		conflicts, _ := git("diff", "--name-only", "--diff-filter=U")
		git("merge", "--abort")
		if conflicts == "" {
			conflicts = strings.TrimSpace(string(out))
		}
		return fail("land %s: merge refused, %s unchanged; rebase the Division on %s. Conflicts:\n%s", branch, master, master, conflicts)
	}
	head, _ := git("rev-parse", "--short", "HEAD")
	say("land %s: merged %s commit(s) into %s (%s)", branch, n, master, head)

	rollback := func(what, out string) error {
		git("reset", "--hard", old)
		return fail("%s failed; %s rolled back to %.7s:\n%s", what, master, old, tail(out, 40))
	}
	for _, c := range []struct {
		what string
		args []string
	}{{"go vet", []string{"go", "vet", "./..."}}, {"gofmt", []string{"gofmt", "-l", "."}}, {"go test", []string{"go", "test", "./..."}}} {
		cmd := exec.Command(c.args[0], c.args[1:]...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil || (c.what == "gofmt" && len(out) > 0) {
			return rollback(c.what, string(out))
		}
	}
	say("checks: vet, gofmt, test ok")

	// Build aside and rename: Agents exec bin/berthier meanwhile.
	tmp := filepath.Join(root, "bin", ".berthier.land")
	build := exec.Command("go", "build", "-o", tmp, "./cmd/berthier")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return rollback("go build", string(out))
	}
	if err := os.Rename(tmp, filepath.Join(root, "bin", "berthier")); err != nil {
		return fail("install bin/berthier: %v", err)
	}
	say("built bin/berthier")

	files, _ := git("diff", "--name-only", old, "HEAD")
	changed := strings.Fields(files)
	services := core.LandServices(changed)
	if len(services) == 0 {
		say("no service code changed; nothing restarted")
		say("PASS")
		return nil
	}
	for _, f := range changed {
		if strings.HasPrefix(f, "systemd/") {
			exec.Command(e.Systemctl, "--user", "daemon-reload").Run()
			break
		}
	}
	ok := true
	for _, s := range services {
		unit := "berthier-" + s
		if out, err := exec.Command(e.Systemctl, "--user", "restart", unit).CombinedOutput(); err != nil {
			say("%s: restart failed: %s", unit, strings.TrimSpace(string(out)))
			ok = false
			continue
		}
		// restart returns once the old process is gone and the new one started.
		if why := e.waitUp(s, time.Now()); why != "" {
			say("%s: restarted but %s; see systemctl --user status %s", unit, why, unit)
			ok = false
			continue
		}
		say("%s: restarted, up", unit)
	}
	if !ok {
		say("FAIL")
		return exitCode(1)
	}
	say("PASS")
	return nil
}

// waitUp waits until a restarted service works: the Console answers its page,
// the loop writes state.json. Empty when it does, else what it did not do.
func (e *Env) waitUp(service string, since time.Time) string {
	deadline := time.Now().Add(e.LandWait)
	client := http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		switch service {
		case "dashboard":
			if resp, err := client.Get(e.ConsoleURL); err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					return ""
				}
			}
		case "loop":
			if st, err := os.Stat(e.State); err == nil && st.ModTime().After(since) {
				return ""
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if service == "dashboard" {
		return e.ConsoleURL + " did not answer 200 within " + e.LandWait.String()
	}
	return "state.json not written within " + e.LandWait.String()
}

// worktreeOf is the checkout of branch in root's repository, if any.
func worktreeOf(root, branch string) string {
	out, _ := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	path := ""
	for _, l := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			path = p
		} else if l == "branch refs/heads/"+branch {
			return path
		}
	}
	return ""
}

// tail is the last n lines of s.
func tail(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}
