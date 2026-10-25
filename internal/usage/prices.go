// Package usage keeps what coding agents spent: the token totals each agent
// session its owner runs reports through `nodevas hook`, alongside the usage
// runs already record, and the prices that turn tokens into an estimated cost.
//
// Costs are API-equivalent estimates. Agents signed in with a subscription
// pay nothing per token, and the prices here are a vendor's list prices on a
// date (PricesAsOf), so a figure is a reference for comparing plans and
// harnesses, not a bill. Where an agent says what a session cost (Claude
// Code's -p result, OpenCode), that figure wins over the estimate.
package usage

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PricesAsOf is the date the built-in prices were taken from the vendors'
// price lists.
const PricesAsOf = "2026-10-06"

// Limits on what a price table holds.
const (
	MaxCustomPrices = 200
	MaxPricePerMTok = 10_000.0
	MaxModelLen     = 128
)

// Sources of a price.
const (
	SourceBuiltin = "builtin"
	SourceCustom  = "custom"
)

// Price is what one model costs, in USD per million tokens. Model names it
// exactly or as a prefix (see Table.Lookup); it may hold one "*", which
// matches anything.
type Price struct {
	Model             string  `json:"model"`
	InputPerMTok      float64 `json:"inputPerMTok"`
	OutputPerMTok     float64 `json:"outputPerMTok"`
	CacheReadPerMTok  float64 `json:"cacheReadPerMTok"`
	CacheWritePerMTok float64 `json:"cacheWritePerMTok"`
	Source            string  `json:"source"`
}

// builtinPrices are Anthropic's first-party list prices on PricesAsOf (cache
// writes at the 5-minute rate). Other vendors' models are left unpriced on
// purpose rather than guessed: the owner adds what they pay as custom prices.
var builtinPrices = []Price{
	price("claude-fable-5-1", 10, 50, 0.25, 12.5),
	price("claude-mythos-5-1", 10, 50, 0.25, 12.5),
	price("claude-fable-5", 10, 50, 1, 12.5),
	price("claude-mythos-5", 10, 50, 1, 12.5),
	price("claude-opus-5-5", 4, 20, 0.20, 5),
	price("claude-opus-5", 5, 25, 0.5, 6.25),
	price("claude-opus-4-8", 5, 25, 0.5, 6.25),
	price("claude-opus-4-7", 5, 25, 0.5, 6.25),
	price("claude-opus-4-6", 5, 25, 0.5, 6.25),
	price("claude-opus-4-5", 5, 25, 0.5, 6.25),
	// Claude Opus 4 and 4.1.
	price("claude-opus-4", 15, 75, 1.5, 18.75),
	price("claude-sonnet-5-5", 2, 10, 0.20, 2.5),
	price("claude-sonnet-5", 2, 10, 0.2, 2.5),
	// Claude Sonnet 4.x.
	price("claude-sonnet-4", 3, 15, 0.3, 3.75),
	price("claude-haiku-5-5", 0.10, 0.50, 0.01, 0.125),
	price("claude-haiku-4-5", 1, 5, 0.1, 1.25),
	// Claude Code's model aliases, as they resolve on PricesAsOf (an
	// assumption: the CLI moves them to newer models over time).
	price("opus", 4, 20, 0.20, 5),
	price("sonnet", 2, 10, 0.20, 2.5),
	price("haiku", 0.10, 0.50, 0.01, 0.125),
	// OpenCode's free models cost nothing.
	price("opencode/*-free", 0, 0, 0, 0),
}

func price(model string, in, out, read, write float64) Price {
	return Price{Model: model, InputPerMTok: in, OutputPerMTok: out, CacheReadPerMTok: read,
		CacheWritePerMTok: write, Source: SourceBuiltin}
}

// BuiltinPrices is a copy of the built-in table.
func BuiltinPrices() []Price { return append([]Price(nil), builtinPrices...) }

// Table is the prices a cost is estimated from: the owner's custom ones over
// the built-in ones.
type Table struct {
	prices []Price
}

// NewTable is the built-in prices with custom ones over them.
func NewTable(custom []Price) Table {
	all := make([]Price, 0, len(custom)+len(builtinPrices))
	for _, p := range custom {
		p.Model = normalizeModel(p.Model)
		p.Source = SourceCustom
		all = append(all, p)
	}
	return Table{prices: append(all, builtinPrices...)}
}

// Prices lists the table, custom prices first, each group by model.
func (t Table) Prices() []Price {
	out := append([]Price(nil), t.prices...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source == SourceCustom
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// Lookup finds model's price. A model is tried as reported, after its last
// "/" (a provider's prefix: "anthropic/claude-…"), and from "claude-" on
// (cloud prefixes: "us.anthropic.claude-…"). An entry matches a name that
// equals it or starts with it followed by a separator, so date and variant
// suffixes ("-20250929", "@…", "[1m]") still match; the longest matching entry
// wins, a custom one over a built-in one of the same length.
func (t Table) Lookup(model string) (Price, bool) {
	var best Price
	bestLen, found := -1, false
	for _, name := range candidates(model) {
		for _, p := range t.prices {
			n, ok := matchLen(p.Model, name)
			if !ok {
				continue
			}
			if n > bestLen || (n == bestLen && p.Source == SourceCustom && best.Source != SourceCustom) {
				best, bestLen, found = p, n, true
			}
		}
	}
	return best, found
}

// candidates are the forms of a model name Lookup tries.
func candidates(model string) []string {
	m := normalizeModel(model)
	if m == "" {
		return nil
	}
	out := []string{m}
	if i := strings.LastIndex(m, "/"); i >= 0 && i < len(m)-1 {
		out = append(out, m[i+1:])
	}
	if i := strings.Index(m, "claude-"); i > 0 {
		out = append(out, m[i:])
	}
	return out
}

// matchLen reports whether pattern matches name, and how much of it is
// literal (the longer, the more specific).
func matchLen(pattern, name string) (int, bool) {
	if pattern == "" {
		return 0, false
	}
	if before, after, ok := strings.Cut(pattern, "*"); ok {
		if len(name) >= len(before)+len(after) && strings.HasPrefix(name, before) && strings.HasSuffix(name, after) {
			return len(before) + len(after), true
		}
		return 0, false
	}
	if name == pattern {
		// An exact match beats any prefix of the same entry.
		return len(pattern) + 1, true
	}
	if strings.HasPrefix(name, pattern) && isSeparator(name[len(pattern)]) {
		return len(pattern), true
	}
	return 0, false
}

func isSeparator(b byte) bool {
	switch b {
	case '-', '@', '.', ':', '_', '[', '/', ' ':
		return true
	}
	return false
}

// normalizeModel is a model name as stored and compared: trimmed and lower
// case.
func normalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// Tokens are one usage figure's token counts.
type Tokens struct {
	Input, Output, CacheRead, CacheWrite int64
}

// Total is every token counted.
func (t Tokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// Cost is what tokens cost at p, in USD.
func (p Price) Cost(t Tokens) float64 {
	return (float64(t.Input)*p.InputPerMTok + float64(t.Output)*p.OutputPerMTok +
		float64(t.CacheRead)*p.CacheReadPerMTok + float64(t.CacheWrite)*p.CacheWritePerMTok) / 1e6
}

// ErrInvalid marks a request the caller can fix.
var ErrInvalid = errors.New("invalid argument")

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// ValidModelName reports whether s may name a model: text, short, no control
// characters.
func ValidModelName(s string) bool {
	if s == "" || len(s) > MaxModelLen || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ValidatePrices checks a custom price table.
func ValidatePrices(prices []Price) error {
	if len(prices) > MaxCustomPrices {
		return invalidf("at most %d prices", MaxCustomPrices)
	}
	seen := make(map[string]bool, len(prices))
	for _, p := range prices {
		model := normalizeModel(p.Model)
		if !ValidModelName(model) {
			return invalidf("a price's model is empty, too long or not text")
		}
		if strings.Count(model, "*") > 1 {
			return invalidf("model %q: at most one *", p.Model)
		}
		if seen[model] {
			return invalidf("model %q is priced twice", p.Model)
		}
		seen[model] = true
		for _, v := range []float64{p.InputPerMTok, p.OutputPerMTok, p.CacheReadPerMTok, p.CacheWritePerMTok} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > MaxPricePerMTok {
				return invalidf("model %q: prices are between 0 and %.0f USD per million tokens", p.Model, MaxPricePerMTok)
			}
		}
	}
	return nil
}
