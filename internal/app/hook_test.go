package app

import (
	"strings"
	"testing"
)

// The line a turn gets when its Agent did not report it.
func TestAutoReport(t *testing.T) {
	for _, c := range []struct{ final, want string }{
		{"", "finished its turn"},
		{"  Done.\n\nDetails follow here.", "Done."},
		{"Merged the branch", "Merged the branch"},
		{"Should I push to main?", "needs you: Should I push to main?"},
		{"Ran the tests. Shall I merge?", "needs you: Ran the tests."},
		{"Run `make` and push", "Run `make` and push"},
		{"Pushed it. You must approve the PR.", "needs you: Pushed it."},
		{"Done, you need to restart the loop.", "needs you: Done, you need to restart the loop."},
		{"**Ready?**", "needs you: **Ready?**"},
	} {
		if got := autoReport(c.final); got != c.want {
			t.Errorf("autoReport(%q) = %q, want %q", c.final, got, c.want)
		}
	}
	long := autoReport(strings.Repeat("word ", 40))
	if n := len([]rune(long)); n != 120 || !strings.HasSuffix(long, "…") {
		t.Errorf("long line: %d runes %q", n, long)
	}
}
