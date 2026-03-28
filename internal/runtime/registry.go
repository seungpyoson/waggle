package runtime

import (
	"fmt"

	"github.com/seungpyoson/waggle/internal/config"
)

func OpenStore(paths config.Paths) (*Store, error) {
	if paths.RuntimeDB == "" {
		return nil, fmt.Errorf("runtime database path required")
	}
	return NewStore(paths.RuntimeDB)
}

func RegisterWatch(paths config.Paths, w Watch) error {
	store, err := OpenStore(paths)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.UpsertWatch(w)
}

func UnregisterWatch(paths config.Paths, projectID, agentName string) error {
	store, err := OpenStore(paths)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.RemoveWatch(projectID, agentName)
}
