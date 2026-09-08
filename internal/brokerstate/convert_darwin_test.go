package brokerstate_test

import (
	"testing"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
)

// TestConvertPassesTheRealOSCensus runs a conversion against the operating
// system's own census rather than a fixture. It is the composition the fixtures
// cannot prove: by the time the census runs for the second time, the converting
// process is itself holding the store and its journal open to snapshot it, so a
// census read as "any handle blocks" would refuse every real conversion.
//
// The process half is asked about a name nothing on this machine carries, so
// the test cannot be decided by whatever else happens to be running. That a
// foreign process is seen and does block is proven by the census tests and by
// the fixtures that block Convert on a foreign PID.
func TestConvertPassesTheRealOSCensus(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(t, dir)
	newLegacyStore(t, dir, fieldShapes()[1])
	census, err := brokerstate.NewOSWriterCensus()
	if err != nil {
		t.Fatal(err)
	}
	census.Binary = "waggle-census-nothing-is-called-this"

	report, err := brokerstate.Convert(t.Context(), brokerstate.NewConversionConfig(paths), census, statetest.Process{}, broker.UpgradeDomain)
	if err != nil {
		t.Fatalf("convert under the real census: %v", err)
	}
	if report.ToVersion != config.NativeSchemaVersion || report.Tasks != legacyTaskCount {
		t.Fatalf("report = %+v", report)
	}
}
