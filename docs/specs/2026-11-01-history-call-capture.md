# History call capture

## 1. Outcomes
Valid incoming and outgoing history call records reach the existing calls table once, retaining owner summaries and observed media. Invalid or ambiguous records are skipped with sanitized reason counts; one persistence failure does not stop a batch.

## 2. Scope
Extend the existing history handler and call persistence only. No schema, dependencies, Pocket inference, new producer, pairing, deployment or production database replay.

## 3. Constraints / root cause
`handleHistorySync` reads Conversations but ignores the pinned SDK's CallLogRecords. This proves a coverage gap; it does not prove delivery of the missing November 1 call. Existing live-event outgoing capture already exists. Require explicit direction, source ID, seconds timestamp within WhatsApp era and five minutes of present, valid individual creator/participants; reject group/link/nonregular/self/ambiguous identities. Preserve PN/LID namespaces without guessing aliases. Diagnostics contain counts and fixed reasons only.

## 4. Prior decisions and grounding
Reuse Maya `.artifacts/pocket-call-match/outgoing-diagnosis.md` and its two zero-finding Sonnet reviews. Graphify query performed by coordinating lead; external Go is outside Serena support, so degraded mode uses bounded rg over calls.go, main.go and exact pinned SDK protobuf. Existing `callPartner`, `storeCallObservation`, calls table and lifecycle tests are the reuse targets. No alternate persistence surface.

## Reuse Audit
| New surface | Existing search | Why needed | Reuse |
|---|---|---|---|
| history normalization in calls.go | rg handleHistorySync, recordCallEvent, storeCallObservation, CallLogRecord in bridge and pinned SDK | history differs from live metadata and needs validation before shared storage | existing callObservation/upsert |
| regression tests | calls_test.go and callStore | history input currently untested | existing in-memory SQLite fixture |

## 5. Ordered plan
1. Add failing actual-handler history regression using in-memory store and synthetic SDK records.
2. Independent Sonnet review of spec, plan and red tests (at least two rounds, five conservative boundary lenses).
3. Normalize history conservatively, persist through existing upsert, emit sanitized counters. Add boundary and batch tests.
4. Run full Go tests, race, vet and build; independent final review to zero. Commit locally with hooks. Coordinator owns deployment and live proof; rollback is reverting this commit.

## 6. Verification
```sh
cd whatsapp-bridge
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/whatsapp-history-call-check .
```
Actual-handler replay uses synthetic records and isolated in-memory SQLite only. Production recovery depends on WhatsApp delivering history; no claim of recovery until source rows are observed after coordinated release.

## Verification evidence (local, before release)
- Lead: Codex; independent reviewer: Claude Sonnet through the configured external adapter.
- `.artifacts/history-calls/red.txt`: actual handler count 0 versus expected 1, plus boundary/batch regression failures before implementation.
- Five prebuild reviews: rounds 1–3 zero; round 4 requested stronger direction, namespace, timestamp, media and diagnostic tests; amended suite passed round 5 with zero Critical/Important findings.
- Full Go suite: 10 top-level tests, 36 passes including subtests; 29 history passes including subtests. `tests.jsonl` contains the exact actual-handler replay.
- `race.txt`: race-enabled full suite passed. `go vet ./...` and isolated build both exited 0; `git diff --check` passed.
- Final implementation review rounds 1–2: zero Critical/Important findings. Review receipts and supplied source packets are under `.artifacts/history-calls/`.
- Release proof remains coordinator-owned: merge reviewed commit, build exact SHA, deploy, observe sanitized live/history diagnostics and source calls freshness without synthetic production writes. A history resend is not guaranteed.
