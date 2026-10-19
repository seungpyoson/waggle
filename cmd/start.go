package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

var foreground, initialize bool
var startupFD int
var codexAppServer string

func init() {
	startCmd.Flags().BoolVar(&foreground, "foreground", false, "Run broker in the foreground")
	startCmd.Flags().BoolVar(&initialize, "initialize", false, "Create a fresh canonical store; existing stores are rejected")
	startCmd.Flags().StringVar(&codexAppServer, "codex-app-server", "", "Register the exact independently owned Codex Unix App Server socket")
	startCmd.Flags().IntVar(&startupFD, "startup-fd", 0, "Internal daemon startup channel")
	startCmd.Flags().MarkHidden("startup-fd")
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the independently owned project broker",
	RunE: func(cmd *cobra.Command, args []string) error {
		var startup *os.File
		if startupFD != 0 {
			if !foreground || startupFD != config.StartupPipeFD {
				return fmt.Errorf("invalid daemon startup channel")
			}
			startup = os.NewFile(uintptr(startupFD), "broker-startup")
			defer startup.Close()
		}
		// One result goes to the launching parent, including failures before acquisition.
		report := func(err error) error {
			response := protocol.OKResponse(nil)
			if err != nil {
				response = protocol.ErrResponse(protocol.ErrInternalError, err.Error())
			}
			if startup != nil {
				return json.NewEncoder(startup).Encode(response)
			}
			return nil
		}
		provider := config.ProviderConfig{CodexEndpoint: codexAppServer, ClientVersion: Version, Transport: config.NewNativeConfig()}
		if err := provider.Validate(); err != nil {
			return errors.Join(err, report(err))
		}
		projectID, err := config.ResolveProjectID(cmd.Context())
		if err != nil {
			return errors.Join(err, report(err))
		}
		paths = config.NewPaths(projectID)
		if paths.DataDir == "" {
			err := fmt.Errorf("cannot determine paths: HOME not set")
			return errors.Join(err, report(err))
		}
		if !foreground {
			daemonArgs := []string{os.Args[0], "start", "--foreground"}
			if initialize {
				daemonArgs = append(daemonArgs, "--initialize")
			}
			daemonArgs = append(daemonArgs, "--codex-app-server", codexAppServer)
			if err := broker.StartDaemon(paths.DataDir, filepath.Dir(paths.Socket), paths.Log, projectID, daemonArgs); err != nil {
				return err
			}
			printJSON(map[string]any{"ok": true, "message": "broker started"})
			return nil
		}
		if err := broker.EnsureDirs(paths.DataDir, filepath.Dir(paths.Socket)); err != nil {
			return errors.Join(err, report(err))
		}
		action := config.OpenStore
		if initialize {
			action = config.CreateStore
		}
		owner, err := brokerstate.Acquire(cmd.Context(), config.NewOwnershipConfig(paths.DB, action), brokerstate.OSProcessInspector{})
		if err != nil {
			return errors.Join(err, report(err))
		}
		// Startup failures before service release ownership under the same bounded wait.
		fail := func(err error) error {
			owner.BeginShutdown(err)
			waitCtx, cancel := context.WithTimeout(context.Background(), config.Defaults.ShutdownTimeout)
			defer cancel()
			return errors.Join(err, owner.Wait(waitCtx), report(err))
		}
		if initialize {
			if err := broker.Initialize(cmd.Context(), owner); err != nil {
				return fail(err)
			}
		}
		b, err := broker.New(cmd.Context(), owner, config.NewBrokerConfig(config.BrokerEndpoints{Socket: paths.Socket, PID: paths.PID}), provider)
		if err != nil {
			return fail(err)
		}
		if err := report(nil); err != nil {
			return fail(err)
		}
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signals)
		return supervise(cmd.Context(), signals, b.Serve, owner, config.Defaults.ShutdownTimeout, cmd.ErrOrStderr())
	},
}
