package core

import "strings"

// Army-wide broadcasts: one inbox message per Agent, delivered like a notify.
const (
	TagShutdown = "[berthier shutdown]"
	TagWake     = "[berthier wake]"
)

var broadcasts = map[string]struct{ tag, text string }{
	"shutdown": {TagShutdown, "The user is closing down soon: finish or park your current step, commit and push WIP, leave a `berthier report`, update `berthier notes`, and start nothing long."},
	"wake":     {TagWake, "The army is back after a restart: pick up where you left off. Check your Division Brief, the Board and your last `berthier report`, and resume unfinished work."},
}

// IsBroadcast: an inbox kind that is an army-wide broadcast.
func IsBroadcast(kind string) bool { _, ok := broadcasts[kind]; return ok }

// BroadcastText is the prompt of a broadcast kind ("shutdown", "wake"): its
// tag and standing text, then the user's extra text capped at max runes
// (<= 0: uncapped). ok is false for an unknown kind.
func BroadcastText(kind, extra string, max int) (text string, ok bool) {
	b, ok := broadcasts[kind]
	if !ok {
		return "", false
	}
	extra = strings.TrimSpace(extra)
	if r := []rune(extra); max > 0 && len(r) > max {
		extra = string(r[:max]) + " [truncated]"
	}
	if extra != "" {
		return b.tag + " " + b.text + " " + extra, true
	}
	return b.tag + " " + b.text, true
}

// BroadcastTargets are the addressee names (inbox `to`) of every Agent with a
// live pane, Berthier included, and how many Frozen Agents are skipped.
func BroadcastTargets(ag Agents, berthier *string) (to []string, skipped int) {
	for _, k := range ag.Keys {
		a := ag.M[k]
		switch {
		case a.Frozen != nil:
			skipped++
		case a.Status == "gone":
		case berthier != nil && k == *berthier:
			to = append(to, "berthier")
		default:
			if n := CorpsName(strOr(a.WorkspaceLabel, "")); n != "" {
				to = append(to, n)
			}
		}
	}
	return to, skipped
}
