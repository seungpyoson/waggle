# M0 Codex review — revision 4

Verdict: **APPROVE — M0 design only. Blocking findings: none.**

Dispatch: fresh collaboration reviewer `m0_r4_astra_review`, explicitly configured with `model=gpt-6-astra`, `reasoning_effort=xhigh`, `fork_turns=none`. Identity evidence is the dispatch configuration, not self-description.

Reviewed document: `docs/native-messaging-plan.md`.

Document SHA256: `e18636f22a2b970cc5a75384fe29abd6eb6a21361920f109a5435ffee62473b4`.

Base HEAD: `5fac0530a789f54b2a87fb10d9d21a14b2dded91`.

Branch: `feat/native-agent-messaging`.

The reviewer independently read the complete plan and verified the same HEAD/digest before its final verdict. No drift. Prior approval reports were not evidence for this verdict.

The reviewer found the fresh-idle contradiction resolved at the design level: verified endpoint/conversation membership and native availability establish receive-binding without an ordinary tool call; outbound authentication is per call and credential propagation is an M1 conformance proof. Delayed native readiness uses the same endpoint; reconnection restores existing enrollment rather than discovering an unenrolled session.

It also found coherent delivery-envelope/receipt semantics, retained-input barriers, process-lifetime ownership, bounded shutdown reporting, predecessor cleanup, named cutover cessation, active executable retirement, canonical activation state, and exact Codex thread membership on the selected endpoint.

Mandatory later evidence:

1. M1: a fresh SessionStart enrollment with no prior ordinary tool calls, including readiness after the hook returns; first inbound wake and authenticated acknowledgement/reply without credentials in prompts. Prove Codex endpoint membership, plain-session rejection, credential isolation across fork/resume/switch, unchanged policies, and Claude independent broker lineage plus actual external outcomes.
2. M2: ownership races/suspended owners/stale handles/callbacks, shutdown timeout and interrupted cleanup, no resubmission of retained input, cessation failure detection, actual old-binary isolation, executable retirement, snapshot/task preservation and interrupted conversion/activation.
3. M3: real sessions, successful build/full tests, degraded help, installation/terminal checks, and both required final reviews with verified dispatch metadata. Blocked or skipped evidence remains not confirmed.

Checks were read-only plan/source, official OpenAI documentation, local Codex version/help and Git/digests. Claude documentation refresh and router diagnostic failed with PermissionError; the reviewer did not independently refresh those semantics. No builds, tests, provider sessions or vulnerability reproductions were run. No files were edited; `GEMINI.md` was preserved.

The verdict does not establish runtime correctness or authorize implementation without Claude approval of the same revision. User-data cutover and release completion remain unconfirmed.
