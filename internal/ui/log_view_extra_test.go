package ui

import (
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    float64
		want string
	}{
		{0.0005, "<1ms"},
		{0.01, "10ms"},
		{0.5, "500ms"},
		{1.5, "1.50s"},
		{10.123, "10.12s"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatStatusCountsAndLatency(t *testing.T) {
	c := formatStatusCounts(logs.StatusCounts{Class2xx: 2, Class4xx: 1, Total: 3})
	if c == "" {
		t.Error("formatStatusCounts empty")
	}
	s := formatLatencyStats(logs.LatencyStats{Count: 1, Min: 0.1, Max: 0.2, Avg: 0.15})
	if s == "" {
		t.Error("formatLatencyStats empty")
	}
	if got := formatStatusCounts(logs.StatusCounts{}); got != "" {
		t.Errorf("empty counts should be empty, got %q", got)
	}
	if got := formatLatencyStats(logs.LatencyStats{}); got != "" {
		t.Errorf("empty latency should be empty, got %q", got)
	}
}
