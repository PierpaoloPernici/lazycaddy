package logs

import (
	"math"
	"strings"
)

// Filter is the source-aware filter applied in the log dashboard.
// Every field is optional: empty means "match all". The filtering is
// bounded, read-only and runs on the in-memory history only.
type Filter struct {
	Host   string
	Status int    // exact status, 0 means any
	Class  int    // 2..5 for 2xx..5xx, 0 means any
	Level  string // exact, case-insensitive
	Text   string // substring over Raw/Msg/Logger/Host/URI, case-insensitive
}

// Matches reports whether e passes f.
func (f Filter) Matches(e Entry) bool {
	if f.Host != "" && !strings.EqualFold(e.Host, f.Host) {
		return false
	}
	if f.Status != 0 && e.Status != f.Status {
		return false
	}
	if f.Class != 0 {
		class := e.Status / 100
		if class != f.Class {
			// Status -1 (absent) never matches a class filter.
			return false
		}
	}
	if f.Level != "" && !strings.EqualFold(e.Level, f.Level) {
		return false
	}
	if f.Text != "" {
		needle := strings.ToLower(f.Text)
		hay := strings.ToLower(string(e.Raw))
		if !strings.Contains(hay, needle) {
			// Fallback to structured fields when Raw is empty or the
			// entry was not parsed: Msg/Logger/Host/URI are still
			// searchable.
			alt := strings.ToLower(strings.Join([]string{e.Msg, e.Logger, e.Host, e.URI}, " "))
			if !strings.Contains(alt, needle) {
				return false
			}
		}
	}
	return true
}

// Apply returns the entries that pass f, preserving order. A zero filter
// returns the input slice unchanged (but copied).
func Apply(entries []Entry, f Filter) []Entry {
	if f == (Filter{}) {
		out := make([]Entry, len(entries))
		copy(out, entries)
		return out
	}
	var out []Entry
	for _, e := range entries {
		if f.Matches(e) {
			out = append(out, e)
		}
	}
	return out
}

// StatusCounts is a bounded summary of the status-class distribution.
type StatusCounts struct {
	Class1xx int
	Class2xx int
	Class3xx int
	Class4xx int
	Class5xx int
	Other    int
	Total    int
}

// CountStatusClasses tallies entries by HTTP status class. Parsed == false
// or Status == -1 counts as Other only when it would not otherwise fit.
func CountStatusClasses(entries []Entry) StatusCounts {
	var c StatusCounts
	for _, e := range entries {
		c.Total++
		switch e.Status / 100 {
		case 1:
			c.Class1xx++
		case 2:
			c.Class2xx++
		case 3:
			c.Class3xx++
		case 4:
			c.Class4xx++
		case 5:
			c.Class5xx++
		default:
			c.Other++
		}
	}
	return c
}

// LatencyStats is a basic summary over the Duration field.
type LatencyStats struct {
	Count int
	Min   float64
	Max   float64
	Avg   float64
}

// SummarizeLatency computes min/max/avg over entries that carry a
// non-negative Duration (Duration < 0 means absent). An empty input or
// no durations yields Count 0 and all zeros.
func SummarizeLatency(entries []Entry) LatencyStats {
	var sum float64
	min := math.MaxFloat64
	max := -math.MaxFloat64
	n := 0
	for _, e := range entries {
		if e.Duration < 0 {
			continue
		}
		n++
		sum += e.Duration
		if e.Duration < min {
			min = e.Duration
		}
		if e.Duration > max {
			max = e.Duration
		}
	}
	if n == 0 {
		return LatencyStats{}
	}
	return LatencyStats{Count: n, Min: min, Max: max, Avg: sum / float64(n)}
}
