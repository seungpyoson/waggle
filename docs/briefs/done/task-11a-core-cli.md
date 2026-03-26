# Task 11a: Core CLI Commands — MVP Commands

**Branch:** `feat/11-cli`
**Files:** `main.go`, `cmd/root.go`, `cmd/start.go`, `cmd/stop.go`, `cmd/status.go`, `cmd/task.go`, `cmd/task_create.go`, `cmd/task_list.go`, `cmd/task_claim.go`, `cmd/task_complete.go`, `cmd/task_fail.go`, `cmd/lock.go`, `cmd/unlock.go`, `cmd/locks.go`
**Depends on:** Tasks 1 (config), 8 (client), 9 (broker), 10 (lifecycle)

These are the commands the smoke test needs. Get these right first.

## Dependencies

```bash
go get github.com/spf13/cobra@latest
go mod tidy
```

## Files to create

### main.go
```go
package main

import "github.com/seungpyoson/waggle/cmd"

func main() {
    cmd.Execute()
}
```

### cmd/root.go — Shared infrastructure

```go
// Root command + helper functions used by all commands
var rootCmd = &cobra.Command{
    Use:   "waggle",
    Short: "Agent session coordination broker",
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}

// Helper: resolve project root + paths
func resolvePaths() (config.Paths, error) {
    cwd, _ := os.Getwd()
    root, err := config.FindProjectRoot(cwd)
    if err != nil {
        return config.Paths{}, err
    }
    return config.NewPaths(root), nil
}

// Helper: connect to broker with handshake, auto-start if needed
func connectBroker(paths config.Paths, sessionName string) (*client.Client, error) {
    // Check if broker is running, auto-start if not
    if err := ensureBroker(paths); err != nil {
        return nil, err
    }
    c, err := client.Connect(paths.Socket)
    if err != nil {
        return nil, fmt.Errorf("connect to broker: %w", err)
    }
    // Handshake — use empty name for anonymous one-shot commands
    name := sessionName
    if name == "" {
        name = fmt.Sprintf("cli-%d", os.Getpid())
    }
    resp, err := c.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: name})
    if err != nil || !resp.OK {
        c.Close()
        return nil, fmt.Errorf("handshake failed")
    }
    return c, nil
}

// Helper: send command with clean disconnect
func sendCommand(c *client.Client, req protocol.Request) error {
    resp, err := c.Send(req)
    // Clean disconnect
    c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
    c.Close()
    if err != nil {
        return printErr(protocol.ErrInternal, err.Error())
    }
    return printJSON(resp)
}

// Helper: print JSON to stdout
func printJSON(v any) error {
    enc := json.NewEncoder(os.Stdout)
    return enc.Encode(v)
}

// Helper: print error JSON to stderr, return error for exit code
func printErr(code, msg string) error {
    resp := protocol.ErrResponse(code, msg)
    enc := json.NewEncoder(os.Stderr)
    enc.Encode(resp)
    return fmt.Errorf(msg)
}

// Helper: ensure broker is running
func ensureBroker(paths config.Paths) error {
    if paths.Socket == "" {
        return fmt.Errorf("cannot determine socket path (HOME not set?)")
    }
    if broker.IsRunning(paths.PID) {
        return nil
    }
    // Auto-start
    return broker.StartDaemon(paths)
}
```

### Each command follows the pattern:

```go
// cmd/task_create.go
var taskCreateCmd = &cobra.Command{
    Use:   "create [payload]",
    Short: "Create a new task",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        paths, err := resolvePaths()
        if err != nil { return printErr(protocol.ErrBrokerNotRunning, err.Error()) }

        c, err := connectBroker(paths, "")
        if err != nil { return printErr(protocol.ErrBrokerNotRunning, err.Error()) }

        return sendCommand(c, protocol.Request{
            Cmd:            protocol.CmdTaskCreate,
            Payload:        json.RawMessage(args[0]),
            Type:           typeFlag,
            Tags:           tagsFlag,
            DependsOn:      dependsOnFlag,
            Priority:       &priorityFlag,
            MaxRetries:     &maxRetriesFlag,
            IdempotencyKey: idempotencyKeyFlag,
        })
    },
}

func init() {
    taskCmd.AddCommand(taskCreateCmd)
    taskCreateCmd.Flags().StringVar(&typeFlag, "type", "", "Task type")
    taskCreateCmd.Flags().StringSliceVar(&tagsFlag, "tags", nil, "Comma-separated tags")
    taskCreateCmd.Flags().Int64SliceVar(&dependsOnFlag, "depends-on", nil, "Dependency task IDs")
    taskCreateCmd.Flags().IntVar(&priorityFlag, "priority", 0, "Priority (higher = first)")
    taskCreateCmd.Flags().IntVar(&maxRetriesFlag, "max-retries", 0, "Max crash retries")
    taskCreateCmd.Flags().StringVar(&idempotencyKeyFlag, "idempotency-key", "", "Dedup key")
}
```

## Commands to implement (11a — MVP set)

| Command | Args | Flags | Notes |
|---------|------|-------|-------|
| `waggle start` | none | `--foreground` | foreground runs broker inline; without flag, calls StartDaemon |
| `waggle stop` | none | none | sends stop command to broker |
| `waggle status` | none | none | |
| `waggle task create` | payload (JSON) | --type, --tags, --depends-on, --priority, --max-retries, --idempotency-key | |
| `waggle task list` | none | --state, --type, --owner | |
| `waggle task claim` | none | --type, --tags | returns claim_token in response |
| `waggle task complete` | id, result | --token (required) | |
| `waggle task fail` | id, reason | --token (required) | |
| `waggle lock` | resource | none | uses session name from PID |
| `waggle unlock` | resource | none | |
| `waggle locks` | none | none | |

## Acceptance criteria

- [ ] `go build -o waggle .` succeeds
- [ ] `./waggle --help` shows subcommands
- [ ] `./waggle task --help` shows task subcommands
- [ ] Full smoke test passes (see Rule 1c in orchestrator prompt)
- [ ] `go vet ./...`
- [ ] Commit: `feat(cli): core commands — start, stop, status, task CRUD, locks`
