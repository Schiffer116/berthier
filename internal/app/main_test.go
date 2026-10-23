package app

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain drops git's repository variables first: a git hook (the pre-commit
// gate) exports GIT_DIR, GIT_INDEX_FILE etc., and the tests' git commands on
// scratch repos would otherwise act on the real one.
func TestMain(m *testing.M) {
	vars := []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY"}
	if out, err := exec.Command("git", "rev-parse", "--local-env-vars").Output(); err == nil {
		vars = append(vars, strings.Fields(string(out))...)
	}
	for _, v := range vars {
		os.Unsetenv(v)
	}
	os.Exit(m.Run())
}
