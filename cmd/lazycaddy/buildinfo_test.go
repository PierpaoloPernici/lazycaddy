package main

import "testing"

func TestVersionOutput(t *testing.T) {
	originalVersion, originalCommit, originalDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = originalVersion, originalCommit, originalDate
	})

	tests := []struct {
		name    string
		version string
		commit  string
		date    string
		want    string
	}{
		{
			name:    "development defaults",
			version: "dev",
			commit:  "unknown",
			date:    "unknown",
			want:    "lazycaddy dev (commit unknown, built unknown)\n",
		},
		{
			name:    "release metadata",
			version: "0.1.0",
			commit:  "abc1234",
			date:    "2026-08-08T00:00:00Z",
			want:    "lazycaddy 0.1.0 (commit abc1234, built 2026-08-08T00:00:00Z)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, commit, date = tt.version, tt.commit, tt.date
			if got := versionOutput(); got != tt.want {
				t.Errorf("versionOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}
