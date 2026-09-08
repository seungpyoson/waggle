package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/spf13/cobra"
)

// The refusals an operator can act on, kept apart from the failures they cannot.
// A blocked conversion says the machine is not offline yet; a running broker
// says which process to stop; a store that is not legacy or not prepared says
// the requested step has already happened or never applied. Everything else is
// the operation's own failure code, which promises nothing about the cause.
const (
	codeConversionBlocked  = "CONVERSION_BLOCKED"
	codeNotLegacy          = "NOT_LEGACY"
	codeNotPrepared        = "NOT_PREPARED"
	codeBrokerRunning      = "BROKER_RUNNING"
	codeShutdownIncomplete = "SHUTDOWN_INCOMPLETE"
	codeConversionFailed   = "CONVERSION_FAILED"
	codeActivationFailed   = "ACTIVATION_FAILED"
	codeRollbackFailed     = "ROLLBACK_FAILED"
	codeInspectionFailed   = "INSPECTION_FAILED"
)

// storeCommands is the offline storage commands and the one dependency they do
// not resolve for themselves. A census reads the machine, so a test that must
// not depend on what else is running supplies its own; nothing else here is
// substitutable, because everything else is the store itself.
type storeCommands struct {
	newCensus func() (brokerstate.WriterCensus, error)
}

func newStoreCommands() storeCommands {
	return storeCommands{newCensus: osWriterCensus}
}

func osWriterCensus() (brokerstate.WriterCensus, error) {
	census, err := brokerstate.NewOSWriterCensus()
	if err != nil {
		return nil, err
	}
	return census, nil
}

func (s storeCommands) census() (brokerstate.WriterCensus, error) {
	if s.newCensus == nil {
		return nil, fmt.Errorf("%w: no writer census configured", brokerstate.ErrCensusUnavailable)
	}
	return s.newCensus()
}

// storeInspection and storeReport carry the ok flag every result starts with
// alongside the record the store package produced, without restating a single
// one of its fields here.
type storeInspection struct {
	OK bool `json:"ok"`
	brokerstate.Inspection
}

type storeReport struct {
	OK bool `json:"ok"`
	brokerstate.Report
}

// storePaths resolves the project exactly as `waggle start` does. The store
// commands never touch the process-wide paths a broker-bound command uses:
// they run before there is a broker, and one of them exists to move the file
// those paths point at.
func storePaths(ctx context.Context) (config.Paths, error) {
	projectID, err := config.ResolveProjectID(ctx)
	if err != nil {
		return config.Paths{}, err
	}
	resolved := config.NewPaths(projectID)
	if resolved.DataDir == "" {
		return config.Paths{}, fmt.Errorf("cannot determine data paths: HOME not set")
	}
	return resolved, nil
}

// inspect reports the storage as it stands and changes nothing, so a missing
// store is an answer rather than a failure: an operator asking what is there
// is entitled to be told that nothing is.
func (s storeCommands) inspect(ctx context.Context) (any, string, error) {
	resolved, err := storePaths(ctx)
	if err != nil {
		return nil, codeInspectionFailed, err
	}
	view, err := brokerstate.Inspect(ctx, brokerstate.NewConversionConfig(resolved), resolved)
	if err != nil {
		return nil, codeInspectionFailed, err
	}
	return storeInspection{OK: true, Inspection: view}, "", nil
}

// convert upgrades a schema-v1 store to native storage, offline. The store it
// leaves behind is prepared, never active: admitting a broker stays a separate
// decision an operator makes with activate.
func (s storeCommands) convert(ctx context.Context) (any, string, error) {
	resolved, err := storePaths(ctx)
	if err != nil {
		return nil, codeConversionFailed, err
	}
	census, err := s.census()
	if err != nil {
		return nil, failureCode(err, codeConversionFailed), err
	}
	report, err := brokerstate.Convert(ctx, brokerstate.NewConversionConfig(resolved), census,
		brokerstate.OSProcessInspector{}, broker.UpgradeDomain)
	if err != nil {
		return nil, failureCode(err, codeConversionFailed), err
	}
	return storeReport{OK: true, Report: report}, "", nil
}

// rollback restores the snapshot a prepared store recorded as its origin. An
// activated store is past the point where that undo is honest, and says so.
func (s storeCommands) rollback(ctx context.Context) (any, string, error) {
	resolved, err := storePaths(ctx)
	if err != nil {
		return nil, codeRollbackFailed, err
	}
	census, err := s.census()
	if err != nil {
		return nil, failureCode(err, codeRollbackFailed), err
	}
	report, err := brokerstate.Rollback(ctx, brokerstate.NewConversionConfig(resolved), census)
	if err != nil {
		return nil, failureCode(err, codeRollbackFailed), err
	}
	return storeReport{OK: true, Report: report}, "", nil
}

// activate admits a converted store to native service. It holds ownership only
// for that one transition and releases it before returning, so the broker this
// activation exists to permit can start immediately afterwards.
func (s storeCommands) activate(ctx context.Context) (result any, code string, err error) {
	resolved, err := storePaths(ctx)
	if err != nil {
		return nil, codeActivationFailed, err
	}
	owner, err := brokerstate.Acquire(ctx, config.NewOwnershipConfig(resolved.DB, config.OpenStore), brokerstate.OSProcessInspector{})
	if err != nil {
		return nil, failureCode(err, codeActivationFailed), err
	}
	// Ownership is released on every path from here, including the failing
	// ones: a command that exits still owning the store leaves the next broker
	// waiting on a process that is gone.
	defer func() {
		owner.BeginShutdown(nil)
		release, cancel := context.WithTimeout(context.Background(), config.Defaults.ShutdownTimeout)
		defer cancel()
		if releaseErr := owner.Wait(release); releaseErr != nil {
			err = errors.Join(err, releaseErr)
			result, code = nil, failureCode(err, codeActivationFailed)
		}
	}()
	already, err := activateOwnedStore(ctx, owner)
	if err != nil {
		return nil, failureCode(err, codeActivationFailed), err
	}
	message := "store activated"
	if already {
		message = "store already active"
	}
	return map[string]any{"ok": true, "message": message}, "", nil
}

// activateOwnedStore performs the transition and reports whether the store was
// already active, which is the one thing Activate cannot say: reaching the
// active state is success either way, and an operator still needs to know
// which of the two happened. Both answers come from held ownership, so no
// other process can move the store between the question and the transition.
func activateOwnedStore(ctx context.Context, owner *brokerstate.Owner) (bool, error) {
	already := false
	if err := owner.Do(ctx, func(op *brokerstate.Operation) error {
		return op.Write(ctx, func(tx *brokerstate.WriteTx) error {
			err := tx.RequireActive()
			if err == nil {
				already = true
				return nil
			}
			if errors.Is(err, brokerstate.ErrPrepared) {
				return nil
			}
			return err
		})
	}); err != nil {
		return false, err
	}
	if already {
		return true, nil
	}
	return false, owner.Activate(ctx)
}

// failureCode names the refusals an operator can act on. Everything else keeps
// the operation's own failure code rather than being sorted into a cause this
// command did not establish.
func failureCode(err error, failure string) string {
	switch {
	case errors.Is(err, brokerstate.ErrWritersPresent), errors.Is(err, brokerstate.ErrCensusUnavailable):
		return codeConversionBlocked
	case errors.Is(err, brokerstate.ErrNotLegacy):
		return codeNotLegacy
	case errors.Is(err, brokerstate.ErrNotPrepared):
		return codeNotPrepared
	case errors.Is(err, brokerstate.ErrOwnerAlive):
		return codeBrokerRunning
	case errors.Is(err, brokerstate.ErrShutdownIncomplete), errors.Is(err, brokerstate.ErrFinalizationFailed):
		return codeShutdownIncomplete
	default:
		return failure
	}
}

func init() {
	rootCmd.AddCommand(newStoreCommand(newStoreCommands()))
}

// newStoreCommand builds the offline storage commands. Each subcommand's work
// is a function returning its result, its failure code and its error; this
// layer decides nothing and renders one of the two.
func newStoreCommand(commands storeCommands) *cobra.Command {
	store := &cobra.Command{
		Use:   "store",
		Short: "Inspect, convert, activate or roll back this project's canonical store",
		Long: "Offline commands for this project's canonical store.\n\n" +
			"They run without a broker, and conversion requires that no broker is running at all:\n" +
			"the machine is surveyed for old Waggle processes and open handles before anything changes.\n" +
			"A conversion snapshots the store first and leaves it prepared; activate admits it to service.",
		// Runnable so NoArgs is actually reached: a group that is not runnable
		// answers a mistyped subcommand with its own help and a success status,
		// and a storage command that silently did nothing is the last thing an
		// operator mid-conversion should have to notice for themselves.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	store.AddCommand(
		storeSubcommand("inspect", "Report the store's schema version, cutover state and snapshots",
			"Report what this project's storage holds, changing nothing. A store that is not there is reported as absent, not as a failure.",
			commands.inspect),
		storeSubcommand("convert", "Upgrade a schema-v1 store to native storage, offline",
			"Upgrade the schema-v1 store this project already has to native storage.\n\n"+
				"The machine is surveyed for old Waggle processes and for handles on the store, twice, and any\n"+
				"uncertainty blocks the conversion. A snapshot is taken before anything changes, and every change\n"+
				"happens in one transaction, so an interruption leaves the store as it was. The converted store is\n"+
				"prepared: run `waggle store activate` to admit a broker to it.",
			commands.convert),
		storeSubcommand("activate", "Admit a converted store to native service",
			"Move a prepared store to active, so a broker may start on it. Ownership is held for the transition\n"+
				"alone and released before this command returns. A broker that is already running refuses it.",
			commands.activate),
		storeSubcommand("rollback", "Restore the snapshot a prepared store was converted from",
			"Restore the snapshot this store's conversion recorded as its origin, undoing the conversion.\n\n"+
				"Only a prepared store can be rolled back: once activated, the store has been admitted to service\n"+
				"and its snapshot is no longer an honest account of it.",
			commands.rollback),
	)
	return store
}

func storeSubcommand(use, short, long string, run func(context.Context) (any, string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, code, err := run(cmd.Context())
			if err != nil {
				printErr(code, err.Error())
				return nil
			}
			printJSON(result)
			return nil
		},
	}
}
