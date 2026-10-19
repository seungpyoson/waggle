# Fallback root-cause audit — September 7

The PID-zero spawn path has been removed, but the broader no-fallback architecture is incomplete. This audit supersedes any inference that passing the focused driver or launch tests establishes that architecture.

The earlier answers addressed a narrower question: whether failure after a native submission starts another submission. That did not cover selection before execution, reporting success without the requested evidence, or requiring ownership authority at the provider boundary. Calling a path pre-existing does not discharge those obligations.

## Evidence and causal chain

1. **Why does resource substitution remain?** `internal/spawn/terminal.go:75` continues to another terminal after any `LookPath` error. The Linux launch code was consolidated while preserving that behavior. A missing executable and an unusable executable can therefore enter the same candidate-selection path.
2. **Why can execution choose a substitute?** `OpenTab` accepts `LinuxDefault`, an unresolved category, and contains both the candidate policy and the effectful launch. Its input does not identify one already-selected executable.
3. **Why was the retained behavior missed by verification?** `TestOpenTabFailedLinuxLauncherDoesNotSwitchTerminals` deliberately lets lookup succeed and injects failure at execution. Passing it proves only that later boundary. It does not reject selection changes after lookup failure.
4. **Why did deleting the PID machinery not close the original requirement?** The deleted registry was one occurrence. Other contracts still permit the same class of weaker guarantees: `cmd/status.go:116` converts a failed status RPC into `ok: true`; `internal/native/connector.go:39` accepts configuration and ordinary enrollment data without an ownership-issued capability; `internal/broker/scheduler.go:102` and `:140` independently open and close connections for observation and submission.
5. **What is the actionable root cause?** The implementation and checks have been organized around individual functions and named legacy paths. Selection policy, authority to act, resource lifetime, and evidence sufficient for success have not all been made mandatory at their owning boundaries. Replacing one branch cannot establish those missing contracts.

## Required correction

- Resolve an explicit configured terminal/provider binding before effects. Execution receives that binding and cannot enumerate alternatives after failure. Defaults belong in config; unavailable or invalid selections produce an error.
- Make provider access require authority minted from an admitted ownership lifetime. Enrollment structs describe identity; their fields alone must not authorize native access. Fence persistent changes inside the acting transaction.
- Own native subscriptions through binding, observations, dispatch, retirement, and shutdown. Reattachment must join the previous connection's work and preserve committed attempts and uncertainty. Connection resources must not become a second canonical readiness registry.
- Report only evidence actually obtained. A failed status operation must not produce a successful status result. Keep launch-request acceptance, native readiness, input possession, and consumption distinct.
- Test missing and inaccessible configured resources, unsupported protocols, stale ownership, changed bindings, loss of observations, and shutdown with work still active. Exercise the entire production lifecycle and verify that forbidden entrypoints cannot obtain authority. Keep the existing no-resubmission tests.

## Verification and limits

The previous PID-lookup regression failed for Terminal and iTerm before removal and now passes. Focused race tests pass for launch failure, cancellation before launch, no alternate launch after execution failure, missing broker, all CLI help in a degraded directory, and rejection of removed spawn RPCs. All packages compile with the race detector; the application build and `go vet ./...` pass. Builds, caches, and temporary files used Dev Envoy.

Confidence is high in the source/API findings and the stated scope of those executed tests. The lookup-selection and status-result branches are source-inspected findings; their missing behavioral regressions still need implementation. Actual native provider behavior, full subscription lifetime, and the complete behavioral suite remain unconfirmed. No provider sessions or installed integrations were changed by this audit.
