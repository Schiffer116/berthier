package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// usageCommands are the commands the usage text lists.
func usageCommands() []string {
	var l []string
	for _, line := range strings.Split(usageText, "\n")[2:] {
		if line != "" && line[0] != ' ' {
			l = append(l, strings.Fields(line)[0])
		}
	}
	return l
}

func registered(cmd string) bool {
	_, ok := commands[cmd]
	return ok || cmd == "sample" // sample is dispatched before the map: it must stay cheap
}

func TestUsageCommandsAreRegistered(t *testing.T) {
	list := usageCommands()
	if len(list) < 15 {
		t.Fatalf("parsed only %v from the usage text", list)
	}
	for _, c := range list {
		if !registered(c) {
			t.Errorf("usage lists %q but no command is registered", c)
		}
	}
	// And the other way: nothing registered is missing from the usage text.
	for c := range commands {
		if c != "brief-after-dialog" && !strsHas(list, c) {
			t.Errorf("command %q is not in the usage text", c)
		}
	}
}

func TestSystemdCommandsExist(t *testing.T) {
	units, _ := filepath.Glob("../../systemd/berthier-*.service")
	if len(units) == 0 {
		t.Fatal("no units found")
	}
	exec := regexp.MustCompile(`(?m)^ExecStart=\S*/bin/berthier (\S+)`)
	for _, u := range units {
		b, _ := os.ReadFile(u)
		m := exec.FindStringSubmatch(string(b))
		if m == nil {
			t.Errorf("%s: no ExecStart running bin/berthier", u)
		} else if !registered(m[1]) {
			t.Errorf("%s: ExecStart runs unknown command %q", u, m[1])
		}
	}
}

func TestPromptCommand(t *testing.T) {
	s := newSandbox(t)
	dir := fakeHerdr(t, s)
	s.mode(dir, "takes")
	_, errs, code := s.sh("", "prompt", "p1", "go on")
	if code != 0 || errs != "" || calls(dir) != "agent prompt" {
		t.Errorf("code %d %q calls %q", code, errs, calls(dir))
	}
	text, _ := os.ReadFile(filepath.Join(dir, "text"))
	if string(text) != "go on" {
		t.Errorf("prompted %q", text)
	}
	s.mode(dir, "error")
	_, errs, code = s.sh("", "prompt", "p1", "hi")
	if code != 1 || !strings.Contains(errs, "agent target x not found") {
		t.Errorf("error case: %d %q", code, errs)
	}
	_, errs, code = s.sh("", "prompt", "p1")
	if code != 1 || !strings.Contains(errs, "usage: berthier prompt") {
		t.Errorf("usage case: %d %q", code, errs)
	}
}
