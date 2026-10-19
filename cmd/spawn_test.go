package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnMissingBrokerDoesNotLaunchOrCreateRegistration(t *testing.T) {
	home, commands := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", commands)
	t.Setenv("WAGGLE_PROJECT_ID", "spawn-test")
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	marker := filepath.Join(commands, "launched")
	t.Setenv("SPAWN_LAUNCH_LOG", marker)
	if err := os.WriteFile(filepath.Join(commands, "osascript"), []byte("#!/bin/sh\nprintf x >> \"$SPAWN_LAUNCH_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := executeRootCommandForTestWithError(t, "spawn", "--name", "worker")
	if err == nil || !strings.Contains(err.Error(), "broker unavailable; run waggle start") || strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("missing broker reported launch success: stdout=%q err=%v", stdout, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("missing broker launched terminal: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed launch created state: %v %v", entries, err)
	}
}

func TestSpawnRejectsArgumentsAndEmptyLabel(t *testing.T) {
	t.Setenv("WAGGLE_PROJECT_ID", "spawn-test")
	for _, args := range [][]string{
		{"spawn", "unexpected", "--name", "worker"},
		{"spawn", "--name", " "},
	} {
		if _, _, err := executeRootCommandForTestWithError(t, args...); err == nil {
			t.Fatalf("invalid spawn accepted: %v", args)
		}
	}
}

func TestAllCommandHelpWorksWithoutProjectBrokerOrTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("WAGGLE_PROJECT_ID", "")
	t.Setenv("WAGGLE_ROOT", "")
	t.Chdir(t.TempDir())
	var visit func([]string)
	visit = func(path []string) {
		command, _, err := rootCmd.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		stdout, _, err := executeRootCommandForTestWithError(t, append(append([]string{}, path...), "--help")...)
		if err != nil || !strings.Contains(stdout, "Usage:") {
			t.Fatalf("help failed for %v: %q %v", path, stdout, err)
		}
		for _, child := range command.Commands() {
			visit(append(append([]string{}, path...), child.Name()))
		}
	}
	visit(nil)
}
