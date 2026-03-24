# Task 11: CLI Commands — Full cobra Tree

**Files:**
- Create: `main.go`
- Create: `cmd/root.go` and all command files (see file structure in main plan)
- Depends on: Tasks 1 (config), 8 (client), 9-10 (broker)

Each command follows the same pattern: parse flags, build protocol.Request, send via client, print Response as JSON to stdout.

- [ ] **Step 1: Add cobra dependency**

Run: `cd ~/Projects/Claude/waggle && go get github.com/spf13/cobra@latest`

- [ ] **Step 2: Create main.go**

```go
// main.go
package main

import "github.com/seungpyoson/waggle/cmd"

func main() {
    cmd.Execute()
}
```

- [ ] **Step 3: Create root command with auto-start logic**

`cmd/root.go`:
- Detect project root via `config.FindProjectRoot(cwd)`
- Compute paths via `config.NewPaths(root)`
- `PersistentPreRun`: for non-start commands, check if broker is running (`lifecycle.IsRunning`), auto-start if not
- JSON output helper: `printJSON(v any)` that marshals and prints to stdout
- Error output helper: `printErr(code, message)` that prints error JSON and exits with code 1

- [ ] **Step 4: Implement daemon commands (start, stop, status)**

`cmd/start.go`:
- `--foreground` flag: run broker inline (used by daemon fork)
- Without `--foreground`: call `lifecycle.StartDaemon`, wait briefly, verify PID

`cmd/stop.go`:
- Connect to broker, send `stop` command

`cmd/status.go`:
- Connect to broker, send `status`, print response

- [ ] **Step 5: Implement session commands (connect, disconnect)**

`cmd/connect.go`:
- `--name` flag (required)
- Send connect request, print session info

`cmd/disconnect.go`:
- Send disconnect request

- [ ] **Step 6: Implement event commands (publish, subscribe)**

`cmd/publish.go`:
- Args: topic, message
- Send publish request

`cmd/subscribe.go`:
- Args: topic
- `--last N` flag
- Send subscribe request, then loop reading stream events and printing to stdout
- Handle SIGINT for graceful disconnect

- [ ] **Step 7: Implement task parent command and all subcommands**

`cmd/task.go`: parent command grouping all task subcommands

`cmd/task_create.go`:
- Args: payload (JSON string)
- Flags: `--type`, `--tags`, `--depends-on`, `--lease`, `--max-retries`, `--priority`, `--idempotency-key`

`cmd/task_list.go`:
- Flags: `--state`, `--type`, `--owner`

`cmd/task_claim.go`:
- Flags: `--type`, `--tags`
- Print claim response including claim_token

`cmd/task_complete.go`:
- Args: task_id, result
- `--token` flag for claim token

`cmd/task_fail.go`:
- Args: task_id, reason
- `--token` flag

`cmd/task_heartbeat.go`:
- Args: task_id
- `--token` flag

`cmd/task_cancel.go`:
- Args: task_id

`cmd/task_get.go`:
- Args: task_id

`cmd/task_update.go`:
- Args: task_id, message

- [ ] **Step 8: Implement lock commands**

`cmd/lock.go`:
- Args: resource

`cmd/unlock.go`:
- Args: resource

`cmd/locks.go`:
- No args, lists all locks

- [ ] **Step 9: Build and verify help output**

Run: `cd ~/Projects/Claude/waggle && go build -o waggle . && ./waggle --help`
Expected: Clean help with all subcommands

Run: `./waggle task --help`
Expected: Task subcommands listed

- [ ] **Step 10: Commit**

```bash
git add main.go cmd/
python3 ~/.claude/lib/safe_git.py commit -m "feat: CLI commands — full cobra command tree"
```
