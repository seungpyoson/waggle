package brokerstate_test

import (
	"context"
	"testing"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
)

// realHandleCensus is the operating system's own open-handle census, taken with
// the real tool against real files, with the process half left unasked.
//
// The process half cannot take part in this proof. It reads every process on
// the machine and matches the canonical binary name, and the end-to-end tests
// of this module run real brokers under that name while this test runs. A
// conversion refused because one of them is up is the guard working as
// designed, not a failure of the code under test. That a foreign process is
// seen and does block is proven by the census tests and by the fixtures that
// block Convert on a foreign PID.
type realHandleCensus struct{ brokerstate.OSWriterCensus }

// WaggleProcesses reports no old writers. That is a statement about which
// question this test asks, not a claim about the machine it runs on, and Scope
// says so in the words the report carries to an operator.
func (realHandleCensus) WaggleProcesses(context.Context) ([]brokerstate.Handle, error) {
	return nil, nil
}

// realHandleScope is what this census honestly saw: the open-handle half only.
const realHandleScope = "same-user open handles, taken with the real census tool; no process census was taken"

func (realHandleCensus) Scope() string { return realHandleScope }

// TestConvertPassesTheRealOSCensus runs a conversion against the operating
// system's own open-handle census rather than a fixture. It is the composition
// the fixtures cannot prove: by the time the census runs for the second time,
// the converting process is itself holding the store and its journal open to
// snapshot it, so a census read as "any handle blocks" would refuse every real
// conversion.
func TestConvertPassesTheRealOSCensus(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(t, dir)
	newLegacyStore(t, dir, fieldShapes()[1])
	real, err := brokerstate.NewOSWriterCensus()
	if err != nil {
		t.Fatal(err)
	}
	census := realHandleCensus{OSWriterCensus: real}

	report, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), census, statetest.Process{}, broker.UpgradeDomain)
	if err != nil {
		t.Fatalf("convert under the real census: %v", err)
	}
	if report.ToVersion != config.NativeSchemaVersion || report.Tasks != legacyTaskCount {
		t.Fatalf("report = %+v", report)
	}
	if report.CensusScope != census.Scope() || report.CensusScope == "" {
		t.Fatalf("report census scope = %q, want the census's own %q", report.CensusScope, census.Scope())
	}
}
