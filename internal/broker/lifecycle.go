package broker

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

// EnsureDirs creates the specified directories if they don't exist.
func EnsureDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}
	return nil
}

// appendEnvOverride removes any existing entry for key from env, then appends key=value.
// This ensures the injected value always wins, even if the key was already set.
func appendEnvOverride(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, prefix+value)
}

// StartDaemon forks the broker as a background process.
// It redirects stdout/stderr to logFile and returns immediately.
func StartDaemon(dataDir, socketDir, logFile, projectID string, args []string) error {
	// Ensure directories exist
	if err := EnsureDirs(dataDir, socketDir); err != nil {
		return err
	}

	// Open log file for stdout/stderr
	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer log.Close()

	// Get current executable path
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}

	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create startup channel: %w", err)
	}
	defer readyRead.Close()
	args = append(args, "--startup-fd="+strconv.Itoa(config.StartupPipeFD))
	procAttr := &os.ProcAttr{
		Files: []*os.File{nil, log, log, readyWrite},
		Env:   appendEnvOverride(os.Environ(), "WAGGLE_PROJECT_ID", projectID),
	}
	process, err := os.StartProcess(exe, args, procAttr)
	closeErr := readyWrite.Close()
	if err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	defer process.Release()
	if closeErr != nil {
		return closeErr
	}
	if err := readyRead.SetReadDeadline(time.Now().Add(config.Defaults.StartupTimeout)); err != nil {
		return err
	}
	var response protocol.Response
	if err := json.NewDecoder(readyRead).Decode(&response); err != nil {
		return fmt.Errorf("read broker startup result (see %s): %w", logFile, err)
	}
	if !response.OK {
		return fmt.Errorf("broker startup rejected: %s", response.Error)
	}
	return nil
}
