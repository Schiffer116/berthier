package core

import (
	"strings"
	"testing"
)

func TestCacheOf(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-10-23T10:00:00Z","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-10-23T10:00:05Z","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":3,"cache_read_input_tokens":150000,"cache_creation_input_tokens":2000,"output_tokens":500,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2000}}}}`,
		`{"type":"assistant","timestamp":"2026-10-23T12:00:00Z","message":{"id":"m2","model":"<synthetic>","usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":0}}}`,
		`{"type":"system","timestamp":"2026-10-23T12:00:01Z"}`,
	}
	c, ok := CacheOf(lines)
	check(t, "the last real call, not the synthetic limit stop", []any{ok, c}, `[true,{"last_at":1792749605,"context":152503,"model":"claude-opus-5-5","ttl_1h":true}]`)
	usd, _ := c.ReviveUSD()
	check(t, "a Revive writes the context at the 1h rate", round2(usd), `1.22`)
	c.TTL1h = false
	usd, _ = c.ReviveUSD()
	check(t, "or the 5m one", round2(usd), `0.76`)
	if _, ok := CacheOf(lines[2:]); ok {
		t.Error("a cache from no real call")
	}
	if _, ok := (Cache{Model: "gpt-9"}).ReviveUSD(); ok {
		t.Error("an unpriced model priced")
	}
	cfg := Config{CacheWarm: 3300, FrozenMin: 50000}
	check(t, "warm, small, cold", []bool{Cache{LastAt: 1000, Context: 90000}.Warm(4299, cfg), Cache{Context: 49999}.Warm(1e9, cfg), Cache{LastAt: 1000, Context: 90000}.Warm(4300, cfg)}, `[true,true,false]`)
}

func TestFreshPrompt(t *testing.T) {
	p := FreshPrompt("halfway", "Build it.")
	if OriginOf(p) != "fresh" || !strings.Contains(p, "run berthier notes, read your Board, your last report and git status") ||
		!strings.Contains(p, "Your last report: halfway") || !strings.HasSuffix(p, "Your original Brief:\n\nBuild it.") {
		t.Errorf("fresh prompt: %q", p)
	}
	if p := FreshPrompt("", ""); strings.Contains(p, "\n") {
		t.Errorf("a Main Agent's fresh prompt: %q", p)
	}
}
