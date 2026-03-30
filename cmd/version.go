package cmd

import (
	"runtime"

	"github.com/spf13/cobra"
)

var (
	// Version is the waggle version, injected at build time via -ldflags
	Version = "dev"
	// Commit is the git commit hash, injected at build time via -ldflags
	Commit = "unknown"
	// BuildTime is the build timestamp, injected at build time via -ldflags
	BuildTime = "unknown"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show waggle version and build information",
	RunE: func(cmd *cobra.Command, args []string) error {
		result := map[string]any{
			"ok":      true,
			"version": Version,
			"commit":  Commit,
			"built":   BuildTime,
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
			"go":      runtime.Version(),
		}
		printJSON(result)
		return nil
	},
}

