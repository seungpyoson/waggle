# Codex provider source checkpoint — September 7

Status: **Startup timing is implemented in the isolated source; all 104 analytics tests pass and the App Server integration test binary builds. Native startup behavior remains unconfirmed.** Earlier checkpoints passed 298 protocol tests and 177 hooks tests. The user Terminal run passed two subscription tests and exposed an invalid history-read assumption in the third; that corrected test still needs execution. M1 remains incomplete. The user approved proceeding with the identified provider source prerequisites. No installed executable, live provider session, user settings, or database was changed.

The isolated checkout is `.worktrees/codex-native`, branch `feat/native-session-enrollment`, based on upstream `rust-v0.153.4`, commit `3d2ee51ca2d5db578f328aa75e20aa22c0197c9a`. The attempt to create a checkout on Dev-OWC failed with `Operation not permitted` even after escalation was approved. The workspace clone succeeded. The accompanying patch preserves this draft outside the ignored checkout; its digest and per-file hashes are in `codex-provider-source-2026-09-07.json`.

## Boundary and production evidence

The invariant for this slice is that subscribing to a conversation attaches only an already loaded native thread, without loading stored history or starting a model turn. The App Server's existing thread subscription and unload lifecycle is the sole owner of this invariant.

The pinned source has `thread/unsubscribe` but no corresponding explicit subscription request. `thread/read` does not subscribe. `thread/resume` can load persisted history when the thread is absent, so a preceding read cannot make resume enforce a loaded-thread-only contract. These production paths are documented in the implementation status report.

The draft adds experimental `thread/subscribe` to the public v2 decoder and the existing per-thread request serialization. It delegates directly to `ensure_conversation_listener`; it adds no registry, listener implementation, resume branch, credential, or native permission override. The canonical helper now resolves the loaded runtime inside the existing pending-unload lock, alongside subscription registration. This removes the earlier interval in which an acquired runtime reference could outlive an unload before subscription admission.

Subscription preserves the existing native semantics: explicit archive, delete, or shutdown may still close the thread. It does not claim permanent readiness or delivery acceptance. Waggle has not yet been changed to call this draft API.

## Per-thread hook environment

`build_hooks_config` now supplies the explicit `shell_environment_policy.set` values from the selected local environment, or the thread configuration when there is no local environment. The existing hook configuration builder applies these values over its captured process environment. Both command hooks and legacy notifications use that projection. Per-handler overrides and the existing protected-variable scrub remain in their existing command construction path. This does not apply shell inheritance filters to hooks or change their permission class.

Reconfiguration rebuilds the projection from the original inherited snapshot. It shares the existing asynchronous task owner, while already admitted hooks retain their captured environment. Rotation and removal therefore affect subsequent hooks without changing another thread or losing in-flight work. Default-shell selection follows the same last-value-wins order as command environment construction.

The new regression runs real hook command processes for two distinct thread identities in the same directory, then rotates and removes one thread's override. It observes values through the normal hook output path and checks that a configured protected launch variable is scrubbed. The regression first failed with a missing override and then passed. A second regression exposed first-value shell selection and passed after making selection match environment assignment order. All 177 hooks tests passed, including background-task reconfiguration and non-Unicode environment preservation. These are hook-runtime tests; ordinary shell-tool credential propagation, broker credential issuance, fork/resume/switch isolation, and remote-TUI enrollment remain not confirmed.

## Startup execution and first-turn effects

The isolated provider now executes root-thread SessionStart during native initialization, after history initialization and before the submission loop is started. Startup, resume, and clear events no longer need a first tool call or model turn. The existing pending-start queue holds either a turn-scoped trigger or the completed startup outcome. The first turn consumes that outcome at its original hook boundary, preserving context insertion order and the one-time stop effect without executing the hook again. SubagentStart retains its actual child-turn identity, and compaction hooks retain their turn-scoped behavior.

Hook reporting explicitly distinguishes session and turn scope. Startup notifications and analytics retain an absent turn ID; they do not substitute a later turn ID. Existing turn event delivery, metrics, memory controls, hook timeouts, and trust checks remain in their existing paths. A startup with no matching hook does not materialize a transcript merely to create unused hook input.

All 104 analytics tests pass, including a reducer regression for startup with a null turn ID. The App Server integration test binary builds, and final Clippy for App Server, core, hooks, and analytics passes with one unchanged upstream unused-import warning. Rust formatting exits successfully; stable rustfmt reports that it ignores the nightly-only imports-granularity option. Full repository formatter, automatic fix, and Bazel gates remain open for their previously recorded environment limits. The public RPC regression creates a fresh thread and observes its exact hook identity before any turn is submitted; it then checks that a startup stop prevents the first model request, the next turn receives the saved context, and the hook never runs again. Execution of that regression is not confirmed: the known test-socket restriction prevents this session from running the App Server fixtures. Compilation and final source evidence are recorded separately in the JSON.

Broker enrollment already stores a pending incarnation after checking the configured provider and endpoint; later observation verifies native readiness. Startup can register that pending enrollment before the native thread appears in ThreadManager and return without waiting for a model turn. The Waggle credential handoff and real fresh-idle round trip are still not implemented or confirmed.

## Native start-hook identity

Further source inspection found that `Session::session_id()` returns the family identity shared by the root and descendant threads (`core/src/session/session.rs`). The start-hook producer used that value as `session_id`, and neither SessionStart nor SubagentStart exposed a separate `thread_id`. That field cannot identify an exact native thread for enrollment in all supported lifecycles.

The provider now supplies a required `thread_id` from `sess.thread_id()` in the existing typed start-hook request. SessionStart and SubagentStart serializers expose it alongside the preserved `session_id`. The generated hook schemas declare the field. Existing request constructors were updated; no identity inference, credential, provider setting, or new hook execution path was added.

A regression through the existing hook dispatcher first failed because the `${thread_id}` input placeholder was absent. After the change it passed for both event types with distinct generated family and thread IDs. This test uses a fake MCP hook executor to inspect the serialized hook payload; it is a provider payload test, not a Waggle transport or enrollment path. All 176 hooks tests then passed without skips, including generated-schema validation. Core/hooks Clippy passed with one unused-import warning in the unchanged upstream `core/tests/suite/openai_file_mcp.rs`. Rust formatting and whitespace checks passed; the previously blocked full formatter and fix gates remain open. The JSON records exact commands and logs.

At the identity-only checkpoint, startup timing and environment propagation were still unchanged. The subsequent changes are described above. Exact identity alone does not establish Waggle credential propagation, fresh-idle delivery, or fork/resume isolation.

## Validation and remaining work

Three new regression tests exercise public JSON-RPC: attachment without a turn, rejection of stored history without loading it, and two-client attachment followed by original-client disconnect, a correlated turn completion, archive, and rejected reattachment. The first two use the existing stdio fixture; the third uses the existing TCP WebSocket fixture. All compile. Both execution attempts, including approved escalation, failed all three tests in fixture setup: Wiremock could not bind a local OS port (`Operation not permitted`). No subscription assertions ran. The second attempt disabled retries; further identical attempts were stopped. Actual Unix transport with a remote TUI, unload races, and interrupted subscription remain unconfirmed.

### User Terminal run and test correction

The user's subsequent run `04daf0eb-6da8-4fc1-805d-62ca3aaca909` executed all three selected tests: rejection of saved history and the two-client disconnect/completion/archive scenario passed. The fresh-idle test successfully received its exact `thread/subscribe` response, then failed at `thread/read(includeTurns=true)` with `-32601: list_turns is not supported yet`. This is user-supplied execution evidence; the transcript contains no source digest and was not independently rerun here.

The test incorrectly assumed turn history was queryable before any first turn. Direct inspection confirms that `thread/read(includeTurns=true)` delegates paginated threads to the store's turn-list operation, which can explicitly report unsupported. Changing history mode or supplying a warm-up turn would change the scenario. The correction keeps the default fresh thread and checks its exact ID, idle status, and affirmative direct-input availability through a metadata-only read. It then verifies subscription removal, awaits graceful server exit, and asserts zero model requests recorded by the existing Responses fixture. It does not inspect the empty `turns` field from a metadata-only response. This changes only the regression test; the provider implementation is unchanged. The first corrective build caught the optional type of `canAcceptDirectInput`; the expected value now requires `Some(true)`.

The corrected integration test binary builds successfully, and scoped App Server Clippy passes. Rust formatting and `git diff --check` pass. Full repository formatting remains blocked by the previously recorded tooling/cache limits; identical failed attempts were not repeated. The corrected behavioral test has not executed here. The pre-correction patch is retained alongside the current patch, and the JSON distinguishes the user's run from independent compilation and lint evidence.

Both baseline `just test --cargo-profile dev-small -p codex-app-server-protocol --lib` attempts failed before compilation: the configured network proxy rejected `https://index.crates.io/config.json` with CONNECT 403 while resolving `futures`. The second attempt received escalation approval but encountered the same network rejection. Further network retries were stopped.

### Release lockfile correction

The user's subsequent normal-Terminal `cargo fetch --locked` reached the index but rejected the release lockfile. An independent `cargo metadata --offline --locked` reproduced that lockfile error. Both `Cargo.toml` and `Cargo.lock` were still byte-for-byte the upstream tag's files: the workspace version was `0.153.4`, while all 149 local package entries in the lockfile were `0.0.0`.

Cargo regenerated the stale local metadata through `cargo metadata --offline --format-version 1`. A full parsed-object comparison against the original lockfile verified that the only changes were those 149 local version values, now `0.153.4`; every external package record, dependency list, checksum, and other value remained identical. The next locked resolver reached a missing-crate error. The user then completed `cargo fetch --locked`, downloading 653 crates. Independent `cargo metadata --offline --locked --format-version 1` now exits 0. The dependency prerequisite is resolved.

The required `just bazel-lock-update` was attempted after this generated lockfile change and exited 127 because `bazel` is not installed. `MODULE.bazel.lock` was not edited by hand; its validation remains unconfirmed.

### Schema generation and checks after dependency download

The first compiled protocol run passed 297 tests and failed only the experimental precomputed-export comparison. The documented `just write-app-server-schema` entrypoint then failed because it referenced a nonexistent `write_schema_fixtures` binary. The recipe now invokes the existing Python generator, which invokes the existing ignored Rust generator test through the required `just test` runner. It fails on an empty test selection and does not retry generation. This repairs the single documented entrypoint; no generated schema was edited by hand.

Both normal and `--experimental` generation commands passed. Only the experimental precomputed artifact changed. The subsequent `just test --offline --locked -j 2 -p codex-app-server-protocol --lib` passed all 298 regular tests; the one generator test remains intentionally ignored in this normal run. It was executed successfully in each generation command.

`just clippy -p codex-app-server --profile dev-small --offline --locked -j 2` passed, including test compilation. The required `just fix` attempt failed before linting because Cargo could not bind its coordination TCP listener. Read-only Clippy does not establish completion of that fix gate.

The final `just fmt` completed its Rust and Just groups but exited 1 because `dotslash` is missing and both Python formatter groups could not write the default uv cache. The touched generator script passed a separate `ruff format --check` with installed Ruff 0.15.18; this does not establish the repository's pinned Python formatter gate. `git diff --check` passed. The full Rust suite was not run because the targeted server tests remain blocked. Exact commands, exits, log digests, and source hashes are in the accompanying JSON.

The next behavioral check needs the user's normal Terminal:

```sh
bash docs/reviews/native-messaging/run-codex-startup-checks.txt
```

The runner validates the saved native HEAD, complete patch, and file hashes before and after the subscription and startup tests. It disables retries and saves a uniquely named log in this evidence directory. It does not install the modified executable.

Corrected subscription and startup execution, per-thread Waggle hook/shell credentials, retained driver connections, Claude conformance, migration, and milestone reviews remain unfinished. No M1 approval is claimed.
