package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStatusFailureCannotClaimBrokerRunningOrSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WAGGLE_PROJECT_ID", "status-fixture")
	for _, name := range []string{"connection refused", "response lost", "deadline exceeded", "rejected", "empty observation"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			command := newStatusCommand(func(context.Context, string) (json.RawMessage, error) {
				calls++
				return nil, errors.New(name)
			})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SilenceErrors, command.SilenceUsage = true, true
			command.SetArgs(nil)
			if err := command.Execute(); err == nil {
				t.Fatal("failed observation returned success")
			}
			var result struct {
				OK     bool
				Broker map[string]any
			}
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatal(err, output.String())
			}
			if result.OK || result.Broker["status"] != "unknown" || strings.Contains(output.String(), "running") || calls != 1 {
				t.Fatalf("manufactured status or retried: %s", output.String())
			}
		})
	}
}
