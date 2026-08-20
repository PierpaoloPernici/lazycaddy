package logs

import "testing"

func TestFilter_Matches(t *testing.T) {
	entries := []Entry{
		{Host: "example.com", Status: 200, Level: "info", Duration: 0.1, Raw: []byte(`{"host":"example.com"}`), Parsed: true, Msg: "handled"},
		{Host: "other.com", Status: 404, Level: "error", Duration: 0.5, Raw: []byte(`not found`), Parsed: false},
		{Host: "example.com", Status: 500, Level: "error", Duration: -1, Raw: []byte(`boom`), Parsed: true},
	}
	tests := []struct {
		f    Filter
		want int
	}{
		{Filter{}, 3},
		{Filter{Host: "example.com"}, 2},
		{Filter{Status: 200}, 1},
		{Filter{Class: 2}, 1},
		{Filter{Class: 4}, 1},
		{Filter{Level: "ERROR"}, 2},
		{Filter{Text: "handled"}, 1},
		{Filter{Text: "boom"}, 1},
		{Filter{Host: "example.com", Class: 5}, 1},
	}
	for i, tt := range tests {
		got := Apply(entries, tt.f)
		if len(got) != tt.want {
			t.Errorf("test %d: got %d, want %d filter %+v", i, len(got), tt.want, tt.f)
		}
	}
}

func TestCountStatusClasses(t *testing.T) {
	entries := []Entry{{Status: 200}, {Status: 301}, {Status: 404}, {Status: 500}, {Status: -1}, {Status: 101}}
	c := CountStatusClasses(entries)
	if c.Class2xx != 1 || c.Class3xx != 1 || c.Class4xx != 1 || c.Class5xx != 1 || c.Class1xx != 1 || c.Other != 1 || c.Total != 6 {
		t.Errorf("counts = %+v", c)
	}
}

func TestSummarizeLatency(t *testing.T) {
	entries := []Entry{{Duration: 0.1}, {Duration: 0.3}, {Duration: -1}, {Duration: 0.2}}
	s := SummarizeLatency(entries)
	if s.Count != 3 || s.Min != 0.1 || s.Max != 0.3 {
		t.Errorf("latency = %+v", s)
	}
	if s.Avg < 0.199 || s.Avg > 0.201 {
		t.Errorf("avg = %v, want ~0.2", s.Avg)
	}
	if got := SummarizeLatency(nil); got.Count != 0 {
		t.Error("nil should give 0 count")
	}
	if got := SummarizeLatency([]Entry{{Duration: -1}}); got.Count != 0 {
		t.Error("no durations should give 0 count")
	}
}
