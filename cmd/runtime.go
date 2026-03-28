package cmd

import (
	"fmt"

	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(runtimeCmd)
}

var runtimeCmd = &cobra.Command{
	Use:   "runtime",
	Short: "Manage the local machine runtime",
}

func resolveRuntimePaths() (config.Paths, error) {
	paths := config.NewPaths("")
	if paths.RuntimeDir == "" || paths.RuntimeDB == "" || paths.RuntimePID == "" || paths.RuntimeLog == "" || paths.RuntimeState == "" || paths.RuntimeStartLockDir == "" {
		return config.Paths{}, fmt.Errorf("cannot determine runtime paths: HOME not set")
	}
	return paths, nil
}

func resolveRuntimeProjectID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return config.ResolveProjectID()
}

func openRuntimeStore() (*rt.Store, config.Paths, error) {
	runtimePaths, err := resolveRuntimePaths()
	if err != nil {
		return nil, config.Paths{}, err
	}

	store, err := rt.OpenStore(runtimePaths)
	if err != nil {
		return nil, config.Paths{}, err
	}
	return store, runtimePaths, nil
}
