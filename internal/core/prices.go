package core

import "strings"

// Price is a model's API price in dollars per million tokens. Cache writes
// cost 1.25x input (5-minute TTL) or 2x input (1-hour TTL).
type Price struct {
	In, Out, CacheRead float64
	// Above LongPrompt prompt tokens (input + cache) the Long rates apply.
	LongPrompt              int64
	LongIn, LongOut, LongCR float64
}

// Prices are Anthropic's first-party API rates (claude-api skill, cached
// 2026-10-06). Never fill this in from memory: a missing model is counted in
// tokens, priced at zero and flagged unpriced.
var Prices = map[string]Price{
	"claude-fable-5-1":  {In: 10, Out: 50, CacheRead: 0.25},
	"claude-mythos-5-1": {In: 10, Out: 50, CacheRead: 0.25},
	"claude-fable-5":    {In: 10, Out: 50, CacheRead: 1},
	"claude-mythos-5":   {In: 10, Out: 50, CacheRead: 1},
	"claude-opus-5-5":   {In: 4, Out: 20, CacheRead: 0.20},
	"claude-opus-5":     {In: 5, Out: 25, CacheRead: 0.50},
	"claude-opus-4-8":   {In: 5, Out: 25, CacheRead: 0.50},
	"claude-opus-4-7":   {In: 5, Out: 25, CacheRead: 0.50},
	"claude-opus-4-6":   {In: 5, Out: 25, CacheRead: 0.50},
	"claude-sonnet-5-5": {In: 2, Out: 10, CacheRead: 0.20},
	"claude-sonnet-5":   {In: 2, Out: 10, CacheRead: 0.20},
	"claude-sonnet-4-6": {In: 3, Out: 15, CacheRead: 0.30},
	"claude-haiku-5-5":  {In: 0.10, Out: 0.50, CacheRead: 0.01, LongPrompt: 100000, LongIn: 0.50, LongOut: 2.50, LongCR: 0.05},
	"claude-haiku-4-5":  {In: 1, Out: 5, CacheRead: 0.10},
}

// PriceOf finds a model's price: an exact id, else the longest priced id it
// starts with (dated snapshots, a "[1m]" suffix).
func PriceOf(model string) (Price, bool) {
	if p, ok := Prices[model]; ok {
		return p, true
	}
	best := ""
	for id := range Prices {
		if strings.HasPrefix(model, id) && len(id) > len(best) {
			best = id
		}
	}
	return Prices[best], best != ""
}

// Tokens is a count of tokens by kind.
type Tokens struct {
	In      int64 `json:"in"`
	CR      int64 `json:"cache_read"`
	W5m     int64 `json:"write_5m"`
	W1h     int64 `json:"write_1h"`
	Out     int64 `json:"out"`
	Unknown int64 `json:"write_unknown,omitempty"` // cache writes without a TTL split, priced as 5m
}

func (t *Tokens) Add(o Tokens) {
	t.In += o.In
	t.CR += o.CR
	t.W5m += o.W5m
	t.W1h += o.W1h
	t.Out += o.Out
	t.Unknown += o.Unknown
}

func (t Tokens) Sub(o Tokens) Tokens {
	return Tokens{t.In - o.In, t.CR - o.CR, t.W5m - o.W5m, t.W1h - o.W1h, t.Out - o.Out, t.Unknown - o.Unknown}
}

// Writes is every cache write, whatever its TTL.
func (t Tokens) Writes() int64 { return t.W5m + t.W1h + t.Unknown }

// Prompt is the call's whole context: input plus cache read and written.
func (t Tokens) Prompt() int64 { return t.In + t.CR + t.Writes() }

// USD prices one API call's tokens; fast mode costs twice the standard rate.
func (p Price) USD(t Tokens, fast bool) float64 {
	in, out, cr := p.In, p.Out, p.CacheRead
	if p.LongPrompt > 0 && t.Prompt() > p.LongPrompt {
		in, out, cr = p.LongIn, p.LongOut, p.LongCR
	}
	usd := (float64(t.In)*in + float64(t.CR)*cr + float64(t.W5m+t.Unknown)*in*1.25 + float64(t.W1h)*in*2 + float64(t.Out)*out) / 1e6
	if fast {
		usd *= 2
	}
	return usd
}
