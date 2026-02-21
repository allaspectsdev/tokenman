package version

import "fmt"

// Set via ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func String() string {
	return fmt.Sprintf("tokenman %s (commit: %s, built: %s)", Version, GitCommit, BuildDate)
}
