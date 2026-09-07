package broker

import (
	"strings"
	"testing"
)

func TestAppendEnvOverride_ReplacesExisting(t *testing.T) {
	env := []string{"PATH=/usr/bin", "WAGGLE_PROJECT_ID=old-value", "HOME=/home/user"}
	result := appendEnvOverride(env, "WAGGLE_PROJECT_ID", "new-value")

	count := 0
	var found string
	for _, e := range result {
		if strings.HasPrefix(e, "WAGGLE_PROJECT_ID=") {
			count++
			found = e
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 WAGGLE_PROJECT_ID entry, got %d", count)
	}
	if found != "WAGGLE_PROJECT_ID=new-value" {
		t.Fatalf("got %q, want %q", found, "WAGGLE_PROJECT_ID=new-value")
	}
	// Verify other env vars preserved
	if len(result) != 3 {
		t.Fatalf("expected 3 entries (PATH, HOME, WAGGLE_PROJECT_ID), got %d", len(result))
	}
}
