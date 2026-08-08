package main

import "fmt"

// These values are replaced by GoReleaser for published builds. Keeping
// useful development defaults makes local binaries identifiable as well.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func versionOutput() string {
	return fmt.Sprintf("lazycaddy %s (commit %s, built %s)\n", version, commit, date)
}
