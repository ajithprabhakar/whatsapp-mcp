# Normal WhatsApp call-log updates

## 1. Outcomes
Normal `events.AppState` CallLogAction records reach the existing calls store without waiting for full history sync. The same source ID replayed through normal and history paths keeps one row, earliest time, known media and owner summary. Invalid records produce sanitized counts/reasons; storage failures produce ERROR diagnostics.

## 2. Scope
User explicitly approved “Include normal updates too”. Add only normal CallLogAction dispatch. No snapshot fetching, pairing, delete handling, schema/dependencies/API changes, Pocket inference or production test writes. Release remains coordinator-owned.

## 3. Root cause and constraints
The registered event callback only intercepts live call events and HistorySync; normal generic AppState updates fall through the switch. The pinned SDK embeds `*waSyncAction.SyncActionValue` in `events.AppState`, exposing `GetCallLogAction().GetCallLogRecord()`. Reuse `historyCallObservation` unchanged for seconds/millis/micro timestamps, explicit direction, owner aliases and individual unambiguous participant validation. Reuse existing calls upsert unchanged. Unrelated/nil AppState passes through without errors; present CallLogAction with nil record is invalid, never synthesized.

## 4. Prior decisions / grounding / reuse
Based on reviewed deployed `2f286eb2` history capture. Reuse canonical Maya DOP and `.artifacts/history-calls/` grounding, Graphify query and review evidence. External Go remains outside Serena support; degraded bounded rg inspected registered dispatch, call persistence and pinned SDK events/protobuf definitions. New branch `codex/whatsapp-appstate-calls-20261101` isolates this approved extension.

| New surface | Existing responsibility searched | Why needed | Reuse |
|---|---|---|---|
| handleCallEvent dispatch helper in calls.go | rg recordCallEvent, handleHistorySync, events.AppState; pinned CallLogAction | registered closure cannot be exercised without starting production runtime; move existing live dispatch/logging into one testable entry point with normal-update handling | recordCallEvent, historyCallObservation, storeCallObservation |
| normal-update regression tests in calls_test.go | existing in-memory callStore and actual history handler tests | normal input envelope and passthrough behavior previously untested | same fixtures, SDK records and capturing logger |

## 5. Ordered plan
1. Add red regression against existing dispatcher: AppState CallLogAction currently unhandled and writes no row; include history/normal dedup, passthrough, invalid and storage ERROR diagnostics.
2. Two narrow independent Sonnet prebuild reviews of source contract, spec, plan and red tests; retain earlier history validation review evidence.
3. Extract the existing registered call dispatch into `handleCallEvent`; add only approved AppState branch using shared normalizer/upsert. Register that entry point in main.
4. Full Go tests, race, vet, isolated build, diff check; independent final full-source review to zero. Commit with hooks; no push/merge/deploy. Revert commit for rollback.

## 6. Verification
```sh
cd whatsapp-bridge
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/whatsapp-appstate-call-check .
```
Actual dispatch proof uses fabricated SDK AppState events and in-memory SQLite, never production sessions or calls. Live release proof must observe genuine update delivery and source-row freshness; no retrospective recovery claim follows merely from code coverage.

## Local evidence before release
- Lead: Codex. Independent reviewer: Claude Sonnet through the configured external adapter.
- `.artifacts/appstate-calls/red.txt`: existing dispatcher returned `handled=false` for an actual SDK AppState CallLogAction. `dispatcher-red.txt` records planned extracted dispatcher absent before implementation.
- Prebuild reviews 1–2 and final full-source review: zero Critical/Important findings. Receipts/source packets are `.artifacts/appstate-calls/{pre-1.json,pre-2.json,final.json,pre.txt,final.txt}`.
- Full suite: 13 top-level tests, 48 passing tests including subtests; eight normal-update tests including subtests. `tests.jsonl` retains actual-dispatch replay proof; `race.txt` confirms full race suite passed. Vet, isolated build and diff check exited 0.
- No history-normalization/upsert changes, fullsync event enabling, snapshot fetching, pairing or delete handling. No production source writes, push, merge or restart. Coordinator owns release and genuine normal-update liveness proof.
