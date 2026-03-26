# Task 11b: Remaining CLI Commands

**Branch:** `feat/11-cli` (same branch as 11a, committed after)
**Files:** `cmd/connect.go`, `cmd/disconnect.go`, `cmd/publish.go`, `cmd/subscribe.go`, `cmd/task_heartbeat.go`, `cmd/task_cancel.go`, `cmd/task_get.go`, `cmd/task_update.go`
**Depends on:** Task 11a

These are the non-MVP commands. They follow the exact same pattern as 11a.

## Commands

| Command | Args | Flags | Notes |
|---------|------|-------|-------|
| `waggle connect` | none | --name (required) | Long-lived — prints session info, stays connected |
| `waggle disconnect` | none | none | Sends disconnect to broker |
| `waggle publish` | topic, message | none | |
| `waggle subscribe` | topic | --last N (deferred, accept flag but ignore) | Long-lived — streams events to stdout, handles SIGINT |
| `waggle task heartbeat` | id | --token (required) | |
| `waggle task cancel` | id | none | |
| `waggle task get` | id | none | |
| `waggle task update` | id, message | none | Future: append progress note. For now return "not yet implemented" |

**Special: subscribe command**
This is the one long-lived command. After handshake + subscribe request:
```go
// Read events from broker and print to stdout
for {
    line, err := c.ReadStream()
    if err != nil {
        break
    }
    fmt.Println(string(line))
}
```
Handle SIGINT to send clean disconnect:
```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt)
go func() {
    <-sigCh
    c.Send(protocol.Request{Cmd: protocol.CmdDisconnect})
    c.Close()
    os.Exit(0)
}()
```

## Acceptance criteria

- [ ] All commands exist and have --help
- [ ] `waggle subscribe` streams and handles Ctrl+C
- [ ] `go build -o waggle .` still succeeds
- [ ] `go vet ./...`
- [ ] Commit: `feat(cli): remaining commands — connect, disconnect, publish, subscribe, task extras`
